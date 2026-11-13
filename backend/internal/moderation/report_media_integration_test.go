//go:build integration

package moderation_test

import (
	"encoding/json"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestReportSnapshotPersistsMediaKindsAndIDs(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	reporter, _ := mustCreateIdentity(t, pool, "reporter")
	sender, _ := mustCreateIdentity(t, pool, "sender")
	conv := mustCreateConv(t, pool, store.ConversationKindMatch, reporter.ID, sender.ID)
	mid := memberID(t, pool, conv.ID, sender.ID)
	type expected struct {
		id                int64
		kind, body, media string
		burned            bool
	}
	var wants []expected
	for _, kind := range []string{"text", "image", "video"} {
		var mediaID pgtype.UUID
		if kind != "text" {
			if err := pool.QueryRow(ctx, `INSERT INTO media_items(kind,mime,size_bytes,storage_key,source,owner_identity_id) VALUES($1,$2,8,$3,'user',$4) RETURNING id`, kind, map[string]string{"image": "image/png", "video": "video/mp4"}[kind], "report-"+kind, sender.ID).Scan(&mediaID); err != nil {
				t.Fatal(err)
			}
		}
		body := "证据文字"
		if kind != "text" {
			body = "媒体配文"
		}
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,sender_member_id,sender_identity_id,kind,body,media_id,client_msg_id) VALUES($1,$2,$3,$4,$5,$6,gen_random_uuid()) RETURNING id`, conv.ID, mid, sender.ID, kind, body, mediaID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		mediaString := ""
		if mediaID.Valid {
			mediaString = mediaID.String()
		}
		wants = append(wants, expected{id, kind, body, mediaString, false})
	}
	// 已焚毁的上下文仍保留既有空正文和 burnedAt 语义；不恢复任何已清空内容。
	var burnedID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,sender_member_id,sender_identity_id,kind,body,burned_at,client_msg_id) VALUES($1,$2,$3,'text','',now(),gen_random_uuid()) RETURNING id`, conv.ID, mid, sender.ID).Scan(&burnedID); err != nil {
		t.Fatal(err)
	}
	wants = append(wants, expected{burnedID, "text", "", "", true})
	svc := moderation.NewReportService(pool, testLogger(), nil, nil)
	reportID, err := svc.Report(ctx, reporter.ID, moderation.ReportRequest{ConversationID: conv.ID, MemberID: mid, MessageIDs: []int64{wants[1].id}, Reason: "porn"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, "SELECT snapshot FROM reports WHERE id=$1", reportID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot []struct {
		ID       int64  `json:"id"`
		Kind     string `json:"kind"`
		MediaID  string `json:"mediaId"`
		Body     string `json:"body"`
		BurnedAt string `json:"burnedAt"`
		Reported bool   `json:"reported"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != len(wants) {
		t.Fatalf("快照条数=%d want=%d body=%s", len(snapshot), len(wants), raw)
	}
	for i, want := range wants {
		got := snapshot[i]
		if got.ID != want.id || got.Kind != want.kind || got.MediaID != want.media || got.Body != want.body || (got.BurnedAt != "") != want.burned || got.Reported != (want.id == wants[1].id) {
			t.Errorf("snapshot[%d]=%+v want=%+v", i, got, want)
		}
	}
	// mediaId 在文本快照中省略，不产生零UUID或空字符串字段。
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 3} {
		if _, ok := items[i]["mediaId"]; ok {
			t.Errorf("文本快照不应携带mediaId: %v", items[i])
		}
	}
}
