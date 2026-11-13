//go:build integration

package media_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// 仅验证 P1 新增的查询契约，不提前实现上传或消息发送接口。
func TestUserMediaQueriesRespectOwnershipAndBurn(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	owner, _ := mkIdentity(t, pool, "owner")
	other, _ := mkIdentity(t, pool, "other")
	q := store.New(pool)
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO media_items(kind,mime,size_bytes,storage_key,source,owner_identity_id) VALUES('image','image/png',1,'user/test.png','user',$1) RETURNING id`, owner.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	assertAccess := func(identity pgtype.UUID, want bool) {
		t.Helper()
		got, err := q.CanIdentityAccessMedia(ctx, store.CanIdentityAccessMediaParams{MediaID: id, IdentityID: identity})
		if err != nil || got != want {
			t.Fatalf("access=%v want %v err=%v", got, want, err)
		}
	}
	assertAccess(owner.ID, true)
	assertAccess(other.ID, false)
	item, err := q.GetUserMediaForSend(ctx, store.GetUserMediaForSendParams{ID: id, OwnerIdentityID: owner.ID})
	if err != nil || item.Source != "user" || item.FolderID.Valid || item.OwnerIdentityID != owner.ID {
		t.Fatalf("user media: %+v %v", item, err)
	}
	if _, err := q.GetUserMediaForSend(ctx, store.GetUserMediaForSendParams{ID: id, OwnerIdentityID: other.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("他人的媒体应不可发送: %v", err)
	}
	var convID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO conversations(kind) VALUES('match') RETURNING id").Scan(&convID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO conversation_members(conversation_id,member_id,identity_id) VALUES($1,1,$2),($1,2,$3)`, convID, owner.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id) VALUES($1,$2,1,gen_random_uuid(),'image','caption',$3) RETURNING id`, convID, owner.ID, id).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	assertAccess(owner.ID, true)
	assertAccess(other.ID, true)
	relay, err := q.GetMessageForRelay(ctx, messageID)
	if err != nil || relay.ConversationID != convID || relay.SenderIdentityID != owner.ID || relay.MediaID != id || relay.Kind != "image" || relay.Body != "caption" {
		t.Fatalf("relay: %+v %v", relay, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE messages SET burned_at=now() WHERE id=$1", messageID); err != nil {
		t.Fatal(err)
	}
	assertAccess(owner.ID, false)
	assertAccess(other.ID, false)
}
