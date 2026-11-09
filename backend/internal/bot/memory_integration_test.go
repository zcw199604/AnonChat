//go:build integration

package bot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/anonchat/anonchat/backend/migrations"
)

// memoryFakeLLM 仅记录内存中的假请求，不连接外部服务。
type memoryFakeLLM struct {
	calls  []ChatRequest
	result ChatResult
	err    error
	onCall func()
}

func (f *memoryFakeLLM) Chat(_ context.Context, req ChatRequest) (ChatResult, error) {
	f.calls = append(f.calls, req)
	if f.onCall != nil {
		f.onCall()
	}
	return f.result, f.err
}

type memoryFixture struct {
	pool                               *pgxpool.Pool
	e                                  *Engine
	llm                                *memoryFakeLLM
	now                                time.Time
	log                                bytes.Buffer
	botID, botIdentity, peerID, convID pgtype.UUID
}

func newMemoryFixture(t *testing.T) *memoryFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL 未设置")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrations.Up(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(context.Background(), `TRUNCATE identities, conversations, bots, character_cards, bot_settings RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	f := &memoryFixture{pool: pool, now: time.Now().UTC().Truncate(time.Microsecond), llm: &memoryFakeLLM{result: ChatResult{Content: `{"profile":[{"topic":"basic_info","subtopic":"称呼","value":"小林","action":"set"}],"events":[{"gist":"他说下周考试","tags":["考试"]}],"affinity_delta":2}`, PromptTokens: 30, CompletionTokens: 10, CachedTokens: 5}}}
	f.botIdentity = f.identity(t, "角色")
	f.peerID = f.identity(t, "对方")
	var cardID pgtype.UUID
	f.row(t, `INSERT INTO character_cards(name) VALUES('小雅') RETURNING id`).Scan(&cardID)
	f.row(t, `INSERT INTO bots(identity_id,label,card_id,enabled) VALUES($1,'记忆测试',$2,true) RETURNING id`, f.botIdentity, cardID).Scan(&f.botID)
	f.convID = f.conversation(t, f.botIdentity, f.peerID, true)
	settings := &SettingsCache{current: Settings{Ready: true, Enabled: true, Model: "main", MemoryModel: "memory-test", MemoryExtractEvery: 20, Timeout: time.Second, Location: time.UTC}}
	f.e = NewEngine(pool, slog.New(slog.NewTextHandler(&f.log, nil)), nil, f.llm, settings, WithClock(func() time.Time { return f.now }))
	return f
}

// checkedMemoryRow 保证 seed 的 Scan 错误也立即暴露。
type checkedMemoryRow struct {
	t   *testing.T
	row interface{ Scan(...any) error }
}

