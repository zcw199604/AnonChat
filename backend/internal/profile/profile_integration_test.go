//go:build integration

package profile_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

type meResponse struct {
	Nickname   string `json:"nickname"`
	PublicCode string `json:"publicCode"`
	Profile    struct {
		Gender         string   `json:"gender"`
		Age            *int     `json:"age"`
		Province       *string  `json:"province"`
		ProvincePublic bool     `json:"provincePublic"`
		Tags           []string `json:"tags"`
		CleanMode      bool     `json:"cleanMode"`
	} `json:"profile"`
	MatchPrefs struct {
		Gender       string `json:"gender"`
		SameProvince bool   `json:"sameProvince"`
	} `json:"matchPrefs"`
	Vip struct {
		Active bool    `json:"active"`
		Until  *string `json:"until"`
	} `json:"vip"`
	BroadcastCards int32 `json:"broadcastCards"`
}

func newIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "profile-tok-" + name
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: name, AvatarSeed: "seed-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

func TestUpdateProfileValidation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, token := newIdentity(t, pool, "prof")

	// 合法更新。
	resp := testutil.Do(t, env.Server, http.MethodPatch, "/api/identity/profile", map[string]any{
		"gender": "female", "age": 24, "province": "浙江", "provincePublic": true,
		"tags": []string{"音乐", "夜猫子"}, "cleanMode": true,
	}, token, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("更新资料状态 = %d, body=%s", resp.Status, resp.Body)
	}
	var me meResponse
	resp.JSON(t, &me)
	if me.Profile.Gender != "female" || me.Profile.Age == nil || *me.Profile.Age != 24 {
		t.Fatalf("资料未保存: %+v", me.Profile)
	}
	if me.Profile.Province == nil || *me.Profile.Province != "浙江" || !me.Profile.ProvincePublic {
		t.Fatalf("省份未保存: %+v", me.Profile)
	}
	if len(me.Profile.Tags) != 2 || !me.Profile.CleanMode {
		t.Fatalf("标签或清流未保存: %+v", me.Profile)
	}
	if me.PublicCode == "" {
		t.Fatal("me 应返回匿名编号")
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{"非法省份", map[string]any{"province": "火星"}},
		{"年龄过小", map[string]any{"age": 17}},
		{"年龄过大", map[string]any{"age": 81}},
		{"标签过多", map[string]any{"tags": []string{"音乐", "电影", "游戏", "动漫", "读书", "运动"}}},
		{"非法标签", map[string]any{"tags": []string{"不存在"}}},
		{"未知字段", map[string]any{"nope": 1}},
	}
	for _, c := range cases {
		r := testutil.Do(t, env.Server, http.MethodPatch, "/api/identity/profile", c.body, token, "")
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: 状态 = %d, 期望 400, body=%s", c.name, r.Status, r.Body)
		}
	}
}

