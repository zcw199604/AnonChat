//go:build integration

package bot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/bottle"
	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/jackc/pgx/v5/pgtype"
)

// p5LLM 按轮次返回协议文本，可在调用中制造新输入。
type p5LLM struct {
	calls []ChatRequest
	reply func(int, ChatRequest) (ChatResult, error)
}

func (l *p5LLM) Chat(_ context.Context, req ChatRequest) (ChatResult, error) {
	l.calls = append(l.calls, req)
	if l.reply != nil {
		return l.reply(len(l.calls), req)
	}
	return ChatResult{Content: "你好", PromptTokens: 10, CompletionTokens: 5}, nil
}

// p5Broker 直接捕获与真实用户共享的广播载荷。
type p5Broker struct{ messages []realtime.Envelope }

func (b *p5Broker) Broadcast(_ pgtype.UUID, env realtime.Envelope, _ map[pgtype.UUID]struct{}) {
	b.messages = append(b.messages, env)
}
func (b *p5Broker) SendToIdentity(_ pgtype.UUID, env realtime.Envelope) {
	b.messages = append(b.messages, env)
}

type p5Fixture struct {
	*memoryFixture
	model   *p5LLM
	broker  *p5Broker
	mediaID pgtype.UUID
}

func newP5Fixture(t *testing.T, mode string) *p5Fixture {
	t.Helper()
	f := newMemoryFixture(t)
	f.exec(t, `UPDATE conversations SET ended_at=NULL WHERE id=$1`, f.convID)
	f.exec(t, `UPDATE bots SET core_mode=$2,reply_delay_min_ms=0,reply_delay_max_ms=0,typing_ms_per_char=0,conv_min_interval_seconds=0 WHERE id=$1`, f.botID, mode)
	var folder, media pgtype.UUID
	f.row(t, `INSERT INTO media_folders(name) VALUES('表情-'||gen_random_uuid()::text) RETURNING id`).Scan(&folder)
	f.row(t, `INSERT INTO media_items(folder_id,kind,mime,size_bytes,storage_key,tags) VALUES($1,'image','image/png',1,gen_random_uuid()::text,ARRAY['开心']) RETURNING id`, folder).Scan(&media)
	f.exec(t, `INSERT INTO card_expressions(card_id,key,description,media_item_id) SELECT card_id,'开心','开心地笑',$2 FROM bots WHERE id=$1`, f.botID, media)
	model := &p5LLM{}
	broker := &p5Broker{}
	f.e.llmOverride = model
	f.e.conv = conversation.New(f.pool, f.e.logger, broker, conversation.WithClock(func() time.Time { return f.now }))
	f.e.sleep = func(context.Context, time.Duration) error { return nil }
	f.e.convs[f.convID] = &convState{}
	f.message(t, f.convID, f.peerID, "你好", f.now.Add(-time.Second), false)
	return &p5Fixture{memoryFixture: f, model: model, broker: broker, mediaID: media}
}
func p5RequestText(req ChatRequest) string {
	var parts []string
	for _, m := range req.Messages {
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n")
}
func (f *p5Fixture) sent(t *testing.T) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT kind FROM messages WHERE conversation_id=$1 AND sender_identity_id=$2 ORDER BY id`, f.convID, f.botIdentity)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	return kinds
}
func TestP5SinglePromptHasExpressions(t *testing.T) {
	f := newP5Fixture(t, "single")
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 1 || !strings.Contains(p5RequestText(f.model.calls[0]), "可用表情：开心（开心地笑）") {
		t.Fatal("single 心核提示词必须显式渲染可用表情")
	}
}
func TestP5DualReasonToolAndUsage(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.model.reply = func(n int, req ChatRequest) (ChatResult, error) {
		content := `{"tool_calls":[{"name":"get_time","arguments":{}}]}`
		if n == 2 {
			content = `{"thought":"想法-private","guidance":"温柔问候","mood":{"label":"开心","value":7},"affinity_delta":3,"expression":"开心","silent":false}`
		}
		if n == 3 {
			content = "你好呀"
		}
		return ChatResult{Content: content, PromptTokens: 10, CompletionTokens: 5}, nil
	}
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 3 {
		t.Fatalf("dual 工具、结论、心核应调用3次，实际%d", len(f.model.calls))
	}
	if strings.Contains(p5RequestText(f.model.calls[2]), "可用表情：") {
		t.Fatal("dual 心核不得列表情")
	}
	kinds := f.sent(t)
	if strings.Join(kinds, ",") != "text,image" {
		t.Fatalf("表情应在文本后以普通image发送: %v", kinds)
	}
	var reason, reply, replies int
	f.row(t, `SELECT count(*) FILTER(WHERE kind='reason'),count(*) FILTER(WHERE kind='reply'),sum(replies) FROM bot_usage WHERE bot_id=$1`, f.botID).Scan(&reason, &reply, &replies)
	if reason != 2 || reply != 1 || replies != 1 {
		t.Fatalf("用量应2 reason + 1 reply，且只回复计数1: %d %d %d", reason, reply, replies)
	}
	var affinity, value int
	var mood string
	f.row(t, `SELECT affinity,mood_label,mood_value FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&affinity, &mood, &value)
	if affinity != 3 || mood != "开心" || value != 7 {
		t.Fatal("智核情绪和好感度未在成功发送后应用")
	}
	if strings.Contains(f.log.String(), "private") {
		t.Fatal("智核内容不得写日志")
	}
	for _, env := range f.broker.messages {
		if env.Type != realtime.TypeMsgNew {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(env.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 2 || payload["conversationId"] == nil || payload["message"] == nil {
			t.Fatal("msg.new 顶层字段集合发生变化")
		}
		message := payload["message"].(map[string]any)
		want := []string{"id", "memberId", "nickname", "avatarSeed", "kind", "body", "createdAt", "burnAfterRead"}
		if message["kind"] == "image" {
			want = append(want, "mediaUrl")
		}
		if len(message) != len(want) {
			t.Fatal("msg.new 消息字段集合发生变化")
		}
		for _, field := range want {
			if _, ok := message[field]; !ok {
				t.Fatalf("msg.new 缺少原字段 %s", field)
			}
		}
		for _, key := range []string{"identityId", "identity_id", "bot", "memory", "affinity", "thought"} {
			if strings.Contains(string(env.Data), `"`+key+`"`) {
				t.Fatalf("用户消息含内部字段%s", key)
			}
		}
	}
}

func TestP5ReasonFailureFallsBackToSingle(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.model.reply = func(n int, _ ChatRequest) (ChatResult, error) {
		text := "不是智核JSON-private"
		if n > 1 {
			text = "恢复正常回复\n[表情:开心]"
		}
		return ChatResult{Content: text, PromptTokens: 10, CompletionTokens: 5}, nil
	}
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 2 || !strings.Contains(p5RequestText(f.model.calls[1]), "可用表情：开心（开心地笑）") {
		t.Fatal("智核失败必须以single重新构造提示词并列出表情")
	}
	if got := strings.Join(f.sent(t), ","); got != "text,image" {
		t.Fatalf("降级后应继续回复并发表情: %s", got)
	}
	var reasons int
	f.row(t, `SELECT count(*) FROM bot_usage WHERE kind='reason' AND replies=0`).Scan(&reasons)
	if reasons != 1 {
		t.Fatal("智核解析失败但有token也应计量")
	}
}

func TestP5ExpressionsAfterTextAndPendingInput(t *testing.T) {
	for _, mode := range []string{"single", "dual"} {
		for _, pending := range []bool{false, true} {
			name := mode + "/正常"
			if pending {
				name = mode + "/打断"
			}
			t.Run(name, func(t *testing.T) {
				f := newP5Fixture(t, mode)
				f.model.reply = func(_ int, req ChatRequest) (ChatResult, error) {
					if strings.HasSuffix(req.CacheKey, "-reason") {
						return ChatResult{Content: `{"guidance":"你好","expression":"开心"}`, PromptTokens: 10}, nil
					}
					if pending {
						f.e.mu.Lock()
						f.e.convs[f.convID].dirty = true
						f.e.mu.Unlock()
					}
					return ChatResult{Content: "[表情:开心]\n你好\n再聊聊\n[表情:开心]", PromptTokens: 10}, nil
				}
				f.e.runTurn(f.convID, false)
				want := "text,text,image"
				if pending {
					want = "text,text"
				}
				if got := strings.Join(f.sent(t), ","); got != want {
					t.Fatalf("发送顺序/最多1表情/打断: got=%s want=%s", got, want)
				}
			})
		}
	}
}

func TestP5ExpressionRespectsConversationMediaLimit(t *testing.T) {
	for _, mode := range []string{"single", "dual"} {
		t.Run(mode, func(t *testing.T) {
			f := newP5Fixture(t, mode)
			f.exec(t, `UPDATE bots SET conv_media_limit=0 WHERE id=$1`, f.botID)
			f.model.reply = func(_ int, req ChatRequest) (ChatResult, error) {
				if strings.HasSuffix(req.CacheKey, "-reason") {
					return ChatResult{Content: `{"guidance":"你好","expression":"开心"}`}, nil
				}
				return ChatResult{Content: "你好\n[表情:开心]"}, nil
			}
			f.e.runTurn(f.convID, false)
			if got := strings.Join(f.sent(t), ","); got != "text" {
				t.Fatalf("表情也必须受conv_media_limit限制: %s", got)
			}
		})
	}
}

func TestP5StyleRetryAndSetting(t *testing.T) {
	for _, guard := range []bool{false, true} {
		t.Run(map[bool]string{false: "关闭", true: "开启"}[guard], func(t *testing.T) {
			f := newP5Fixture(t, "single")
			f.e.settings.current.OOCGuard = guard
			f.model.reply = func(n int, _ ChatRequest) (ChatResult, error) {
				text := "**你好**"
				if n > 1 {
					text = "## 第二次也有Markdown"
				}
				return ChatResult{Content: text, PromptTokens: 10, CompletionTokens: 5}, nil
			}
			f.e.runTurn(f.convID, false)
			want := 1
			if guard {
				want = 2
			}
			if len(f.model.calls) != want {
				t.Fatalf("guard=%v 模型调用数%d", guard, len(f.model.calls))
			}
			if guard && !strings.Contains(p5RequestText(f.model.calls[1]), "上一次回复不符合聊天风格") {
				t.Fatal("重试缺少FixHint")
			}
			var body string
			f.row(t, `SELECT body FROM messages WHERE conversation_id=$1 AND sender_identity_id=$2 ORDER BY id DESC LIMIT 1`, f.convID, f.botIdentity).Scan(&body)
			if guard && (strings.Contains(body, "#") || strings.Contains(body, "**")) {
				t.Fatal("重试仍跑偏时应Sanitize")
			}
			var replies, retry int
			f.row(t, `SELECT sum(replies),count(*) FILTER(WHERE kind='retry') FROM bot_usage WHERE bot_id=$1`, f.botID).Scan(&replies, &retry)
			if replies != 1 || retry != want-1 {
				t.Fatalf("重试不应额外计回复: %d %d", replies, retry)
			}
		})
	}
}

func TestP5AIIdentityTextIsUnchanged(t *testing.T) {
	f := newP5Fixture(t, "single")
	f.e.settings.current.OOCGuard = true
	identityText := "我是 AI，我是机器人，我不是真人"
	f.model.reply = func(_ int, _ ChatRequest) (ChatResult, error) { return ChatResult{Content: identityText}, nil }
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 1 {
		t.Fatal("身份表述不得触发重试")
	}
	var body string
	f.row(t, `SELECT body FROM messages WHERE conversation_id=$1 AND sender_identity_id=$2 ORDER BY id DESC LIMIT 1`, f.convID, f.botIdentity).Scan(&body)
	if body != identityText {
		t.Fatalf("身份表述被修改: %q", body)
	}
}

func TestP5MemoryInjectedAndIsolated(t *testing.T) {
	f := newP5Fixture(t, "single")
	f.exec(t, `INSERT INTO bot_peer_memories(bot_id,peer_identity_id,profile,affinity,mood_label,mood_value) VALUES($1,$2,'{"basic_info":{"称呼":{"v":"专属称呼-private","t":"2026-11-09T12:00:00Z"}}}',30,'开心',7)`, f.botID, f.peerID)
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 1 || !strings.Contains(p5RequestText(f.model.calls[0]), "专属称呼-private") || !strings.Contains(p5RequestText(f.model.calls[0]), "熟悉（轻松随意）") {
		t.Fatal("回复前应注入该bot画像与关系")
	}
	otherIdentity := f.identity(t, "另一角色")
	var otherBot pgtype.UUID
	f.row(t, `INSERT INTO bots(identity_id,label,card_id,enabled,reply_delay_min_ms,reply_delay_max_ms,typing_ms_per_char,conv_min_interval_seconds) SELECT $1,'其他',card_id,true,0,0,0,0 FROM bots WHERE id=$2 RETURNING id`, otherIdentity, f.botID).Scan(&otherBot)
	otherConv := f.conversation(t, otherIdentity, f.peerID, false)
	f.message(t, otherConv, f.peerID, "你好", f.now.Add(-time.Second), false)
	f.e.convs[otherConv] = &convState{}
	f.e.runTurn(otherConv, false)
	if len(f.model.calls) != 2 || strings.Contains(p5RequestText(f.model.calls[1]), "专属称呼-private") {
		t.Fatal("同一对方对不同bot的记忆不得混入")
	}
	if strings.Contains(f.log.String(), "private") {
		t.Fatal("记忆不得写日志")
	}
}

func TestP5AlternateGreetingSkipsReason(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.exec(t, `DELETE FROM messages WHERE conversation_id=$1`, f.convID)
	f.exec(t, `UPDATE character_cards SET first_message='',alternate_greetings=ARRAY['{{char}}向{{user}}问好'] WHERE id=(SELECT card_id FROM bots WHERE id=$1)`, f.botID)
	f.e.runTurn(f.convID, true)
	if len(f.model.calls) != 0 {
		t.Fatal("备选开场白也应直接发送，不调用智核或心核")
	}
	var body string
	f.row(t, `SELECT body FROM messages WHERE conversation_id=$1 AND sender_identity_id=$2 ORDER BY id DESC LIMIT 1`, f.convID, f.botIdentity).Scan(&body)
	if body != "小雅向对方问好" {
		t.Fatalf("备选开场白宏替换错误: %q", body)
	}
}

func TestP5ReasonSilentAppliesRelationWithoutReply(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.model.reply = func(_ int, _ ChatRequest) (ChatResult, error) {
		return ChatResult{Content: `{"silent":true,"affinity_delta":-2,"mood":{"label":"平静","value":5}}`, PromptTokens: 10, CompletionTokens: 5}, nil
	}
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 1 || len(f.sent(t)) != 0 {
		t.Fatal("智核沉默时不得调用心核或发送内容")
	}
	var affinity, replies int
	f.row(t, `SELECT affinity FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&affinity)
	f.row(t, `SELECT sum(replies) FROM bot_usage WHERE bot_id=$1`, f.botID).Scan(&replies)
	if affinity != -2 || replies != 0 {
		t.Fatal("智核沉默仍需应用关系且不计回复")
	}
}

func TestP5ReasonCallErrorStillRepliesAndCountsTokens(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.model.reply = func(n int, _ ChatRequest) (ChatResult, error) {
		if n == 1 {
			return ChatResult{PromptTokens: 12, CompletionTokens: 3}, errors.New("模型错误-private")
		}
		return ChatResult{Content: "正常回复"}, nil
	}
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 2 || strings.Join(f.sent(t), ",") != "text" {
		t.Fatal("智核调用失败不得阻断single回复")
	}
	var tokens, replies int
	f.row(t, `SELECT sum(prompt_tokens+completion_tokens),sum(replies) FROM bot_usage WHERE bot_id=$1 AND kind='reason'`, f.botID).Scan(&tokens, &replies)
	if tokens != 15 || replies != 0 {
		t.Fatal("失败智核返回的token必须计入reason")
	}
	if strings.Contains(f.log.String(), "private") {
		t.Fatal("原始模型错误不得进入日志")
	}
}

func TestP5ReasonLimitStopsAdditionalCalls(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.exec(t, `UPDATE bots SET total_token_limit=10 WHERE id=$1`, f.botID)
	f.model.reply = func(_ int, _ ChatRequest) (ChatResult, error) {
		return ChatResult{Content: `{"guidance":"友善回复"}`, PromptTokens: 10}, nil
	}
	f.e.runTurn(f.convID, false)
	if len(f.model.calls) != 1 || len(f.sent(t)) != 0 {
		t.Fatal("智核已耗尽限额时不得继续消耗心核")
	}
}

type p5BottleService struct {
	picked *bottle.PickedBottle
	convID pgtype.UUID
	reply  string
	throws int
}

func (s *p5BottleService) HoldForBot(context.Context, pgtype.UUID, time.Duration) (*bottle.PickedBottle, error) {
	return s.picked, nil
}
func (s *p5BottleService) Reply(_ context.Context, _, _ pgtype.UUID, text string) (pgtype.UUID, error) {
	s.reply = text
	return s.convID, nil
}
func (s *p5BottleService) ThrowBack(context.Context, pgtype.UUID, pgtype.UUID) error {
	s.throws++
	return nil
}

func TestP5BottleMemoryAndStyleWithoutReasonOrExpression(t *testing.T) {
	f := newP5Fixture(t, "dual")
	f.e.settings.current.OOCGuard = true
	f.exec(t, `INSERT INTO bot_peer_memories(bot_id,peer_identity_id,profile,affinity) VALUES($1,$2,'{"basic_info":{"称呼":{"v":"瓶子称呼-private","t":"2026-11-09T12:00:00Z"}}}',30)`, f.botID, f.peerID)
	svc := &p5BottleService{picked: &bottle.PickedBottle{ID: genTestUUID(t), Content: "今晚睡不着", PosterIdentityID: f.peerID}, convID: f.convID}
	f.e.bottles = svc
	f.model.reply = func(n int, req ChatRequest) (ChatResult, error) {
		text := "**你好**"
		if n > 1 {
			text = "好好休息\n[表情:开心]"
		}
		return ChatResult{Content: text, PromptTokens: 10, CompletionTokens: 5}, nil
	}
	bot, err := f.e.q.GetBot(context.Background(), f.botID)
	if err != nil {
		t.Fatal(err)
	}
	f.e.bottleForBot(context.Background(), &botEntry{bot: bot})
	if len(f.model.calls) != 2 || svc.reply != "好好休息" || svc.throws != 0 {
		t.Fatalf("瓶子应仅心核重试且只发送文本: calls=%d text=%q throws=%d", len(f.model.calls), svc.reply, svc.throws)
	}
	for _, req := range f.model.calls {
		text := p5RequestText(req)
		if !strings.Contains(text, "瓶子称呼-private") || !strings.Contains(text, "熟悉（轻松随意）") || strings.Contains(text, "可用表情：") || strings.HasSuffix(req.CacheKey, "-reason") {
			t.Fatal("瓶子须注入投瓶人记忆，但不走智核或表情列表")
		}
	}
	var reply, retry, reason, replies int
	f.row(t, `SELECT count(*) FILTER(WHERE kind='reply'),count(*) FILTER(WHERE kind='retry'),count(*) FILTER(WHERE kind='reason'),sum(replies) FROM bot_usage WHERE bot_id=$1`, f.botID).Scan(&reply, &retry, &reason, &replies)
	if reply != 1 || retry != 1 || reason != 0 || replies != 1 {
		t.Fatalf("瓶子调用分类型计量错误 %d %d %d %d", reply, retry, reason, replies)
	}
	var affinity int
	f.row(t, `SELECT affinity FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&affinity)
	if affinity != 30 {
		t.Fatal("瓶子single触碰不得改变好感度")
	}
}