func (r checkedMemoryRow) Scan(dest ...any) {
	r.t.Helper()
	if err := r.row.Scan(dest...); err != nil {
		r.t.Fatal(err)
	}
}
func (f *memoryFixture) row(t *testing.T, q string, args ...any) checkedMemoryRow {
	t.Helper()
	return checkedMemoryRow{t, f.pool.QueryRow(context.Background(), q, args...)}
}
func (f *memoryFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *memoryFixture) identity(t *testing.T, name string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	f.row(t, `INSERT INTO identities(token_hash,nickname,avatar_seed,public_code) VALUES(decode(replace(gen_random_uuid()::text,'-',''),'hex'),$1,'seed',upper(substr(replace(gen_random_uuid()::text,'-',''),1,10))) RETURNING id`, name).Scan(&id)
	return id
}
func (f *memoryFixture) conversation(t *testing.T, bot, peer pgtype.UUID, ended bool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	var at any
	if ended {
		at = f.now
	}
	f.row(t, `INSERT INTO conversations(kind,ended_at) VALUES('match',$1) RETURNING id`, at).Scan(&id)
	f.exec(t, `INSERT INTO conversation_members(conversation_id,member_id,identity_id) VALUES($1,1,$2),($1,2,$3)`, id, bot, peer)
	return id
}
func (f *memoryFixture) message(t *testing.T, conv, sender pgtype.UUID, body string, at time.Time, burned bool) {
	t.Helper()
	var burnedAt any
	if burned {
		burnedAt = f.now
	}
	f.exec(t, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,created_at,burned_at) VALUES($1,$2,2,gen_random_uuid(),'text',$3,$4,$5)`, conv, sender, body, at, burnedAt)
}
func (f *memoryFixture) run(t *testing.T) {
	t.Helper()
	if err := f.e.memory.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *memoryFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	f.row(t, "SELECT count(*) FROM "+table).Scan(&n)
	return n
}
func (f *memoryFixture) cursor(t *testing.T) (pgtype.Timestamptz, int, pgtype.Timestamptz) {
	t.Helper()
	var until, next pgtype.Timestamptz
	var failures int
	f.row(t, `SELECT extracted_until,fail_count,next_try_at FROM bot_memory_cursors WHERE conversation_id=$1`, f.convID).Scan(&until, &failures, &next)
	return until, failures, next
}

func TestMemoryWorkerCanceledCallStillCountsUsage(t *testing.T) {
	f := newMemoryFixture(t)
	f.message(t, f.convID, f.peerID, "取消调用的消息-private", f.now.Add(-time.Minute), false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.llm.onCall = cancel
	f.llm.err = context.Canceled
	if err := f.e.memory.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回取消错误，实际 %v", err)
	}
	var calls, tokens, replies, used int
	f.row(t, `SELECT count(*),COALESCE(sum(prompt_tokens+completion_tokens),0),COALESCE(sum(replies),0) FROM bot_usage WHERE bot_id=$1 AND kind='memory'`, f.botID).Scan(&calls, &tokens, &replies)
	f.row(t, `SELECT used_tokens FROM bots WHERE id=$1`, f.botID).Scan(&used)
	if calls != 1 || tokens != 40 || used != 40 || replies != 0 {
		t.Fatalf("取消调用应恰好计量一次且不计回复：calls=%d tokens=%d used=%d replies=%d", calls, tokens, used, replies)
	}
	until, failures, next := f.cursor(t)
	if until.InfinityModifier != pgtype.NegativeInfinity || failures != 1 || !next.Time.Equal(f.now.Add(5*time.Minute)) {
		t.Fatal("取消调用应退避且不推进游标")
	}
	if f.count(t, "bot_peer_memories") != 0 || f.count(t, "bot_peer_events") != 0 {
		t.Fatal("取消调用不得写入画像或事件")
	}
	if strings.Contains(f.log.String(), "private") {
		t.Fatal("取消调用日志不得包含消息内容")
	}
}

func TestMemoryWorkerExtractEndedConversation(t *testing.T) {
	f := newMemoryFixture(t)
	last := f.now.Add(-time.Minute)
	f.message(t, f.convID, f.peerID, "我叫小林，下周考试", last, false)
	f.run(t)
	if len(f.llm.calls) != 1 {
		t.Fatalf("抽取调用数=%d", len(f.llm.calls))
	}
	req := f.llm.calls[0]
	if req.Model != "memory-test" || req.MaxTokens != 1200 || req.Temperature != 0.2 || req.CacheKey != "anonchat-memory" {
		t.Fatalf("未复用记忆模型参数: %+v", req)
	}
	until, fail, next := f.cursor(t)
	if !until.Time.Equal(last) || fail != 0 || next.Valid {
		t.Fatalf("游标错误: %v %d %v", until, fail, next)
	}
	var profile []byte
	var affinity int
	f.row(t, `SELECT profile,affinity FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&profile, &affinity)
	if !bytes.Contains(profile, []byte("小林")) || affinity != 2 || f.count(t, "bot_peer_events") != 1 {
		t.Fatal("画像、事件或好感度未写入")
	}
	var kind string
	var replies, prompt, completion, cached int
	f.row(t, `SELECT kind,replies,prompt_tokens,completion_tokens,cached_tokens FROM bot_usage WHERE bot_id=$1`, f.botID).Scan(&kind, &replies, &prompt, &completion, &cached)
	if kind != "memory" || replies != 0 || prompt != 30 || completion != 10 || cached != 5 {
		t.Fatal("抽取用量错误")
	}
	var used, replied int
	f.row(t, `SELECT used_tokens,used_replies FROM bots WHERE id=$1`, f.botID).Scan(&used, &replied)
	if used != 40 || replied != 0 {
		t.Fatal("累计用量错误")
	}
	f.run(t)
	if len(f.llm.calls) != 1 {
		t.Fatal("已抽取消息不应重复调用")
	}
}

