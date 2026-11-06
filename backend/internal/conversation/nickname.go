package conversation

import (
	"context"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// conversationKindIsStory 是故事会话的类型常量（迁移 00004 新增）。
const conversationKindIsStory = store.ConversationKind("story")

// nicknameFor 计算消息中对外显示的昵称（how.md 3.6）：
//   - 房间会话：保持真实昵称（群聊需要称呼）；
//   - 故事会话：显示角色名；
//   - 1v1 会话（match/bottle/private）：空字符串，前端改用匿名代号。
func (s *Service) nicknameFor(ctx context.Context, conv store.Conversation, nickname string, memberID int32) string {
	switch conv.Kind {
	case store.ConversationKindRoom:
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
		return ""
	}
}
