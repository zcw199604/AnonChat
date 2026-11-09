package realtime

import (
	"context"
	"time"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/coder/websocket"
)

type connProbe struct {
	done chan struct{}
	err  error
}

// probeAlive 复用同时发起的探活；网络操作不持有 Hub 锁。
func (c *Conn) probeAlive(ctx context.Context) error {
	c.probeMu.Lock()
	if probe := c.probe; probe != nil {
		c.probeMu.Unlock()
		select {
		case <-probe.done:
			return probe.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	probe := &connProbe{done: make(chan struct{})}
	c.probe = probe
	c.probeMu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if c.ping != nil {
		probe.err = c.ping(probeCtx)
	} else {
		probe.err = c.ws.Ping(probeCtx)
	}
	cancel()
	c.probeMu.Lock()
	close(probe.done)
	c.probe = nil
	c.probeMu.Unlock()
	return probe.err
}

func (g *Gateway) admit(ctx context.Context, c *Conn) (bool, string) {
	perIP, perIdentity := 0, 0
	if settings := g.opts.LimitSettings; settings != nil {
		snapshot := settings.Current()
		ipRule := snapshot.Rules[moderation.RuleWSConnPerIP]
		identityRule := snapshot.Rules[moderation.RuleWSConnPerIPIdentity]
		if ipRule.Enabled {
			perIP = ipRule.Limit * settings.Multiplier()
		}
		if identityRule.Enabled {
			perIdentity = identityRule.Limit
		}
	}
	ok, reason, conflicts := g.hub.admit(c, perIP, perIdentity)
	if ok || reason != "duplicate_session" {
		return ok, reason
	}
	replace := false
	for _, old := range conflicts {
		select {
		case <-old.done:
			replace = true
		default:
			if old.probeAlive(ctx) == nil {
				continue
			}
			old.closeWith(websocket.StatusPolicyViolation, "stale connection")
			// 跳过关闭握手，立即打断旧连接的 Read，使其及时从 Hub 注销。
			if old.closeNow != nil {
				old.closeNow()
			} else if old.ws != nil {
				_ = old.ws.CloseNow()
			}
			replace = true
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-old.removed:
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	if replace {
		ok, reason, _ = g.hub.admit(c, perIP, perIdentity)
	}
	return ok, reason
}

func rejectConnection(ws *websocket.Conn, reason string) {
	env, _ := NewEnvelope(TypeKicked, "", KickedPayload{Reason: reason})
	data, _ := env.Marshal()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = ws.Write(ctx, websocket.MessageText, data)
	cancel()
	_ = ws.Close(websocket.StatusPolicyViolation, reason)
}
