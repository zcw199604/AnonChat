package realtime

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// mkOnlineTestConn 构造一条带 IP/UA/建立时间的测试连接（同一身份可有多条）。
func mkOnlineTestConn(id pgtype.UUID, nickname, publicCode, ip, ua string, at time.Time) *Conn {
	c := mkTestConn(id, 1)
	c.identity = &store.Identity{ID: id, Nickname: nickname, PublicCode: publicCode}
	c.ip = ip
	c.userAgent = ua
	c.connectedAt = at
	return c
}

// TestOnlineSnapshotAggregates 验证同一身份的多条连接聚合为一条：
// IP 去重且排序、ConnCount 正确、ConnectedAt 取最早、UserAgent 取最新连接。
func TestOnlineSnapshotAggregates(t *testing.T) {
	h := NewHub()
	id := uuidN(40)
	early := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	late := early.Add(30 * time.Minute)

	c1 := mkOnlineTestConn(id, "甲", "ABC123", "9.9.9.9", "UA-early", early)
	c2 := mkOnlineTestConn(id, "甲", "ABC123", "1.1.1.1", "UA-late", late)
	c3 := mkOnlineTestConn(id, "甲", "ABC123", "9.9.9.9", "UA-late2", late)
	h.addConn(c1)
	h.addConn(c2)
	h.addConn(c3)

	snap := h.OnlineSnapshot()
	if len(snap) != 1 {
		t.Fatalf("在线身份数 = %d, 期望 1", len(snap))
	}
	got := snap[0]
	if got.IdentityID != id {
		t.Errorf("IdentityID = %v, 期望 %v", got.IdentityID, id)
	}
	if got.Nickname != "甲" || got.PublicCode != "ABC123" {
		t.Errorf("身份字段错误: nickname=%q publicCode=%q", got.Nickname, got.PublicCode)
	}
	if got.ConnCount != 3 {
		t.Errorf("ConnCount = %d, 期望 3", got.ConnCount)
	}
	wantIPs := []string{"1.1.1.1", "9.9.9.9"}
	if len(got.IPs) != len(wantIPs) || got.IPs[0] != wantIPs[0] || got.IPs[1] != wantIPs[1] {
		t.Errorf("IPs = %v, 期望 %v（去重且排序）", got.IPs, wantIPs)
	}
	if !got.ConnectedAt.Equal(early) {
		t.Errorf("ConnectedAt = %v, 期望最早连接时间 %v", got.ConnectedAt, early)
	}
	if got.UserAgent != "UA-late" && got.UserAgent != "UA-late2" {
		t.Errorf("UserAgent = %q, 期望最近建立连接的 UA", got.UserAgent)
	}

	// 断开其中一条后只剩两条，IP 仍应包含两条不同地址。
	h.removeConn(c3)
	if snap := h.OnlineSnapshot(); len(snap) != 1 || snap[0].ConnCount != 2 {
		t.Fatalf("断开一条后快照 = %+v, 期望 1 条、ConnCount=2", snap)
	}

	// 全部断开后该身份应从快照中消失。
	h.removeConn(c1)
	h.removeConn(c2)
	if snap := h.OnlineSnapshot(); len(snap) != 0 {
		t.Fatalf("全部断开后在线身份数 = %d, 期望 0", len(snap))
	}
}

// TestOnlineSnapshotSortedByConnectedAtDesc 验证结果按上线时间降序排列。
func TestOnlineSnapshotSortedByConnectedAtDesc(t *testing.T) {
	h := NewHub()
	base := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	old := mkOnlineTestConn(uuidN(50), "旧", "OLD", "1.0.0.1", "ua", base)
	mid := mkOnlineTestConn(uuidN(51), "中", "MID", "1.0.0.2", "ua", base.Add(time.Minute))
	recent := mkOnlineTestConn(uuidN(52), "新", "NEW", "1.0.0.3", "ua", base.Add(2*time.Minute))
	h.addConn(old)
	h.addConn(mid)
	h.addConn(recent)

	snap := h.OnlineSnapshot()
	if len(snap) != 3 {
		t.Fatalf("在线身份数 = %d, 期望 3", len(snap))
	}
	if snap[0].Nickname != "新" || snap[1].Nickname != "中" || snap[2].Nickname != "旧" {
		t.Fatalf("排序错误: %v %v %v", snap[0].Nickname, snap[1].Nickname, snap[2].Nickname)
	}
}

// TestOnlineSnapshotReturnsCopies 验证快照只含拷贝值：修改返回值不影响 Hub。
func TestOnlineSnapshotReturnsCopies(t *testing.T) {
	h := NewHub()
	c := mkOnlineTestConn(uuidN(60), "甲", "COPY", "2.2.2.2", "ua", time.Now())
	h.addConn(c)

	snap := h.OnlineSnapshot()
	if len(snap) != 1 || len(snap[0].IPs) != 1 {
		t.Fatalf("快照 = %+v, 期望 1 条且 1 个 IP", snap)
	}
	snap[0].IPs[0] = "篡改"
	if got := h.OnlineSnapshot()[0].IPs[0]; got != "2.2.2.2" {
		t.Errorf("快照返回了内部切片引用: %q", got)
	}
}
