package conversation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// BlockPeer 屏蔽 1v1 会话的对方，并以原因 blocked 结束会话（双方收到 conv.ended）。
// 仅适用于 match / bottle / private；用在 room 上返回 invalid_request。
func (s *Service) BlockPeer(ctx context.Context, requesterID, conversationID pgtype.UUID) error {
	conv, err := s.q.GetConversation(ctx, conversationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeInvalidRequest, "会话不存在")
		}
		return err
	}
	if conv.Kind == store.ConversationKindRoom {
		return httpx.NewError(httpx.CodeInvalidRequest, "房间会话不支持一键屏蔽对方，请屏蔽具体成员")
	}
	if _, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: conversationID, IdentityID: requesterID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotMember, "你不在该会话中")
		}
		return err
	}

	others, err := s.q.ListOtherMemberIdentities(ctx, store.ListOtherMemberIdentitiesParams{
		ConversationID: conversationID,
		IdentityID:     requesterID,
	})
	if err != nil {
		return err
	}
	for _, target := range others {
		if err := s.q.InsertBlock(ctx, store.InsertBlockParams{
			BlockerIdentityID: requesterID,
			BlockedIdentityID: target,
		}); err != nil {
			return err
		}
	}
	// 屏蔽后结束会话（不改变已结束状态）。
	return s.End(ctx, conversationID, "blocked")
}

// BlockMember 屏蔽会话中的指定成员（房间使用），不结束会话。
func (s *Service) BlockMember(ctx context.Context, requesterID, conversationID pgtype.UUID, memberID int32) error {
	if _, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: conversationID, IdentityID: requesterID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotMember, "你不在该会话中")
		}
		return err
	}
	target, err := s.q.GetMemberByMemberID(ctx, store.GetMemberByMemberIDParams{
		ConversationID: conversationID,
		MemberID:       memberID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeInvalidRequest, "成员不存在")
		}
		return err
	}
	if target.IdentityID == requesterID {
		return httpx.NewError(httpx.CodeInvalidRequest, "不能屏蔽自己")
	}
	return s.q.InsertBlock(ctx, store.InsertBlockParams{
		BlockerIdentityID: requesterID,
		BlockedIdentityID: target.IdentityID,
	})
}