func TestMemoryWorkerEventWithoutTags(t *testing.T) {
	f := newMemoryFixture(t)
	f.message(t, f.convID, f.peerID, "今天有件开心的事", f.now.Add(-time.Minute), false)
	f.llm.result.Content = `{"profile":[],"events":[{"gist":"他说今天很开心"}],"affinity_delta":0}`
	f.run(t)
	if f.count(t, "bot_peer_events") != 1 {
		t.Fatal("未提供标签的有效事件也应保存")
	}
}

func TestMemoryWorkerFailureBackoffAndSkip(t *testing.T) {
	for _, parseFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("解析失败=%v", parseFailure), func(t *testing.T) {
			f := newMemoryFixture(t)
			last := f.now.Add(-time.Minute)
			f.message(t, f.convID, f.peerID, "待抽取消息-private", last, false)
			if parseFailure {
				f.llm.result.Content = "无效 JSON-private"
			} else {
				f.llm.err = errors.New("模型错误-private")
			}
			start := f.now
			f.run(t)
			until, fail, next := f.cursor(t)
			if fail != 1 || !next.Time.Equal(start.Add(5*time.Minute)) || until.Time.Equal(last) {
				t.Fatalf("首次失败不应推进游标: %v %d %v", until, fail, next)
			}
			f.run(t)
			if len(f.llm.calls) != 1 {
				t.Fatal("退避期间不应调用")
			}
			f.now = start.Add(5 * time.Minute)
			f.run(t)
			until, fail, next = f.cursor(t)
			if fail != 2 || !next.Time.Equal(f.now.Add(10*time.Minute)) || until.Time.Equal(last) {
				t.Fatal("第二次退避错误")
			}
			f.now = f.now.Add(10 * time.Minute)
			f.run(t)
			until, fail, next = f.cursor(t)
			if fail != 0 || next.Valid || !until.Time.Equal(last) {
				t.Fatal("第三次失败应跳过本批并清零")
			}
			if f.count(t, "bot_usage") != 3 || f.count(t, "bot_peer_memories") != 0 {
				t.Fatal("失败有token时应计量，但不写记忆")
			}
			if strings.Contains(f.log.String(), "private") {
				t.Fatal("失败日志不得记录内容或模型原始错误")
			}
		})
	}
}

