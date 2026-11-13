package wanfeng

import (
	"context"
	"fmt"
	"strings"
)

// 三期：历史会话 / 联系人 / 历史消息。
//
// 对方这一组接口都是「POST + X-Get-Via-Post: 1」，query 参数在加密信封的明文里，
// 返回的 data 也是一层加密字符串（APIClient.Call 已统一处理解密）。
//
// 字段名与路径全部来自 Protocol，对方改版时用 WANFENG_PROTOCOL_OVERRIDES 覆盖即可。

// Relation 是对方「历史会话/联系人」列表里的一条。
type Relation struct {
	// UID 是关系记录自身的 id（对方列表项可能叫 uid，也可能是空串）。
	UID string
	// TargetUID 是对方用户在晚风站点的 uid（我方发消息用的 targetUid）。
	TargetUID      string
	TargetNickname string
	// TargetGender 已归一化成对方的状态机值（male / female / secret）。
	TargetGender string
	TargetAge    int
	// LastMessageAt 是 RFC3339 时间字符串，可能为空。
	LastMessageAt string
	CreatedAt     string
	UnreadCount   int
	Pinned        bool
}

// Favorite 是对方「收藏」列表里的一条。
type Favorite struct {
	UID           string
	Nickname      string
	Gender        string
	Age           int
	LastMessageAt string
	Pinned        bool
}

// HomeData 是对方首页聚合数据（历史会话 + 收藏）。
type HomeData struct {
	Relations      []Relation
	RelationsTotal int
	Favorites      []Favorite
	FavoritesTotal int
}

// HistoryMessage 是对方聊天记录里的一条。
type HistoryMessage struct {
	Tid       string
	FromCid   string
	FromUID   string
	TargetUID string
	Type      string
	// Payload 是消息正文：文本消息是文字，媒体消息是对方媒体 URL。
	Payload  string
	TS       int
	Duration int
	Flash    bool
	Recalled bool
	// ReadByRecipient 表示这条消息对方是否已读（自己发的消息才有意义）。
	ReadByRecipient bool
	Nickname        string
	Gender          string
	Age             int
}

// ChatHistoryPage 是一页聊天记录。
type ChatHistoryPage struct {
	Messages []HistoryMessage
	HasMore  bool
}

// HomeData 拉取「历史会话 + 收藏」聚合数据（对方前端进入首页时调用的接口）。
func (c *APIClient) HomeData(ctx context.Context, uid string, pageSize int) (*HomeData, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, fmt.Errorf("缺少 uid")
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	data, err := c.Call(ctx, c.proto.HomeDataPath, map[string]any{
		"uid":      uid,
		"paged":    1,
		"pageSize": pageSize,
	})
	if err != nil {
		return nil, err
	}

	result := &HomeData{
		Relations: c.parseRelations(data),
		Favorites: c.parseFavorites(data),
	}
	result.RelationsTotal = PickInt(data, "relationsTotal")
	if result.RelationsTotal <= 0 {
		result.RelationsTotal = len(result.Relations)
	}
	result.FavoritesTotal = PickInt(data, "favoritesTotal")
	if result.FavoritesTotal <= 0 {
		result.FavoritesTotal = len(result.Favorites)
	}
	return result, nil
}

// Relations 分页拉取历史会话列表。
func (c *APIClient) Relations(ctx context.Context, uid string, page, pageSize int) ([]Relation, int, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, 0, fmt.Errorf("缺少 uid")
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	data, err := c.Call(ctx, c.proto.RelationsPath, map[string]any{
		"uid":      uid,
		"page":     page,
		"pageSize": pageSize,
	})
	if err != nil {
		return nil, 0, err
	}

	list := c.parseRelations(data)
	total := PickInt(data, "total", "relationsTotal")
	if total <= 0 {
		total = len(list)
	}
	return list, total, nil
}

