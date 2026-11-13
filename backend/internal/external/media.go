package external

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Each adapter belongs to one link: late callbacks can never consume a new link's tokens.
type linkMedia struct {
	b     *Bridge
	l     *link
	mu    sync.Mutex
	api   *wanfeng.APIClient
	cache map[string]mediaEcho
}
type mediaEcho struct {
	token   string
	until   time.Time
	mediaID pgtype.UUID
}

func (l *link) mediaContext() (context.Context, context.CancelFunc) {
	ctx := l.mediaCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 30*time.Second)
}

func mediaToken(key string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "anonchat-" + hex.EncodeToString(random[:]) + path.Ext(key), nil
}

func (m *linkMedia) client(ctx context.Context) (*wanfeng.APIClient, error) {
	if m.api != nil {
		return m.api, nil
	}
	binding, err := m.b.bindings.Get(ctx, m.l.userID.String())
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.UpstreamUID == "" {
		return nil, errors.New("missing external identity")
	}
	api := m.b.deps.Wanfeng.Clone()
	api.SetUploadUserID(binding.UpstreamUID)
	if binding.IdentityToken != "" {
		if err = api.BindIdentityCookie(ctx, binding.IdentityToken); err != nil {
			return nil, err
		}
	}
	m.api = api
	return api, nil
}