func TestMemoryWorkerSkipsLimitsNotReadyAndBurned(t *testing.T) {
	f := newMemoryFixture(t)
	f.message(t, f.convID, f.peerID, "正常消息", f.now.Add(-time.Minute), false)
	f.exec(t, `UPDATE bots SET total_token_limit=10,used_tokens=10 WHERE id=$1`, f.botID)
	f.run(t)
	until, failures, next := f.cursor(t)
	if len(f.llm.calls) != 0 || until.InfinityModifier != pgtype.NegativeInfinity || failures != 0 || !next.Time.Equal(f.now.Add(10*time.Minute)) {
		t.Fatal("超限跳过不得推进游标")
	}
	f.exec(t, `UPDATE bots SET used_tokens=0 WHERE id=$1`, f.botID)
	f.e.settings.mu.Lock()
	f.e.settings.current.Ready = false
	f.e.settings.mu.Unlock()
	f.run(t)
	if len(f.llm.calls) != 0 || f.count(t, "bot_memory_cursors") != 1 {
		t.Fatal("未就绪不得抽取")
	}
	f.e.settings.mu.Lock()
	f.e.settings.current.Ready = true
	f.e.settings.mu.Unlock()
	f.now = f.now.Add(10 * time.Minute)
	f.message(t, f.convID, f.peerID, "焚毁消息-secret", f.now.Add(-30*time.Second), true)
	f.run(t)
	if len(f.llm.calls) != 1 {
		t.Fatal("恢复后应抽取")
	}
	for _, m := range f.llm.calls[0].Messages {
		if strings.Contains(m.Content, "焚毁消息-secret") {
			t.Fatal("已焚毁消息进入抽取输入")
		}
	}
	until, _, _ = f.cursor(t)
	if !until.Time.Equal(f.now.Add(-11 * time.Minute)) {
		t.Fatal("游标应以未焚毁批次末尾为准")
	}
}

func TestMemoryWorkerLimitedCandidatesDoNotStarveOtherBot(t *testing.T) {
	f := newMemoryFixture(t)
	f.exec(t, `UPDATE bots SET total_token_limit=10,used_tokens=10 WHERE id=$1`, f.botID)
	previous := f.now.Add(-2 * time.Hour)
	f.exec(t, `INSERT INTO bot_memory_cursors(conversation_id,bot_id,extracted_until,fail_count) VALUES($1,$2,$3,2)`, f.convID, f.botID, previous)
	for i := 0; i < 20; i++ {
		id := f.convID
		if i > 0 {
			id = f.conversation(t, f.botIdentity, f.peerID, true)
		}
		f.message(t, id, f.peerID, "超限会话", f.now.Add(-time.Hour), false)
	}
	otherIdentity := f.identity(t, "其他角色")
	var otherBot pgtype.UUID
	f.row(t, `INSERT INTO bots(identity_id,label,enabled) VALUES($1,'可用bot',true) RETURNING id`, otherIdentity).Scan(&otherBot)
	otherConv := f.conversation(t, otherIdentity, f.peerID, true)
	f.message(t, otherConv, f.peerID, "其他角色的新消息", f.now.Add(-time.Minute), false)
	f.run(t)
	if len(f.llm.calls) != 0 {
		t.Fatal("首轮20个超限候选不得调用模型")
	}
	until, fail, next := f.cursor(t)
	if !until.Time.Equal(previous) || fail != 2 || !next.Time.Equal(f.now.Add(10*time.Minute)) {
		t.Fatal("超限只能推迟重试时间，不得改变提取位置或失败计数")
	}
	f.run(t)
	if len(f.llm.calls) != 1 {
		t.Fatal("第二轮应抽取被前20个超限会话挡住的其他bot")
	}
	var n int
	f.row(t, `SELECT count(*) FROM bot_peer_memories WHERE bot_id=$1`, otherBot).Scan(&n)
	if n != 1 {
		t.Fatal("其他bot记忆未保存")
	}
}

