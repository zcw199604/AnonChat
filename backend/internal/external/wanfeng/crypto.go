package wanfeng

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// 加密信封说明（对方 2026-11 版本）：
//
//	握手：客户端生成 ECDH P-256 密钥对，POST /api/app/handshake {v:3, pub:<未压缩公钥 b64>}
//	    服务端返回 {data:{sid, pub, exp}}；双方 ECDH 出 shared secret。
//	派生：key = HKDF-SHA256(ikm=shared, salt=sid, info="wf-enc-v3", len=32)
//	请求体：{v:3, sid, iv:<b64 12B>, ct:<b64 AES-256-GCM(明文 JSON, AAD=sid)>}
//	响应体：data 若以 "v3." 开头，则为 "v3.<b64 iv>.<b64 ct+tag>"，同 key / 同 AAD 解密。
//	WS 帧：data 同样使用上述 "v3.<iv>.<ct>" 形式。
//
// 以上参数（前缀、版本号、HKDF info）全部来自 Protocol，对方改版时可通过配置覆盖。

// 会话密钥长度（AES-256）。
const envelopeKeyLen = 32

// 12 字节 GCM 标准 nonce 长度。
const envelopeNonceLen = 12

// ErrBadEnvelope 表示密文信封格式不合法（可能是对方改了版本）。
var ErrBadEnvelope = errors.New("密文信封格式不合法")

// handshakeKeypair 生成一次握手的客户端密钥对。
type handshakeKeypair struct {
	priv   *ecdh.PrivateKey
	pubB64 string
}

// newHandshakeKeypair 生成 P-256 客户端密钥对，公钥按未压缩点（65 字节）base64 编码。
func newHandshakeKeypair() (*handshakeKeypair, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 ECDH 密钥失败: %w", err)
	}
	return &handshakeKeypair{
		priv:   priv,
		pubB64: base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()),
	}, nil
}

// deriveSessionKey 按对方约定派生 AES 会话密钥。
func (k *handshakeKeypair) deriveSessionKey(serverPubB64, sid, hkdfInfo string) ([]byte, error) {
	serverPubRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(serverPubB64))
	if err != nil {
		return nil, fmt.Errorf("解析服务端公钥失败: %w", err)
	}
	serverPub, err := ecdh.P256().NewPublicKey(serverPubRaw)
	if err != nil {
		return nil, fmt.Errorf("服务端公钥非法: %w", err)
	}
	shared, err := k.priv.ECDH(serverPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH 计算出错: %w", err)
	}
	return hkdfSHA256(shared, []byte(sid), []byte(hkdfInfo), envelopeKeyLen), nil
}

// hkdfSHA256 实现 RFC 5869 HKDF-SHA256（Extract + Expand）。
// 这里手写是为了避免依赖 golang.org/x/crypto 或不同 Go 版本 crypto/hkdf 的签名差异。
func hkdfSHA256(ikm, salt, info []byte, length int) []byte {
	if length <= 0 {
		return nil
	}
	// Extract
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	prk := mac.Sum(nil)

	// Expand
	out := make([]byte, 0, length)
	var prev []byte
	for i := byte(1); len(out) < length; i++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		if len(info) > 0 {
			mac.Write(info)
		}
		mac.Write([]byte{i})
		prev = mac.Sum(nil)
		out = append(out, prev...)
		if i == 255 {
			break
		}
	}
	return out[:length]
}

// sealEnvelope 使用会话密钥加密明文，返回 "v3.<b64 iv>.<b64 密文+tag>"。
func sealEnvelope(key []byte, sid string, plaintext []byte, prefix string) (string, error) {
	ivB64, ctB64, err := sealParts(key, sid, plaintext)
	if err != nil {
		return "", err
	}
	return prefix + ivB64 + "." + ctB64, nil
}

// openEnvelope 解密 "v3.<b64 iv>.<b64 密文+tag>"。
func openEnvelope(key []byte, sid, envelope, prefix string) ([]byte, error) {
	envelope = strings.TrimSpace(envelope)
	if !strings.HasPrefix(envelope, prefix) {
		return nil, ErrBadEnvelope
	}
	parts := strings.Split(strings.TrimPrefix(envelope, prefix), ".")
	if len(parts) != 2 {
		return nil, ErrBadEnvelope
	}
	return openParts(key, sid, parts[0], parts[1])
}

// sealParts 加密并分别返回 base64(iv) 与 base64(密文+tag)。
func sealParts(key []byte, sid string, plaintext []byte) (string, string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if gcm.NonceSize() != envelopeNonceLen {
		iv = make([]byte, envelopeNonceLen)
	}
	if _, err := rand.Read(iv); err != nil {
		return "", "", fmt.Errorf("生成随机 IV 失败: %w", err)
	}
	ct := gcm.Seal(nil, iv, plaintext, []byte(sid))
	return base64.StdEncoding.EncodeToString(iv), base64.StdEncoding.EncodeToString(ct), nil
}

// openParts 解密 base64(iv) + base64(密文+tag)。
func openParts(key []byte, sid, ivB64, ctB64 string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ivB64))
	if err != nil {
		return nil, fmt.Errorf("解析 IV 失败: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ctB64))
	if err != nil {
		return nil, fmt.Errorf("解析密文失败: %w", err)
	}
	plain, err := gcm.Open(nil, iv, ct, []byte(sid))
	if err != nil {
		return nil, fmt.Errorf("AES-GCM 解密失败: %w", err)
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) == 0 {
		return nil, errors.New("会话密钥为空")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	return cipher.NewGCM(block)
}