// Favorites 分页拉取收藏列表。
func (c *APIClient) Favorites(ctx context.Context, uid string, page, pageSize int) ([]Favorite, int, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, 0, fmt.Errorf("缺少 uid")
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	data, err := c.Call(ctx, c.proto.FavoritesPath, map[string]any{
		"uid":      uid,
		"page":     page,
		"pageSize": pageSize,
	})
	if err != nil {
		return nil, 0, err
	}

	list := c.parseFavorites(data)
	total := PickInt(data, "total", "favoritesTotal")
	if total <= 0 {
		total = len(list)
	}
	return list, total, nil
}

// ChatMessages 拉取与某个人的聊天记录（before 为空表示最新一页）。
func (c *APIClient) ChatMessages(ctx context.Context, uid, targetUID string, limit int, before string) (*ChatHistoryPage, error) {
	uid = strings.TrimSpace(uid)
	targetUID = strings.TrimSpace(targetUID)
	if uid == "" || targetUID == "" {
		return nil, fmt.Errorf("缺少 uid 或 targetUid")
	}
	if limit <= 0 {
		limit = 30
	}

	params := map[string]any{
		"uid":       uid,
		"targetUid": targetUID,
		"limit":     limit,
	}
	if cursor := strings.TrimSpace(before); cursor != "" {
		params["before"] = cursor
	}

	data, err := c.Call(ctx, c.proto.ChatMessagesPath, params)
	if err != nil {
		return nil, err
	}

	page := &ChatHistoryPage{Messages: c.parseHistoryMessages(data)}
	if v, ok := pickAny(data, c.proto.HistoryHasMoreKeys...).(bool); ok {
		page.HasMore = v
	}
	return page, nil
}

// —— 关系维护（不影响聊天主流程，失败只记日志）——

// MarkRelationRead 标记与某人的会话已读。
func (c *APIClient) MarkRelationRead(ctx context.Context, uid, targetUID string) error {
	_, err := c.Call(ctx, c.proto.RelationReadPath, map[string]any{
		"uid":       strings.TrimSpace(uid),
		"targetUid": strings.TrimSpace(targetUID),
	})
	return err
}

// MarkAllRelationsRead 一键已读。
func (c *APIClient) MarkAllRelationsRead(ctx context.Context, uid string) error {
	_, err := c.Call(ctx, c.proto.RelationReadAllPath, map[string]any{"uid": strings.TrimSpace(uid)})
	return err
}

// DeleteRelation 删除一条会话记录（对方侧不再显示该会话）。
func (c *APIClient) DeleteRelation(ctx context.Context, uid, targetUID string) error {
	_, err := c.Call(ctx, c.proto.RelationDeletePath, map[string]any{
		"uid":       strings.TrimSpace(uid),
		"targetUid": strings.TrimSpace(targetUID),
	})
	return err
}

// ClearRelations 批量清空会话记录。
func (c *APIClient) ClearRelations(ctx context.Context, uid string, targetUIDs []string) error {
	if len(targetUIDs) == 0 {
		return nil
	}
	_, err := c.Call(ctx, c.proto.RelationClearPath, map[string]any{
		"uid":        strings.TrimSpace(uid),
		"targetUids": targetUIDs,
	})
	return err
}

// PinFavorite 置顶/取消置顶收藏。
func (c *APIClient) PinFavorite(ctx context.Context, uid, targetUID string, pinned bool) error {
	_, err := c.Call(ctx, c.proto.FavoritePinPath, map[string]any{
		"uid":       strings.TrimSpace(uid),
		"targetUid": strings.TrimSpace(targetUID),
		"pinned":    pinned,
	})
	return err
}

