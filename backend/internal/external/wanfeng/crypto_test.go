package wanfeng

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// TestHKDFSHA256RFC5869 用 RFC 5869 Test Case 1 校验手写 HKDF 实现。
func TestHKDFSHA256RFC5869(t *testing.T) {
	ikm := bytesRepeat(0x0b, 22)
	salt := mustHex(t, "000102030405060708090a0b0c")
	info := mustHex(t, "f0f1f2f3f4f5f6f7f8f9")
	want := "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865"

	got := hkdfSHA256(ikm, salt, info, 42)
	if hex.EncodeToString(got) != want {
		t.Fatalf("HKDF 结果不匹配\nwant=%s\n got=%s", want, hex.EncodeToString(got))
	}
}

// TestSealOpenEnvelope 校验信封加解密往返。
func TestSealOpenEnvelope(t *testing.T) {
	key := bytesRepeat(0x2a, 32)
	sid := "sid-123"

	env, err := sealEnvelope(key, sid, []byte(`{"a":1,"b":"中文"}`), "v3.")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if !strings.HasPrefix(env, "v3.") {
		t.Fatalf("信封前缀错误: %s", env)
	}

	plain, err := openEnvelope(key, sid, env, "v3.")
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if string(plain) != `{"a":1,"b":"中文"}` {
		t.Fatalf("解密结果不符: %s", string(plain))
	}
}

// TestOpenEnvelopeWrongAAD 校验 AAD（sid）不一致时解密失败。
func TestOpenEnvelopeWrongAAD(t *testing.T) {
	key := bytesRepeat(0x11, 32)
	env, err := sealEnvelope(key, "sid-a", []byte(`{"x":1}`), "v3.")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if _, err := openEnvelope(key, "sid-b", env, "v3."); err == nil {
		t.Fatal("sid 不一致时应解密失败")
	}
}

// TestOpenEnvelopeBadFormat 校验非法信封格式。
func TestOpenEnvelopeBadFormat(t *testing.T) {
	key := bytesRepeat(0x11, 32)
	cases := []string{"", "v2.abc.def", "v3.only-one-part", "plain-text"}
	for _, c := range cases {
		if _, err := openEnvelope(key, "sid", c, "v3."); err == nil {
			t.Fatalf("非法信封应报错: %q", c)
		}
	}
}

// TestHandshakeKeyDerivation 校验 ECDH + HKDF 派生的对称性（模拟服务端用同一曲线）。
func TestHandshakeKeyDerivation(t *testing.T) {
	client, err := newHandshakeKeypair()
	if err != nil {
		t.Fatalf("生成客户端密钥失败: %v", err)
	}

	// 用另一对密钥模拟服务端：双方使用相同 sid 派生出的密钥必须一致
	server, err := newHandshakeKeypair()
	if err != nil {
		t.Fatalf("生成服务端密钥失败: %v", err)
	}

	clientKey, err := client.deriveSessionKey(server.pubB64, "sid-xyz", "wf-enc-v3")
	if err != nil {
		t.Fatalf("客户端派生失败: %v", err)
	}
	serverKey, err := server.deriveSessionKey(client.pubB64, "sid-xyz", "wf-enc-v3")
	if err != nil {
		t.Fatalf("服务端派生失败: %v", err)
	}

	if base64.StdEncoding.EncodeToString(clientKey) != base64.StdEncoding.EncodeToString(serverKey) {
		t.Fatal("双方派生密钥不一致")
	}
	if len(clientKey) != envelopeKeyLen {
		t.Fatalf("密钥长度应为 %d，实际 %d", envelopeKeyLen, len(clientKey))
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex 解码失败: %v", err)
	}
	return b
}
