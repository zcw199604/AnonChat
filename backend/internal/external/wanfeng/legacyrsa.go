package wanfeng

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
)

// 对方站点有两套回参加密方案（逆向自 app.js，2026-11-07 抓取）：
//
//  1. v3 信封：`v3.<iv>.<ct>`，密钥来自 ECDH 握手（本包 crypto.go/session.go 已实现）。
//  2. 「旧格式」：响应 data 是一个 base64 串，解码后是若干段 base64 密文用 `|` 连接；
//     每段用对方 JS 里内嵌的 **RSA 私钥**逐段解密，拼起来再 base64 解码得到 JSON。
//
// 站点 JS 里的逻辑（app.js）：
//
//	function Gs(e){const t=rn(e);let n="";for(const o of t.split("|")){...n+=ti.decrypt(o)}return JSON.parse(rn(n))}
//
// 实测：媒体上传接口 `/api/tools/niming-file/upload` 的 data 走的就是旧格式，
// 不解析会把整段密文当成 filePath（进而拼出非法媒体地址，对方看不到我们发的图）。
//
// ⚠️ 这把私钥是对方前端公开下发的（任何访问者都能从 JS 里拿到），只用于解对方自己的回参；
// 对方换密钥时可用 `WANFENG_LEGACY_RSA_KEY` 环境变量覆盖（整段 PEM）。
const legacyRSAPrivateKeyPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIBOwIBAAJBAKUX1WcGR7eHEOCNhN/Jo4SmtONnLhZMNxlxBEshxfNaMFW8nL0a
eXNOX2ylJltSjDbVpfINeBT7MfXktULZI8sCAwEAAQJAZvE6lvgahfzJcEuuO+C+
fnt4tYdj/HFfthR7qeGQThG/ev0qVv8JYuivTLeKFGQmt3uFuAAxPF7yyzSQQ2J7
cQIhAOeBF1jJBWIiBGo3dIBsYR15i9beqaHR9k/JmjLGruMjAiEAto/RxD//9vYq
I5ThuP8sTLgTIX0V76EuGMeq/jjquzkCIDoj3oQqwBJcLrWqRbT3AxDO9MK62ulL
/Mse+33jDgr3AiEAsMPQeBT/4j+3zXv6kwV//0i8PVDATKXj3Fp4Vm8F2QECIQDb
dOMuqVftSVHyAB4rHHRk0a1xSexYwx5gx9nMmQkGEw==
-----END RSA PRIVATE KEY-----`

var (
	legacyKeyOnce sync.Once
	legacyKeyN    *big.Int
	legacyKeyD    *big.Int
	legacyKeySize int
	legacyKeyErr  error
)

// legacyRSAKey 懒加载私钥（支持环境变量覆盖）。
func legacyRSAKey() (*big.Int, *big.Int, int, error) {
	legacyKeyOnce.Do(func() {
		text := legacyRSAPrivateKeyPEM
		if override := strings.TrimSpace(os.Getenv("WANFENG_LEGACY_RSA_KEY")); override != "" {
			text = override
		}
		block, _ := pem.Decode([]byte(text))
		if block == nil {
			legacyKeyErr = fmt.Errorf("旧格式私钥 PEM 解析失败")
			return
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			legacyKeyErr = fmt.Errorf("旧格式私钥解析失败: %w", err)
			return
		}
		legacyKeyN = key.N
		legacyKeyD = key.D
		legacyKeySize = (key.N.BitLen() + 7) / 8
	})
	return legacyKeyN, legacyKeyD, legacyKeySize, legacyKeyErr
}

// legacyRSADecryptPart 解一段 RSA（PKCS#1 v1.5）密文。
//
// 直接用大数运算而不是 crypto/rsa：对方用的是 512 位密钥，Go 1.24+ 的 rsa 包会以
// 「密钥过短」为由拒绝，而这里的数据来自对方自己的公开 JS，仅用于解对方回参。
func legacyRSADecryptPart(part string) ([]byte, error) {
	n, d, size, err := legacyRSAKey()
	if err != nil {
		return nil, err
	}
	cipher, err := base64.StdEncoding.DecodeString(strings.TrimSpace(part))
	if err != nil {
		return nil, fmt.Errorf("旧格式密文分段解码失败: %w", err)
	}
	if len(cipher) != size {
		return nil, fmt.Errorf("旧格式密文长度异常: got=%d want=%d", len(cipher), size)
	}
	m := new(big.Int).Exp(new(big.Int).SetBytes(cipher), d, n)
	plain := m.Bytes()
	if len(plain) < size { // 补齐前导 0（PKCS#1 首字节一定是 0x00）
		padded := make([]byte, size-len(plain), size)
		plain = append(padded, plain...)
	}
	if len(plain) < 11 || plain[0] != 0x00 || plain[1] != 0x02 {
		return nil, fmt.Errorf("旧格式填充异常")
	}
	idx := 2
	for idx < len(plain) && plain[idx] != 0x00 {
		idx++
	}
	if idx >= len(plain) {
		return nil, fmt.Errorf("旧格式填充分隔符缺失")
	}
	return plain[idx+1:], nil
}

// openLegacyPayload 解析「旧格式」回参：base64 → `|` 分段 RSA 解密 → base64 → JSON。
func openLegacyPayload(data string) (any, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil, fmt.Errorf("旧格式回参为空")
	}
	outer, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("旧格式回参解码失败: %w", err)
	}
	var joined strings.Builder
	for _, part := range strings.Split(string(outer), "|") {
		if part == "" {
			continue
		}
		plain, err := legacyRSADecryptPart(part)
		if err != nil {
			return nil, err
		}
		joined.Write(plain)
	}
	inner, err := base64.StdEncoding.DecodeString(joined.String())
	if err != nil {
		return nil, fmt.Errorf("旧格式明文解码失败: %w", err)
	}
	var out any
	if err := json.Unmarshal(inner, &out); err != nil {
		return nil, fmt.Errorf("旧格式明文解析失败: %w", err)
	}
	return out, nil
}
