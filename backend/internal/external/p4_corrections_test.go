package external

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/jackc/pgx/v5/pgtype"
)

type correctionHub struct {
	realtime.Realtime
	mu     sync.Mutex
	events []realtime.Envelope
}

func (h *correctionHub) SendToIdentity(_ pgtype.UUID, ev realtime.Envelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, ev)
}
func TestP4Code15RespectsLinkState(t *testing.T) {
	for _, state := range []linkState{linkMatching, linkIdle, linkActivating, linkInRoom} {
		t.Run(map[linkState]string{linkMatching: "matching", linkIdle: "idle", linkActivating: "activating", linkInRoom: "inroom"}[state], func(t *testing.T) {
			b, _ := testBridge(t)
			l := startBridge(t, b, bridgeRequest(1))
			s := l.sess.(*fakeSession)
			var resolves int
			b.resolve = func(context.Context, string, string, PeerProfile) (match.ExternalPeer, error) {
				resolves++
				return match.ExternalPeer{}, errors.New("stop before activation")
			}
			l.mu.Lock()
			l.state = state
			b.handleFrameLocked(l, matchedFrame())
			l.mu.Unlock()
			s.mu.Lock()
			leaves := s.leaves
			s.mu.Unlock()
			switch state {
			case linkMatching:
				if resolves != 1 {
					t.Fatal("matching未处理成功帧")
				}
			case linkIdle:
				if leaves != 1 || resolves != 0 {
					t.Fatalf("idle: leaves=%d resolves=%d", leaves, resolves)
				}
			default:
				if leaves != 0 || resolves != 0 {
					t.Fatalf("活动会话被退出: leaves=%d resolves=%d", leaves, resolves)
				}
			}
		})
	}
}
func TestP4CommandFailuresNotifyOnlyMedia(t *testing.T) {
	for _, media := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "media"}[media], func(t *testing.T) {
			b, _ := testBridge(t)
			h := &correctionHub{}
			b.deps.Hub = h
			l := startBridge(t, b, bridgeRequest(1))
			b.commandFailed(l, errors.New("write failed"), media)
			h.mu.Lock()
			defer h.mu.Unlock()
			if !media {
				if len(h.events) != 0 {
					t.Fatal("文本失败不应推送media_rejected")
				}
				return
			}
			if len(h.events) != 1 {
				t.Fatalf("媒体失败事件=%d", len(h.events))
			}
			var p map[string]string
			if err := json.Unmarshal(h.events[0].Data, &p); err != nil {
				t.Fatal(err)
			}
			if p["code"] != "media_rejected" || p["message"] != "对方站点暂不支持该媒体，发送失败" {
				t.Fatalf("媒体错误=%v", p)
			}
			b.mu.Lock()
			count := b.stats[l.provider.Name()].Counters.MediaFailed
			b.mu.Unlock()
			if count != 1 {
				t.Fatalf("mediaFailed=%d", count)
			}
		})
	}
}
