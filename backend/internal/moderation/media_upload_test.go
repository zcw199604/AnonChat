package moderation

import (
	"context"
	"testing"
)

func TestMediaUploadRuleDefaultsAndIdentityIsolation(t *testing.T) {
	key := RuleKey("media_upload_per_minute")
	m, ok := Meta(key)
	if !ok || m.KeyPrefix != "media_upload:" || m.DefaultLimit != 10 || m.DefaultWindowSeconds != 60 {
		t.Fatalf("上传限流规则未按契约注册: %+v", m)
	}
	limiter := NewLimitSettings(nil, 1, nil).Limiter(key)
	for i := 0; i < 10; i++ {
		if !limiter.Allow(context.Background(), "media_upload:a") {
			t.Fatalf("第%d次请求被拒", i+1)
		}
	}
	if limiter.Allow(context.Background(), "media_upload:a") {
		t.Fatal("第11次请求未限流")
	}
	if !limiter.Allow(context.Background(), "media_upload:b") {
		t.Fatal("不同身份配额未隔离")
	}
}
