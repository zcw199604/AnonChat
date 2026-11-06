//go:build integration

package match_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// setProfile 直接设置身份的匹配资料（绕过 VIP 校验，便于构造场景）。
func setProfile(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID, gender string, province *string, cleanMode bool, matchGender string, sameProv bool, vipDays int) {
	t.Helper()
	q := `UPDATE identities
	      SET gender = $2, province = $3, province_public = true, clean_mode = $4,
	          match_gender = $5, match_same_province = $6,
	          vip_until = CASE WHEN $7 > 0 THEN now() + make_interval(days => $7) ELSE NULL END
	      WHERE id = $1`
	if _, err := pool.Exec(context.Background(), q, id, gender, province, cleanMode, matchGender, sameProv, vipDays); err != nil {
		t.Fatalf("设置资料失败: %v", err)
	}
}

func strptr(s string) *string { return &s }

// TestMatchPrefsVIPGenderFilter 验证两个 VIP 的性别筛选是双向生效的（how.md 3.4）。
func TestMatchPrefsVIPGenderFilter(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})

	male, tokMale := mkIdentity(t, pool, "male")
	female, tokFemale := mkIdentity(t, pool, "female")
	// 男生只匹配男生，女生只匹配女生 → 两者不兼容。
	setProfile(t, pool, male.ID, "male", nil, false, "male", false, 30)
	setProfile(t, pool, female.ID, "female", nil, false, "female", false, 30)

	wsMale := connectHello(t, env.Server, tokMale)
	wsFemale := connectHello(t, env.Server, tokFemale)
	matchStart(t, wsMale)
	matchStart(t, wsFemale)

	// 等一小段时间，确认没有 match.found。
	expectNoFound(t, wsMale, 700*time.Millisecond)
	expectNoFound(t, wsFemale, 700*time.Millisecond)
}

// TestMatchPrefsVIPGenderMatches 验证性别相容的两个 VIP 能配对，且 match.found 带公开资料。
func TestMatchPrefsVIPGenderMatches(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})

	a, tokA := mkIdentity(t, pool, "va")
	b, tokB := mkIdentity(t, pool, "vb")
	setProfile(t, pool, a.ID, "female", strptr("浙江"), false, "female", true, 30)
	setProfile(t, pool, b.ID, "female", strptr("浙江"), false, "any", false, 30)

	wsA := connectHello(t, env.Server, tokA)
	wsB := connectHello(t, env.Server, tokB)
	matchStart(t, wsA)
	matchStart(t, wsB)

	conv := mustFound(t, wsA)
	if other := mustFound(t, wsB); other != conv {
		t.Fatalf("双方会话不一致: %s vs %s", conv, other)
	}
}

// TestCleanModeIsolation 验证清流与非清流用户不会互相配对。
func TestCleanModeIsolation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})

	clean, tokClean := mkIdentity(t, pool, "clean")
	dirty, tokDirty := mkIdentity(t, pool, "dirty")
	setProfile(t, pool, clean.ID, "secret", nil, true, "any", false, 0)
	setProfile(t, pool, dirty.ID, "secret", nil, false, "any", false, 0)

	wsClean := connectHello(t, env.Server, tokClean)
	wsDirty := connectHello(t, env.Server, tokDirty)
	matchStart(t, wsClean)
	matchStart(t, wsDirty)

	expectNoFound(t, wsClean, 700*time.Millisecond)
	expectNoFound(t, wsDirty, 700*time.Millisecond)
}

// TestNonVIPPrefsIgnored 验证非 VIP 提交的性别偏好被服务端忽略。
func TestNonVIPPrefsIgnored(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})

	a, tokA := mkIdentity(t, pool, "nonvip-a")
	b, tokB := mkIdentity(t, pool, "nonvip-b")
	// 非 VIP 但库里写了 match_gender=male（模拟绕过前端的请求）。
	setProfile(t, pool, a.ID, "female", nil, false, "male", false, 0)
	setProfile(t, pool, b.ID, "female", nil, false, "any", false, 0)

	wsA := connectHello(t, env.Server, tokA)
	wsB := connectHello(t, env.Server, tokB)
	matchStart(t, wsA)
	matchStart(t, wsB)

	conv := mustFound(t, wsA)
	if other := mustFound(t, wsB); other != conv {
		t.Fatalf("非 VIP 的性别偏好应被忽略，双方应配对: %s vs %s", conv, other)
	}
}

// expectNoFound 断言在给定时间内没有收到 match.found（跳过 waiting 等其他事件）。
func expectNoFound(t *testing.T, c *websocket.Conn, wait time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return // 超时即符合预期
		}
		if containsType(data, string(realtime.TypeMatchFound)) {
			t.Fatalf("不应配对，却收到 match.found: %s", data)
		}
	}
}

func containsType(data []byte, typ string) bool {
	return len(data) > 0 && (indexOf(data, []byte(`"t":"`+typ+`"`)) >= 0)
}

func indexOf(hay, needle []byte) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
