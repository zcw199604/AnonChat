package conversation

import (
	"context"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// conversationKindIsStory 是故事会话的类型常量（迁移 00004 新增）。
const conversationKindIsStory = store.ConversationKind("story")

// nicknameFor 计算消息中对外显示的昵称（how.md 3.6，已按产品决定调整）：
//   - 房间会话：真实昵称（群聊需要称呼）；
//   - 故事会话：显示角色名；
//   - 1v1 会话（match/bottle/private）：同样是真实昵称，前端不再使用「匿名·XXXX」代号。
func (s *Service) nicknameFor(ctx context.Context, conv store.Conversation, nickname string, memberID int32) string {
	switch conv.Kind {
	case store.ConversationKindRoom, store.ConversationKindMatch, store.ConversationKindBottle, store.ConversationKindPrivate:
		return nickname
	case conversationKindIsStory:
		story, err := s.q.GetStoryByConversation(ctx, conv.ID)
		if err != nil {
			return ""
		}
		hostRole, guestRole := story.RoleA, story.RoleB
		if story.HostRole == "b" {
			hostRole, guestRole = story.RoleB, story.RoleA
		}
		if memberID == 1 {
			return hostRole
		}
		return guestRole
	default:
		return nickname
	}
}
