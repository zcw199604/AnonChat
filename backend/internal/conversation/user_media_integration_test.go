//go:build integration

package conversation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/media"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type userMediaClient struct {
	ident     *store.Identity
	acks      []conversation.AckPayload
	code, msg string
}

func (c *userMediaClient) Identity() *store.Identity               { return c.ident }
func (c *userMediaClient) Ack(_ string, p conversation.AckPayload) { c.acks = append(c.acks, p) }
func (c *userMediaClient) Error(_ string, code, msg string)        { c.code, c.msg = code, msg }
func userMediaEnv(t *testing.T, conv pgtype.UUID, kind, id, body string) realtime.Envelope {
	t.Helper()
	data, err := json.Marshal(map[string]any{"conversationId": conv.String(), "clientMsgId": newUUID(t).String(), "kind": kind, "mediaId": id, "body": body})
	if err != nil {
		t.Fatal(err)
	}
	return realtime.Envelope{Type: realtime.TypeMsgSend, ID: "user-media", Data: data}
}
func insertUserMedia(t *testing.T, pool *pgxpool.Pool, owner pgtype.UUID, kind, source string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(ctx, `INSERT INTO media_items(kind,mime,size_bytes,storage_key,source,owner_identity_id) VALUES($1,$2,8,$3,$4,$5) RETURNING id`, kind, map[string]string{"image": "image/png", "video": "video/mp4"}[kind], newUUID(t).String(), source, owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func newUserMediaService(pool *pgxpool.Pool, broker realtime.Broker, opts ...conversation.Option) *conversation.Service {
	options := append([]conversation.Option{conversation.WithMedia(media.New(pool, testLogger(), nil, 10))}, opts...)
	return conversation.New(pool, testLogger(), broker, options...)
}

func TestUserMediaSendInAllOneToOneKinds(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	a, _ := createIdentity(t, pool, "sender")
	b, _ := createIdentity(t, pool, "recipient")
	for _, convKind := range []store.ConversationKind{store.ConversationKindMatch, store.ConversationKindBottle, store.ConversationKindPrivate} {
		for _, kind := range []string{"image", "video"} {
			t.Run(string(convKind)+"/"+kind, func(t *testing.T) {
				broker := &fakeBroker{}
				svc := newUserMediaService(pool, broker)
				conv := createConv(t, pool, svc, convKind, false, a.ID, b.ID)
				id := insertUserMedia(t, pool, a.ID, kind, "user")
				client := &userMediaClient{ident: &a}
				svc.HandleSend(ctx, client, userMediaEnv(t, conv.ID, kind, id.String(), ""))
				if client.code != "" || len(client.acks) != 1 {
					t.Fatalf("发送失败: %s/%s ack=%d", client.code, client.msg, len(client.acks))
				}
				if len(broker.broadcasts) != 1 {
					t.Fatalf("广播数=%d", len(broker.broadcasts))
				}
				var payload struct {
					Message struct {
						Kind, Body, MediaURL string
						ID                   int64
					}
				}
				if err := json.Unmarshal(broker.broadcasts[0].env.Data, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Message.Kind != kind || payload.Message.Body != "" || payload.Message.MediaURL != "/api/media/"+id.String() {
					t.Fatalf("广播载荷=%s", broker.broadcasts[0].env.Data)
				}
				row, err := store.New(pool).GetMessageForRelay(ctx, payload.Message.ID)
				if err != nil {
					t.Fatal(err)
				}
				if row.MediaID != id || row.SenderIdentityID != a.ID {
					t.Fatalf("落库=%+v", row)
				}
			})
		}
	}
}

func TestUserMediaValidationAndOneToOneRestriction(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	broker := &fakeBroker{}
	svc := newUserMediaService(pool, broker)
	a, _ := createIdentity(t, pool, "sender")
	b, _ := createIdentity(t, pool, "other")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	own := insertUserMedia(t, pool, a.ID, "image", "user")
	other := insertUserMedia(t, pool, b.ID, "image", "user")
	external := insertUserMedia(t, pool, a.ID, "image", "external")
	for _, tt := range []struct{ name, kind, id, body, want string }{
		{"invalid_uuid", "image", "bad", "", "媒体不存在或不属于你"}, {"missing_uuid", "image", "", "", "媒体不存在或不属于你"},
		{"unknown_uuid", "image", newUUID(t).String(), "", "媒体不存在或不属于你"}, {"wrong_owner", "image", other.String(), "", "媒体不存在或不属于你"},
		{"wrong_source", "image", external.String(), "", "媒体不存在或不属于你"}, {"wrong_kind", "video", own.String(), "", "媒体不存在或不属于你"},
		{"caption_too_long", "image", own.String(), strings.Repeat("字", 1001), "消息内容超过上限"}, {"unsupported", "audio", own.String(), "", "不支持的消息类型"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &userMediaClient{ident: &a}
			svc.HandleSend(ctx, client, userMediaEnv(t, conv.ID, tt.kind, tt.id, tt.body))
			if client.code != "invalid_request" || client.msg != tt.want || len(client.acks) != 0 {
				t.Fatalf("错误=%s/%s ack=%d", client.code, client.msg, len(client.acks))
			}
		})
	}
	for _, kind := range []store.ConversationKind{store.ConversationKindRoom, store.ConversationKindStory} {
		t.Run(string(kind), func(t *testing.T) {
			group := createConv(t, pool, svc, kind, false, a.ID, b.ID)
			client := &userMediaClient{ident: &a}
			svc.HandleSend(ctx, client, userMediaEnv(t, group.ID, "image", own.String(), "配文"))
			if client.code != "invalid_request" || client.msg != "该会话不支持发送媒体" {
				t.Fatalf("错误=%s/%s", client.code, client.msg)
			}
		})
	}
	if len(broker.broadcasts) != 0 {
		t.Fatalf("无效发送广播了%d条", len(broker.broadcasts))
	}
}

func TestUserMediaRequireOneToOneInSendAs(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newUserMediaService(pool, nil)
	a, _ := createIdentity(t, pool, "a")
	b, _ := createIdentity(t, pool, "b")
	conv := createConv(t, pool, svc, store.ConversationKindRoom, false, a.ID, b.ID)
	id := insertUserMedia(t, pool, a.ID, "image", "user")
	_, _, err := svc.SendAs(ctx, conversation.SendInput{Identity: &a, ConversationID: conv.ID, ClientMsgID: newUUID(t), Kind: "image", MediaID: id, RequireOneToOne: true})
	var se *conversation.SendError
	if !errors.As(err, &se) || se.Code != "invalid_request" || se.Msg != "该会话不支持发送媒体" {
		t.Fatalf("err=%v", err)
	}
}

type deleteMediaFilter struct {
	pool *pgxpool.Pool
	id   pgtype.UUID
}

func (f deleteMediaFilter) Filter(ctx context.Context, text string) (string, bool) {
	_, err := f.pool.Exec(ctx, "DELETE FROM media_items WHERE id=$1", f.id)
	if err != nil {
		panic(err)
	}
	return text, false
}
func TestUserMediaDeletedBeforeInsertReturnsInvalidRequest(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	a, _ := createIdentity(t, pool, "a")
	b, _ := createIdentity(t, pool, "b")
	id := insertUserMedia(t, pool, a.ID, "image", "user")
	svc := newUserMediaService(pool, nil, conversation.WithFilter(deleteMediaFilter{pool: pool, id: id}))
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	client := &userMediaClient{ident: &a}
	svc.HandleSend(ctx, client, userMediaEnv(t, conv.ID, "image", id.String(), "配文"))
	if client.code != "invalid_request" || client.msg != "媒体已失效，请重新上传" {
		t.Fatalf("错误=%s/%s", client.code, client.msg)
	}
}