func TestMatchPrefsRequireVIP(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	id, token := newIdentity(t, pool, "prefs")

	// 非 VIP：返回 vip_required。
	r := testutil.Do(t, env.Server, http.MethodPatch, "/api/identity/profile", map[string]any{
		"matchPrefs": map[string]any{"gender": "female", "sameProvince": true},
	}, token, "")
	if r.Status != http.StatusForbidden {
		t.Fatalf("非 VIP 设置匹配偏好状态 = %d, 期望 403, body=%s", r.Status, r.Body)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.JSON(t, &e)
	if e.Error.Code != "vip_required" {
		t.Errorf("错误码 = %q, 期望 vip_required", e.Error.Code)
	}

	// 变成 VIP 后可设置。
	if _, err := pool.Exec(ctx, `UPDATE identities SET vip_until = now() + interval '30 days' WHERE id = $1`, id.ID); err != nil {
		t.Fatalf("设置 VIP 失败: %v", err)
	}
	r = testutil.Do(t, env.Server, http.MethodPatch, "/api/identity/profile", map[string]any{
		"matchPrefs": map[string]any{"gender": "female", "sameProvince": true},
	}, token, "")
	if r.Status != http.StatusOK {
		t.Fatalf("VIP 设置匹配偏好状态 = %d, body=%s", r.Status, r.Body)
	}
	var me meResponse
	r.JSON(t, &me)
	if me.MatchPrefs.Gender != "female" || !me.MatchPrefs.SameProvince {
		t.Fatalf("匹配偏好未保存: %+v", me.MatchPrefs)
	}
	if !me.Vip.Active {
		t.Error("me.vip.active 应为 true")
	}
}

func TestNicknameShuffleAndSet(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, token := newIdentity(t, pool, "nick")

	before := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, token, "")
	var meBefore meResponse
	before.JSON(t, &meBefore)

	shuffled := testutil.Do(t, env.Server, http.MethodPost, "/api/identity/nickname/shuffle", nil, token, "")
	if shuffled.Status != http.StatusOK {
		t.Fatalf("随机昵称状态 = %d, body=%s", shuffled.Status, shuffled.Body)
	}
	var meAfter meResponse
	shuffled.JSON(t, &meAfter)
	if meAfter.Nickname == "" || meAfter.Nickname == meBefore.Nickname {
		t.Fatalf("随机昵称未变化: %q → %q", meBefore.Nickname, meAfter.Nickname)
	}

	set := testutil.Do(t, env.Server, http.MethodPut, "/api/identity/nickname", map[string]string{"nickname": "  夜猫子  "}, token, "")
	if set.Status != http.StatusOK {
		t.Fatalf("自定义昵称状态 = %d, body=%s", set.Status, set.Body)
	}
	var meSet meResponse
	set.JSON(t, &meSet)
	if meSet.Nickname != "夜猫子" {
		t.Errorf("昵称 = %q, 期望「夜猫子」（首尾空白应被去掉）", meSet.Nickname)
	}

	bad := testutil.Do(t, env.Server, http.MethodPut, "/api/identity/nickname", map[string]string{"nickname": "a"}, token, "")
	if bad.Status != http.StatusBadRequest {
		t.Errorf("过短昵称状态 = %d, 期望 400", bad.Status)
	}
}

func TestResetInheritsVipAndCards(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	id, token := newIdentity(t, pool, "reset")
	if _, err := pool.Exec(ctx, `UPDATE identities SET vip_until = now() + interval '20 days', broadcast_cards = 4, gender = 'male', province = '浙江' WHERE id = $1`, id.ID); err != nil {
		t.Fatalf("准备权益失败: %v", err)
	}
	before := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, token, "")
	var meBefore meResponse
	before.JSON(t, &meBefore)

	reset := testutil.Do(t, env.Server, http.MethodPost, "/api/identity/reset", nil, token, "")
	if reset.Status != http.StatusOK || reset.Token == "" || reset.Token == token {
		t.Fatalf("换身份状态 = %d, 新令牌 = %q", reset.Status, reset.Token)
	}

	after := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, reset.Token, "")
	var meAfter meResponse
	after.JSON(t, &meAfter)

	if !meAfter.Vip.Active {
		t.Error("换身份后 VIP 应继承")
	}
	if meAfter.BroadcastCards != 4 {
		t.Errorf("广播卡 = %d, 期望 4（继承）", meAfter.BroadcastCards)
	}
	if meAfter.Profile.Gender != "secret" || meAfter.Profile.Province != nil {
		t.Errorf("换身份后资料应重置为默认值: %+v", meAfter.Profile)
	}
	if meAfter.PublicCode == meBefore.PublicCode || meAfter.PublicCode == "" {
		t.Errorf("匿名编号应重新生成: %q → %q", meBefore.PublicCode, meAfter.PublicCode)
	}
}

// TestNicknameMasked 验证自定义昵称保存的是打码后的文本（审查意见 R1）。
func TestNicknameMasked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		Filter: moderation.NewModerator([]moderation.Entry{{Word: "示例替换词", Level: moderation.LevelMask}}),
	})
	id, token := newIdentity(t, pool, "maskself")

	r := testutil.Do(t, env.Server, http.MethodPut, "/api/identity/nickname", map[string]string{"nickname": "示例替换词"}, token, "")
	if r.Status != http.StatusOK {
		t.Fatalf("自定义昵称状态 = %d, body=%s", r.Status, r.Body)
	}
	var me meResponse
	r.JSON(t, &me)
	if want := "*****"; me.Nickname != want {
		t.Errorf("返回昵称 = %q, 期望 %q（打码后的文本）", me.Nickname, want)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT nickname FROM identities WHERE id = $1`, id.ID).Scan(&stored); err != nil {
		t.Fatalf("查询昵称失败: %v", err)
	}
	if want := "*****"; stored != want {
		t.Errorf("库中昵称 = %q, 期望 %q", stored, want)
	}
}
