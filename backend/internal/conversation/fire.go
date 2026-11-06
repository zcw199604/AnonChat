package conversation

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/realtime"
)

// RunBurner 启动阅后即焚扫描器，直到 ctx 取消。由数据库驱动，进程重启后可继续焚毁。
func (s *Service) RunBurner(ctx context.Context) {
	ticker := time.NewTicker(s.burnTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.BurnOnce(ctx); err != nil {
				s.logger.Warn("焚毁扫描失败", "error", err)
			}
		}
	}
}

// BurnOnce 执行一次焚毁扫描：把 read_at 早于 now-burnDelay 的消息置 burned_at，并按会话推送 msg.burned。
func (s *Service) BurnOnce(ctx context.Context) error {
	cutoff := s.now().Add(-s.burnDelay)
	rows, err := s.q.BurnExpiredMessages(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	grouped := make(map[pgtype.UUID][]int64)
	for _, r := range rows {
		grouped[r.ConversationID] = append(grouped[r.ConversationID], r.ID)
	}
	if s.broker == nil {
		return nil
	}
	for convID, ids := range grouped {
		env, e := realtime.NewEnvelope(realtime.TypeMsgBurned, "", msgBurnedPayload{
			ConversationID: convID.String(),
			MessageIDs:     ids,
		})
		if e != nil {
			continue
		}
		s.broker.Broadcast(convID, env, nil)
	}
	return nil
}