func TestMemoryWorkerIsolationAndLoadTouch(t *testing.T) {
	f := newMemoryFixture(t)
	f.message(t, f.convID, f.peerID, "对第一个角色说的秘密", f.now.Add(-time.Minute), false)
	f.run(t)
	otherIdentity := f.identity(t, "另一个角色")
	var otherBot pgtype.UUID
	f.row(t, `INSERT INTO bots(identity_id,label,enabled) VALUES($1,'另一bot',true) RETURNING id`, otherIdentity).Scan(&otherBot)
	m, relation, err := f.e.loadPeerMemory(context.Background(), f.botID, f.peerID)
	if err != nil || m == nil || relation.Stage == "" || !strings.Contains(RenderMemory(m), "小林") {
		t.Fatalf("读取记忆失败: %v", err)
	}
	other, _, err := f.e.loadPeerMemory(context.Background(), otherBot, f.peerID)
	if err != nil || other != nil {
		t.Fatalf("其他bot不应读到已有记忆: %v", err)
	}
	otherConv := f.conversation(t, otherIdentity, f.peerID, true)
	f.message(t, otherConv, f.peerID, "我喜欢音乐", f.now.Add(-time.Minute), false)
	f.llm.result.Content = `{"profile":[{"topic":"interest","subtopic":"音乐","value":"钢琴","action":"set"}],"events":[],"affinity_delta":-3}`
	f.run(t)
	if f.count(t, "bot_peer_memories") != 2 {
		t.Fatal("两个bot应分别存储")
	}
	other, _, err = f.e.loadPeerMemory(context.Background(), otherBot, f.peerID)
	if err != nil || other == nil || !strings.Contains(RenderMemory(other), "钢琴") || strings.Contains(RenderMemory(other), "小林") {
		t.Fatal("记忆发生跨bot混合")
	}
	f.now = f.now.Add(time.Minute)
	if err := f.e.touchPeerMemory(context.Background(), f.botID, f.peerID, 5, "开心", 7); err != nil {
		t.Fatal(err)
	}
	var affinity, value int
	var label string
	var at time.Time
	f.row(t, `SELECT affinity,mood_label,mood_value,last_interaction_at FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&affinity, &label, &value, &at)
	if affinity != 7 || label != "开心" || value != 7 || !at.Equal(f.now) {
		t.Fatal("touch 未更新关系与时间")
	}
	f.exec(t, `UPDATE bots SET memory_enabled=false WHERE id=$1`, f.botID)
	m, _, err = f.e.loadPeerMemory(context.Background(), f.botID, f.peerID)
	if err != nil || m != nil {
		t.Fatal("关闭记忆时应返回nil")
	}
}

func TestMemoryWorkerLatest80AndEventBounds(t *testing.T) {
	f := newMemoryFixture(t)
	for i := 0; i < 90; i++ {
		f.message(t, f.convID, f.peerID, fmt.Sprintf("序列-%02d", i), f.now.Add(time.Duration(i-100)*time.Second), false)
	}
	events := make([]map[string]any, 4)
	for i := range events {
		events[i] = map[string]any{"gist": strings.Repeat("事", 121), "tags": []string{"测试"}}
	}
	result, _ := json.Marshal(map[string]any{"profile": []any{}, "events": events, "affinity_delta": 50})
	f.llm.result.Content = string(result)
	f.run(t)
	if len(f.llm.calls) != 1 {
		t.Fatal("未抽取")
	}
	var user string
	for _, m := range f.llm.calls[0].Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if strings.Contains(user, "序列-09") || !strings.Contains(user, "序列-10") || strings.Index(user, "序列-10") >= strings.Index(user, "序列-89") {
		t.Fatal("应取最新80条并按时间升序")
	}
	var n, maxLen, affinity int
	f.row(t, `SELECT count(*),COALESCE(max(char_length(gist)),0) FROM bot_peer_events`).Scan(&n, &maxLen)
	f.row(t, `SELECT affinity FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&affinity)
	if n != 3 || maxLen != 120 || affinity != 10 {
		t.Fatalf("抽取边界错误: %d %d %d", n, maxLen, affinity)
	}
	f.exec(t, `INSERT INTO bot_peer_events(bot_id,peer_identity_id,gist,created_at) SELECT $1,$2,'旧事件',$3::timestamptz - make_interval(secs=>i) FROM generate_series(1,50) i`, f.botID, f.peerID, f.now.Add(-time.Hour))
	f.message(t, f.convID, f.peerID, "又有新消息", f.now, false)
	f.llm.result.Content = `{"profile":[],"events":[{"gist":"新事件","tags":[]}],"affinity_delta":0}`
	f.run(t)
	if f.count(t, "bot_peer_events") != 50 {
		t.Fatal("事件应裁剪到50条")
	}
	m, _, err := f.e.loadPeerMemory(context.Background(), f.botID, f.peerID)
	if err != nil || m == nil || len(m.Events) != 8 {
		t.Fatalf("回复时应只读最近8个事件: %v", err)
	}
}