// Relation 把对方列表项解析成 Relation。
func (c *APIClient) parseRelations(data map[string]any) []Relation {
	raw := pickAny(data, c.proto.RelationListKeys...)
	items := asMapSlice(raw)
	out := make([]Relation, 0, len(items))
	for _, item := range items {
		rel := Relation{
			UID:            PickString(item, "uid", "id"),
			TargetUID:      PickString(item, c.proto.RelationTargetKeys...),
			TargetNickname: PickString(item, c.proto.RelationNicknameKeys...),
			TargetGender:   SexToUpstream(PickString(item, c.proto.RelationGenderKeys...)),
			TargetAge:      PickInt(item, c.proto.RelationAgeKeys...),
			LastMessageAt:  PickString(item, c.proto.RelationLastAtKeys...),
			CreatedAt:      PickString(item, "createdAt", "created_at"),
			UnreadCount:    PickInt(item, "unreadCount", "unread"),
		}
		if v, ok := pickAny(item, "pinned").(bool); ok {
			rel.Pinned = v
		}
		if rel.TargetUID == "" {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// parseFavorites 把对方收藏列表项解析成 Favorite。
func (c *APIClient) parseFavorites(data map[string]any) []Favorite {
	raw := pickAny(data, c.proto.FavoriteListKeys...)
	items := asMapSlice(raw)
	out := make([]Favorite, 0, len(items))
	for _, item := range items {
		fav := Favorite{
			UID:           PickString(item, c.proto.FavoriteUIDKeys...),
			Nickname:      PickString(item, c.proto.FavoriteNicknameKeys...),
			Gender:        SexToUpstream(PickString(item, c.proto.FavoriteGenderKeys...)),
			Age:           PickInt(item, c.proto.FavoriteAgeKeys...),
			LastMessageAt: PickString(item, c.proto.RelationLastAtKeys...),
		}
		if v, ok := pickAny(item, "pinned").(bool); ok {
			fav.Pinned = v
		}
		if fav.UID == "" {
			continue
		}
		out = append(out, fav)
	}
	return out
}

// parseHistoryMessages 解析聊天记录列表。
func (c *APIClient) parseHistoryMessages(data map[string]any) []HistoryMessage {
	raw := pickAny(data, c.proto.HistoryMessageKeys...)
	items := asMapSlice(raw)
	out := make([]HistoryMessage, 0, len(items))
	for _, item := range items {
		msg := HistoryMessage{
			Tid:       PickString(item, c.proto.MessageIDKeys...),
			FromCid:   PickString(item, "fromCid", "from_cid"),
			FromUID:   PickString(item, c.proto.FromUIDKeys...),
			TargetUID: PickString(item, c.proto.TargetUIDKeys...),
			Type:      strings.ToLower(PickString(item, c.proto.TypeKeys...)),
			Payload:   PickString(item, "payload", "content"),
			TS:        PickInt(item, c.proto.TimeKeys...),
			Duration:  PickInt(item, "duration"),
			Nickname:  PickString(item, c.proto.NicknameKeys...),
			Gender:    SexToUpstream(PickString(item, c.proto.GenderKeys...)),
			Age:       PickInt(item, c.proto.AgeKeys...),
		}
		if v, ok := pickAny(item, "flash").(bool); ok {
			msg.Flash = v
		}
		if v, ok := pickAny(item, "readByRecipient").(bool); ok {
			msg.ReadByRecipient = v
		}
		if v, ok := pickAny(item, "recalled").(bool); ok {
			msg.Recalled = v
		}
		if msg.Type == "" {
			msg.Type = MediaKindText
		}
		out = append(out, msg)
	}
	return out
}

// asMapSlice 把 any 尽量转成 []map[string]any（容错 []any / map 单条）。
func asMapSlice(raw any) []map[string]any {
	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		// 极少数实现会把列表包一层（如 {list:{items:[...]}}）
		for _, key := range []string{"list", "items", "records"} {
			if inner, ok := v[key]; ok {
				if out := asMapSlice(inner); len(out) > 0 {
					return out
				}
			}
		}
		return nil
	default:
		return nil
	}
}

// PickBool 依次尝试多个候选键并取布尔值（对方有时用 "true"/1）。
func PickBool(m map[string]any, keys ...string) (value bool, ok bool) {
	switch v := pickAny(m, keys...).(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		}
	case float64:
		return v != 0, true
	case int:
		return v != 0, true
	}
	return false, false
}

// PickStringSlice 依次尝试多个候选键并转成字符串切片（对方列表/回执字段容错）。
func PickStringSlice(m map[string]any, keys ...string) []string {
	switch v := pickAny(m, keys...).(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return []string{s}
		}
	}
	return nil
}
