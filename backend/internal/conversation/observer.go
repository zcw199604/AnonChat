package conversation

import (
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// Observer 观察会话事件（bot 引擎实现该接口）。
// 实现方必须立即返回：需要耗时处理时自行开 goroutine。
type Observer interface {
	// OnMessage 在一条消息真正落库并广播之后回调；幂等命中不会回调。
	OnMessage(ev MessageEvent)
	// OnConversationEnded 在会话真正结束（affected > 0）之后回调。
	OnConversationEnded(conversationID pgtype.UUID)
}

// MessageEvent 是一条新消息的观察事件。
type MessageEvent struct {
	ConversationID   pgtype.UUID
	ConversationKind store.ConversationKind
	MessageID        int64
	SenderIdentityID pgtype.UUID
	SenderMemberID   int32
	Kind             string
}

// AddObserver 注册会话事件观察者（应在开始处理请求前调用）。
func (s *Service) AddObserver(o Observer) {
	if o == nil {
		return
	}
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	s.observers = append(s.observers, o)
}

// notifyMessage 通知全部观察者有一条消息已落库并广播。
func (s *Service) notifyMessage(ev MessageEvent) {
	s.obsMu.RLock()
	observers := s.observers
	s.obsMu.RUnlock()
	for _, o := range observers {
		o.OnMessage(ev)
	}
}

// notifyEnded 通知全部观察者会话已结束。
func (s *Service) notifyEnded(conversationID pgtype.UUID) {
	s.obsMu.RLock()
	observers := s.observers
	s.obsMu.RUnlock()
	for _, o := range observers {
		o.OnConversationEnded(conversationID)
	}
}