func TestMemoryWorkerTransactionRollback(t *testing.T) {
	f := newMemoryFixture(t)
	f.message(t, f.convID, f.peerID, "新消息", f.now.Add(-time.Minute), false)
	// 在事件插入处制造数据库约束错误，确认前面的画像与后面的游标、用量整体回滚。
	f.exec(t, `ALTER TABLE bot_peer_events ADD CONSTRAINT memory_test_reject CHECK (gist <> '他说下周考试')`)
	t.Cleanup(func() { f.exec(t, `ALTER TABLE bot_peer_events DROP CONSTRAINT IF EXISTS memory_test_reject`) })
	if err := f.e.memory.RunOnce(context.Background()); err == nil {
		t.Fatal("事务失败应返回错误")
	}
	if f.count(t, "bot_peer_memories") != 0 || f.count(t, "bot_peer_events") != 0 || f.count(t, "bot_usage") != 0 || f.count(t, "bot_memory_cursors") != 0 {
		t.Fatal("失败事务不得留下半份记忆或用量")
	}
}

func TestMemoryWorkerCandidateTriggers(t *testing.T) {
	for _, tc := range []struct {
		name              string
		count             int
		age               time.Duration
		botOnly, oldEnded bool
		want              int
	}{
		{"新消息未达阈值", 4, time.Minute, false, false, 0},
		{"达到阈值", 5, time.Minute, false, false, 1},
		{"空闲十分钟", 1, 11 * time.Minute, false, false, 1},
		{"只有角色发言", 5, time.Minute, true, false, 0},
		{"结束超过八天", 5, time.Minute, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMemoryFixture(t)
			f.e.settings.current.MemoryExtractEvery = 5
			f.exec(t, `UPDATE conversations SET ended_at=NULL WHERE id=$1`, f.convID)
			if tc.oldEnded {
				f.exec(t, `UPDATE conversations SET ended_at=$2 WHERE id=$1`, f.convID, f.now.Add(-9*24*time.Hour))
			}
			sender := f.peerID
			if tc.botOnly {
				sender = f.botIdentity
			}
			for i := 0; i < tc.count; i++ {
				f.message(t, f.convID, sender, fmt.Sprintf("记录%d", i), f.now.Add(-tc.age).Add(time.Duration(i)*time.Millisecond), false)
			}
			f.run(t)
			if len(f.llm.calls) != tc.want {
				t.Fatalf("调用数=%d，期望%d", len(f.llm.calls), tc.want)
			}
		})
	}
}

func TestMemoryWorkerScrubsStoredValues(t *testing.T) {
	f := newMemoryFixture(t)
	f.message(t, f.convID, f.peerID, "对方自己说出的信息", f.now.Add(-time.Minute), false)
	f.llm.result.Content = `{"profile":[{"topic":"basic_info","subtopic":"备注","value":"联系000-0000和fake@example.invalid","action":"set"}],"events":[{"gist":"提到000 0000","tags":[]}],"affinity_delta":0}`
	f.run(t)
	var profile []byte
	var gist string
	f.row(t, `SELECT profile FROM bot_peer_memories WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&profile)
	f.row(t, `SELECT gist FROM bot_peer_events WHERE bot_id=$1 AND peer_identity_id=$2`, f.botID, f.peerID).Scan(&gist)
	if bytes.Contains(profile, []byte("000")) || bytes.Contains(profile, []byte("@")) || strings.Contains(gist, "000") || !strings.Contains(gist, "***") {
		t.Fatal("写入前应清洗画像和事件中的隐私")
	}
}
