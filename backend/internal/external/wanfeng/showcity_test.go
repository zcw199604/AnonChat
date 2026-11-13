package wanfeng

import (
	"context"
	"testing"
	"time"
)

// TestUpdateProfilePayloadShowCity 校验「城市公开/不公开」能通过 update_profile 下发。
//
// 对方站点设置页的「城市：公开 / 不公开」对应资料字段 showCity（默认公开）；
// login 帧没有该字段，只能走 update_profile。
func TestUpdateProfilePayloadShowCity(t *testing.T) {
	p := DefaultProtocol()

	// 未配置（nil）时不下发，保持行为不变
	payload := p.UpdateProfilePayload(Profile{Nickname: "小明", Gender: "male", Age: 20})
	if _, ok := payload["showCity"]; ok {
		t.Fatalf("未配置时不应携带 showCity: %+v", payload)
	}

	// 不公开
	hide := false
	payload = p.UpdateProfilePayload(Profile{Nickname: "小明", Gender: "male", Age: 20, ShowCity: &hide})
	if v, ok := payload["showCity"]; !ok || v != false {
		t.Fatalf("应携带 showCity=false，实际 %+v", payload)
	}

	// 公开
	show := true
	payload = p.UpdateProfilePayload(Profile{Nickname: "小明", ShowCity: &show})
	if v, ok := payload["showCity"]; !ok || v != true {
		t.Fatalf("应携带 showCity=true，实际 %+v", payload)
	}
}

// TestClientPushesShowCityAfterLogin 校验登录成功后会自动补发 update_profile。
//
// 已存在的身份在对方站点上可能还是「公开」，只靠 login 帧改不掉，
// 所以配置了 showCity 就必须在 connected 之后补一帧。
func TestClientPushesShowCityAfterLogin(t *testing.T) {
	f := newFakeUpstream(t)
	client := newTestClient(t, f, nil, &recordingEmitter{})
	hide := false
	client.cfg.Profile.ShowCity = &hide

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	<-f.connectedSent

	proto := DefaultProtocol()
	frames := f.waitFrames(t, proto.ActionUpdateProfile, 1)
	payload := frames[0]
	if v, ok := payload["showCity"]; !ok || v != false {
		t.Fatalf("登录后应下发 showCity=false，实际 %+v", payload)
	}
	// 只发这一个字段（对方前端改开关时同样是增量 patch），避免覆盖对方侧其它资料
	if _, ok := payload["nickname"]; ok {
		t.Fatalf("增量 patch 不应携带昵称，实际 %+v", payload)
	}
}

// TestClientSkipsShowCityWhenUnset 校验未配置时不额外下发 update_profile（避免打扰对方风控）。
func TestClientSkipsShowCityWhenUnset(t *testing.T) {
	f := newFakeUpstream(t)
	client := newTestClient(t, f, nil, &recordingEmitter{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	<-f.connectedSent

	// 给读取循环一点时间，确认没有 update_profile 帧
	proto := DefaultProtocol()
	f.waitFrames(t, proto.ActionLogin, 1)
	time.Sleep(300 * time.Millisecond)
	if got := f.framesWithType(proto.ActionUpdateProfile); len(got) != 0 {
		t.Fatalf("未配置时不应下发 update_profile，实际 %+v", got)
	}
}
