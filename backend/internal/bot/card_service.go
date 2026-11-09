package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/bot/persona"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// CardMedia 是角色卡所需的媒体操作；解析包不依赖媒体服务。
type CardMedia interface {
	CreateFolder(context.Context, string) (store.MediaFolder, error)
	SaveItem(context.Context, pgtype.UUID, string, io.Reader, []string, string) (store.MediaItem, error)
	DeleteFolder(context.Context, pgtype.UUID) error
	DeleteItem(context.Context, pgtype.UUID) error
	MaxUploadBytes() int64
}

// WithMedia 注入角色卡表情使用的媒体服务。
func WithMedia(svc CardMedia) Option { return func(s *Service) { s.media = svc } }

// CardInput 是新建和更新角色卡的字段，校验规则与导入共用。
type CardInput persona.Card

// ImportResult 是导入后保存的角色卡与警告。
type ImportResult struct {
	Card     store.CharacterCard
	Warnings []string
}

// ListCards 返回后台角色卡列表。
func (s *Service) ListCards(ctx context.Context) ([]store.CharacterCard, error) {
	return s.q.ListCharacterCards(ctx)
}

// GetCard 读取单张角色卡。
func (s *Service) GetCard(ctx context.Context, id pgtype.UUID) (store.CharacterCard, error) {
	card, err := s.q.GetCharacterCard(ctx, id)
	return card, cardError(err)
}

// CreateCard 手动创建角色卡；来源格式只能由服务端指定。
func (s *Service) CreateCard(ctx context.Context, in CardInput) (store.CharacterCard, error) {
	card := persona.Card(in)
	card.SourceFormat, card.Extensions = "manual", nil
	return insertCard(ctx, s.q, card)
}