func (m *linkMedia) valid(identity string) bool {
	return identity == m.l.userID.String() && !m.l.closed && !m.l.stopping.Load() && (m.l.state == linkInRoom || m.l.state == linkActivating)
}
func (m *linkMedia) remember(raw, token string, mediaID pgtype.UUID) {
	if m.cache == nil {
		m.cache = make(map[string]mediaEcho)
	}
	for key, e := range m.cache {
		if time.Now().After(e.until) {
			delete(m.cache, key)
		}
	}
	m.cache[wanfeng.MediaURLKey(raw)] = mediaEcho{token: token, until: time.Now().Add(time.Hour), mediaID: mediaID}
}
func (m *linkMedia) MirrorOutbound(identity, kind, token string) (string, error) {
	m.l.mu.Lock()
	id, ok := m.l.outboundTokens[token]
	valid := m.valid(identity)
	if valid && ok {
		delete(m.l.outboundTokens, token)
	}
	m.l.mu.Unlock()
	if !valid || !ok {
		return "", errors.New("unregistered media token")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := m.l.mediaContext()
	defer cancel()
	item, f, err := m.b.deps.Media.OpenItem(ctx, id)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if item.Kind != kind {
		return "", errors.New("media kind mismatch")
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	api, err := m.client(ctx)
	if err != nil {
		return "", err
	}
	uploaded, err := api.UploadMedia(ctx, path.Base(item.StorageKey), item.Mime, data, api.Protocol().BucketImage)
	if err != nil {
		return "", err
	}
	if err = api.RefreshMediaExpiry(ctx, uploaded.FileID, api.UploadUserID(), false); err != nil {
		m.b.deps.Logger.Debug("external media refresh failed", "error", err)
	}
	m.remember(uploaded.URL, token, pgtype.UUID{})
	return uploaded.URL, nil
}
func (m *linkMedia) MirrorInbound(identity, kind, raw string) (string, error) {
	m.l.mu.Lock()
	valid := m.valid(identity)
	attempt := m.l.attempt
	m.l.mu.Unlock()
	if !valid {
		return "", errors.New("inactive media link")
	}
	// Resolve before checking, and check before any network request (including cookie binding).
	base, err := url.Parse(m.b.deps.Wanfeng.BaseURL())
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(ref)
	allowed := false
	for _, host := range m.b.deps.WanfengMediaHosts {
		if strings.EqualFold(target.Hostname(), host) {
			allowed = true
			break
		}
	}
	if !allowed || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return "", errors.New("media host not allowed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.cache[wanfeng.MediaURLKey(target.String())]; ok && time.Now().Before(e.until) {
		if e.mediaID.Valid {
			// A repeated incoming URL reuses the file, but receives a fresh one-use token.
			token, err := mediaToken(e.token)
			if err != nil {
				return "", err
			}
			m.l.mu.Lock()
			defer m.l.mu.Unlock()
			if !m.valid(identity) || m.l.attempt != attempt {
				return "", errors.New("inactive media link")
			}
			m.l.inboundTokens[token] = e.mediaID
			return token, nil
		}
		return e.token, nil
	}
	ctx, cancel := m.l.mediaContext()
	defer cancel()
	api, err := m.client(ctx)
	if err != nil {
		return "", err
	}
	data, _, err := api.DownloadMedia(ctx, target.String())
	if err != nil {
		return "", err
	}
	item, err := m.b.deps.Media.SaveExternalItem(ctx, path.Base(target.Path), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	token, err := mediaToken(item.StorageKey)
	if err != nil {
		return "", err
	}
	m.l.mu.Lock()
	if !m.valid(identity) || m.l.attempt != attempt {
		m.l.mu.Unlock()
		return "", errors.New("inactive media link")
	}
	m.l.inboundTokens[token] = item.ID
	m.l.mu.Unlock()
	m.remember(target.String(), token, item.ID)
	return token, nil
}

func (b *Bridge) sendInboundMediaLocked(l *link, id pgtype.UUID, kind, tid string) {
	msgID := uuid.New()
	if tid != "" {
		msgID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(l.provider.Name()+"|"+l.peerUID+"|"+tid))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, inserted, err := b.send(ctx, conversation.SendInput{Identity: l.peer, ConversationID: l.convID, ClientMsgID: pgtype.UUID{Bytes: msgID, Valid: true}, Kind: kind, MediaID: id, SkipRateLimit: true})
	if err == nil && inserted {
		b.count(l.provider.Name(), func(c *Counters) { c.RelayedIn++ })
	}
}
func (b *Bridge) receiveMediaLocked(l *link, token wanfeng.MediaToken, tid string) bool {
	if l.provider.Name() == "wanfeng" {
		id, ok := l.inboundTokens[token.Path]
		if !ok {
			return false
		}
		delete(l.inboundTokens, token.Path)
		// Kind comes from the sniffed persisted item, never from a remote extension.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		item, f, err := b.deps.Media.OpenItem(ctx, id)
		cancel()
		if err == nil {
			f.Close()
			b.sendInboundMediaLocked(l, id, item.Kind, tid)
		}
		return true
	}
	ctx, cancel := l.mediaContext()
	defer cancel()
	data, _, err := b.chatMedia.Download(ctx, token.Path)
	var item store.MediaItem
	if err == nil {
		item, err = b.deps.Media.SaveExternalItem(ctx, token.Path, bytes.NewReader(data))
	}
	if err != nil {
		kind := "图片"
		if token.Kind == "video" {
			kind = "视频"
		}
		b.sendInboundLocked(l, "["+kind+"加载失败]", tid)
	} else {
		b.sendInboundMediaLocked(l, item.ID, item.Kind, tid)
	}
	return true
}

// Called only after GetMessageForRelay has confirmed conversation and sender.
func (b *Bridge) sendMediaLocked(ctx context.Context, l *link, row store.GetMessageForRelayRow) error {
	if b.deps.Media == nil {
		return errors.New("media service unavailable")
	}
	item, f, err := b.deps.Media.OpenItem(ctx, row.MediaID)
	if err != nil {
		return err
	}
	defer f.Close()
	if item.Kind != row.Kind {
		return errors.New("media kind mismatch")
	}
	var token string
	if l.provider.Name() == "wanfeng" {
		token, err = mediaToken(item.StorageKey)
		if err == nil {
			l.outboundTokens[token] = item.ID
		}
	} else {
		data, readErr := io.ReadAll(f)
		if readErr != nil {
			return readErr
		}
		s, ok := l.sess.(*chat2019Session)
		if !ok {
			return errors.New("invalid chat2019 session")
		}
		token, err = b.chatMedia.Upload(ctx, s.self, path.Base(item.StorageKey), data)
	}
	if err != nil {
		return err
	}
	if err = l.sess.sendMedia(l.peerUID, l.peerNick, item.Kind, token); err != nil {
		delete(l.outboundTokens, token)
		return err
	}
	return nil
}

var _ wanfeng.MediaBridge = (*linkMedia)(nil)