// UpdateCard 更新可编辑字段，保留来源、扩展与表情文件夹。
func (s *Service) UpdateCard(ctx context.Context, id pgtype.UUID, in CardInput) (store.CharacterCard, error) {
	params, err := cardParams(persona.Card(in))
	if err != nil {
		return store.CharacterCard{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.CharacterCard{}, err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)
	existing, err := q.GetCharacterCardForUpdate(ctx, id)
	if err != nil {
		return store.CharacterCard{}, cardError(err)
	}
	updated, err := q.UpdateCharacterCard(ctx, store.UpdateCharacterCardParams{
		ID: id, Name: params.Name, Description: params.Description, Personality: params.Personality,
		Scenario: params.Scenario, FirstMessage: params.FirstMessage, ExampleDialogue: params.ExampleDialogue,
		SystemPrompt: params.SystemPrompt, HeartPrompt: params.HeartPrompt, ReasonPrompt: params.ReasonPrompt,
		AlternateGreetings: params.AlternateGreetings, PostHistoryInstructions: params.PostHistoryInstructions,
		Lorebook: params.Lorebook, Tools: params.Tools, CreatorNotes: params.CreatorNotes,
		ExpressionFolderID: existing.ExpressionFolderID, SourceFormat: existing.SourceFormat, Extensions: existing.Extensions,
	})
	if err != nil {
		return store.CharacterCard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.CharacterCard{}, err
	}
	return updated, nil
}

// DeleteCard 删除卡片及其专属表情文件夹。
func (s *Service) DeleteCard(ctx context.Context, id pgtype.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)
	card, err := q.GetCharacterCardForUpdate(ctx, id)
	if err != nil {
		return cardError(err)
	}
	if card.ExpressionFolderID.Valid && s.media == nil {
		return mediaUnavailable()
	}
	if _, err := q.DeleteCharacterCard(ctx, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if card.ExpressionFolderID.Valid {
		return s.media.DeleteFolder(ctx, card.ExpressionFolderID)
	}
	return nil
}

// ImportCard 解析输入并保存卡片；独立媒体操作失败时补偿已保存的文件。
func (s *Service) ImportCard(ctx context.Context, fileName string, data []byte) (out ImportResult, err error) {
	options := []persona.Option{}
	if s.media != nil {
		options = append(options, persona.WithMaxImageBytes(s.media.MaxUploadBytes()))
	}
	parsed, err := persona.Parse(fileName, data, options...)
	if err != nil {
		return out, httpx.NewError(httpx.CodeInvalidRequest, err.Error())
	}
	if len(parsed.Expressions) > 0 && s.media == nil {
		return out, mediaUnavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	var folderID pgtype.UUID
	defer func() {
		// 先释放卡片与媒体外键持有的事务锁，再执行独立的清理操作。
		_ = tx.Rollback(context.WithoutCancel(ctx))
		if err != nil && folderID.Valid {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			err = errors.Join(err, s.media.DeleteFolder(cleanup, folderID))
		}
	}()
	q := s.q.WithTx(tx)
	card, err := insertCard(ctx, q, parsed.Card)
	if err != nil {
		return out, err
	}
	if len(parsed.Expressions) > 0 {
		folder, ferr := s.createExpressionFolder(ctx, card.Name)
		if ferr != nil {
			return out, ferr
		}
		folderID = folder.ID
		for i, expression := range parsed.Expressions {
			item, serr := s.media.SaveItem(ctx, folderID, expression.FileName, bytes.NewReader(expression.Data), []string{}, expression.Description)
			if serr != nil {
				return out, serr
			}
			if _, serr = q.InsertCardExpression(ctx, store.InsertCardExpressionParams{CardID: card.ID, Key: expression.Key, Description: expression.Description, MediaItemID: item.ID, Sort: int32(i)}); serr != nil {
				return out, expressionError(serr)
			}
		}
		card, err = q.SetCardExpressionFolder(ctx, store.SetCardExpressionFolderParams{ID: card.ID, ExpressionFolderID: folderID})
		if err != nil {
			return out, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return ImportResult{Card: card, Warnings: parsed.Warnings}, nil
}

// MaxExpressionBytes 返回单个表情上传上限。
func (s *Service) MaxExpressionBytes() int64 {
	if s.media == nil {
		return 0
	}
	return s.media.MaxUploadBytes()
}

// ListExpressions 按排序号与 key 返回卡片的表情。
func (s *Service) ListExpressions(ctx context.Context, cardID pgtype.UUID) ([]store.CardExpression, error) {
	if _, err := s.GetCard(ctx, cardID); err != nil {
		return nil, err
	}
	return s.q.ListCardExpressions(ctx, cardID)
}

// ExpressionCount 读取角色卡的表情数量。
func (s *Service) ExpressionCount(ctx context.Context, cardID pgtype.UUID) (int64, error) {
	return s.q.CountCardExpressions(ctx, cardID)
}

// CreateExpression 为卡片保存一个表情；首次上传时自动创建专属文件夹。
func (s *Service) CreateExpression(ctx context.Context, cardID pgtype.UUID, key, description, fileName string, data []byte) (out store.CardExpression, err error) {
	if s.media == nil {
		return out, mediaUnavailable()
	}
	if err := validateExpression(key, description); err != nil {
		return out, err
	}
	if int64(len(data)) > s.media.MaxUploadBytes() {
		return out, httpx.NewError(httpx.CodeInvalidRequest, "表情图片超过大小上限")
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return out, httpx.NewError(httpx.CodeInvalidRequest, "表情只支持 png/jpg/gif/webp 图片")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	var createdFolder, itemID pgtype.UUID
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		if err == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if createdFolder.Valid {
			err = errors.Join(err, s.media.DeleteFolder(cleanup, createdFolder))
		} else if itemID.Valid {
			err = errors.Join(err, s.media.DeleteItem(cleanup, itemID))
		}
	}()
	q := s.q.WithTx(tx)
	card, err := q.GetCharacterCardForUpdate(ctx, cardID)
	if err != nil {
		return out, cardError(err)
	}
	n, err := q.CountCardExpressions(ctx, cardID)
	if err != nil {
		return out, err
	}
	if n >= 100 {
		return out, httpx.NewError(httpx.CodeInvalidRequest, "每张角色卡最多 100 个表情")
	}
	folderID := card.ExpressionFolderID
	if !folderID.Valid {
		folder, ferr := s.createExpressionFolder(ctx, card.Name)
		if ferr != nil {
			return out, ferr
		}
		folderID, createdFolder = folder.ID, folder.ID
	}
	item, err := s.media.SaveItem(ctx, folderID, fileName, bytes.NewReader(data), []string{}, description)
	if err != nil {
		return out, err
	}
	itemID = item.ID
	out, err = q.InsertCardExpression(ctx, store.InsertCardExpressionParams{CardID: cardID, Key: key, Description: description, MediaItemID: itemID})
	if err != nil {
		return out, expressionError(err)
	}
	if createdFolder.Valid {
		if _, err = q.SetCardExpressionFolder(ctx, store.SetCardExpressionFolderParams{ID: cardID, ExpressionFolderID: folderID}); err != nil {
			return out, err
		}
	}
	err = tx.Commit(ctx)
	return out, err
}

// UpdateExpression 更新表情 key、描述与排序，同时同步媒体说明。
func (s *Service) UpdateExpression(ctx context.Context, id pgtype.UUID, key, description string, sort int32) (store.CardExpression, error) {
	if err := validateExpression(key, description); err != nil {
		return store.CardExpression{}, err
	}
	existing, err := s.q.GetCardExpression(ctx, id)
	if err != nil {
		return store.CardExpression{}, expressionError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.CardExpression{}, err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)
	if _, err := q.GetCharacterCardForUpdate(ctx, existing.CardID); err != nil {
		return store.CardExpression{}, cardError(err)
	}
	updated, err := q.UpdateCardExpression(ctx, store.UpdateCardExpressionParams{ID: id, Key: key, Description: description, Sort: sort})
	if err != nil {
		return store.CardExpression{}, expressionError(err)
	}
	if _, err := q.UpdateMediaItem(ctx, store.UpdateMediaItemParams{ID: updated.MediaItemID, Tags: []string{}, Caption: description}); err != nil {
		return store.CardExpression{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.CardExpression{}, err
	}
	return updated, nil
}

// DeleteExpression 删除表情与对应的媒体文件。
func (s *Service) DeleteExpression(ctx context.Context, id pgtype.UUID) error {
	if s.media == nil {
		return mediaUnavailable()
	}
	existing, err := s.q.GetCardExpression(ctx, id)
	if err != nil {
		return expressionError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)
	if _, err := q.GetCharacterCardForUpdate(ctx, existing.CardID); err != nil {
		return cardError(err)
	}
	n, err := q.DeleteCardExpression(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return expressionError(pgx.ErrNoRows)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.media.DeleteItem(ctx, existing.MediaItemID)
}

func insertCard(ctx context.Context, q *store.Queries, card persona.Card) (store.CharacterCard, error) {
	params, err := cardParams(card)
	if err != nil {
		return store.CharacterCard{}, err
	}
	return q.InsertCharacterCard(ctx, params)
}

func cardParams(card persona.Card) (store.InsertCharacterCardParams, error) {
	card.Name = strings.TrimSpace(card.Name)
	if err := persona.Validate(card); err != nil {
		return store.InsertCharacterCardParams{}, httpx.NewError(httpx.CodeInvalidRequest, err.Error())
	}
	if card.AlternateGreetings == nil {
		card.AlternateGreetings = []string{}
	}
	if card.Tools == nil {
		card.Tools = []string{}
	}
	if card.Lorebook == nil {
		card.Lorebook = []persona.LoreEntry{}
	}
	if card.Extensions == nil {
		card.Extensions = map[string]any{}
	}
	lore, err := json.Marshal(card.Lorebook)
	if err != nil {
		return store.InsertCharacterCardParams{}, err
	}
	extensions, err := json.Marshal(card.Extensions)
	if err != nil {
		return store.InsertCharacterCardParams{}, err
	}
	source := card.SourceFormat
	if source == "" {
		source = "manual"
	}
	return store.InsertCharacterCardParams{
		Name: card.Name, Description: strings.TrimSpace(card.Description), Personality: strings.TrimSpace(card.Personality),
		Scenario: strings.TrimSpace(card.Scenario), FirstMessage: strings.TrimSpace(card.FirstMessage),
		ExampleDialogue: strings.TrimSpace(card.ExampleDialogue), SystemPrompt: strings.TrimSpace(card.SystemPrompt),
		HeartPrompt: strings.TrimSpace(card.HeartPrompt), ReasonPrompt: strings.TrimSpace(card.ReasonPrompt),
		PostHistoryInstructions: strings.TrimSpace(card.PostHistoryInstructions), CreatorNotes: strings.TrimSpace(card.CreatorNotes),
		AlternateGreetings: card.AlternateGreetings, Lorebook: lore, Tools: card.Tools, SourceFormat: source, Extensions: extensions,
	}, nil
}

func (s *Service) createExpressionFolder(ctx context.Context, name string) (store.MediaFolder, error) {
	base := "表情·" + name
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf("-%d", n)
		}
		candidate := truncateRunes(base, 40-utf8.RuneCountInString(suffix)) + suffix
		folder, err := s.media.CreateFolder(ctx, candidate)
		if err == nil {
			return folder, nil
		}
		var apiErr *httpx.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != httpx.CodeInvalidRequest || apiErr.Message != "文件夹名已存在" {
			return store.MediaFolder{}, err
		}
	}
}

func validateExpression(key, description string) error {
	if !persona.ValidExpressionKey(key) {
		return httpx.NewError(httpx.CodeInvalidRequest, "表情 key 需为 1–32 个字母、数字、下划线、连字符或中文")
	}
	if utf8.RuneCountInString(description) > 200 {
		return httpx.NewError(httpx.CodeInvalidRequest, "表情描述最多 200 字")
	}
	return nil
}

func cardError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCardNotFound
	}
	return err
}

func expressionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "表情不存在")
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return httpx.NewError(httpx.CodeInvalidRequest, "表情 key 已存在")
	}
	return err
}

func mediaUnavailable() error {
	return httpx.NewError(httpx.CodeInternal, "表情媒体服务不可用")
}
