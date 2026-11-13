package wanfeng

import (
	"errors"
	"path"
	"strings"
)

// 媒体消息（二期）相关：我方前端的图片/视频沿用 chat2019 媒体服务器的「文件名」编码
// （content 形如 "[xxx.jpg]"），本包负责把它与对方站点的媒体 URL 互转。
//
// 数据流：
//   - 发送：前端上传到我方 → 得到 chat2019 远程文件名 → 这里取回字节 → 传到对方上传接口
//     → 得到对方 URL → 用 type=image/video + content=URL 发送。
//   - 接收：对方推送 URL → 这里下载字节 → 传回 chat2019 媒体服务器拿文件名
//     → 下发给前端 "[文件名]"，前端渲染链路完全复用，无需改动。
const (
	// MediaKindImage / MediaKindVideo / MediaKindAudio 是对方消息 type 取值。
	MediaKindImage = "image"
	MediaKindVideo = "video"
	MediaKindAudio = "audio"
	MediaKindText  = "text"
)

// ErrMediaExpired 表示对方媒体已过期/不存在（对方媒体只有约 30 分钟有效期，
// 过期后接口返回 404「文件不存在或已失效」，且无法恢复）。
var ErrMediaExpired = errors.New("对方媒体已失效")

// OutgoingMedia 描述一条待搬运并发送的媒体消息。
type OutgoingMedia struct {
	// Kind 是 image/video/audio。
	Kind string
	// Path 是 chat2019 媒体服务器上的远程文件名（前端 "[xxx.jpg]" 里的 xxx.jpg）。
	Path string
}

// MediaBridge 负责「我方媒体 ↔ 对方媒体」的字节搬运，由 app 层实现。
// 之所以抽成接口：本包不感知 chat2019 媒体服务器/图片端口策略，便于单测与替换。
type MediaBridge interface {
	// MirrorOutbound 把 chat2019 媒体文件名转成对方可用的媒体 URL。
	MirrorOutbound(identityID, kind, remoteFilename string) (string, error)
	// MirrorInbound 把对方媒体 URL 转成 chat2019 媒体文件名（供前端渲染）。
	MirrorInbound(identityID, kind, remoteURL string) (string, error)
}

// mediaExtensions 用于从文件名推断媒体类型（与前端 parseMessageSegments 保持一致）。
var mediaExtensions = map[string]string{
	".jpg": MediaKindImage, ".jpeg": MediaKindImage, ".png": MediaKindImage,
	".gif": MediaKindImage, ".webp": MediaKindImage, ".bmp": MediaKindImage,
	".heic": MediaKindImage, ".avif": MediaKindImage,
	".mp4": MediaKindVideo, ".mov": MediaKindVideo, ".webm": MediaKindVideo,
	".m4v": MediaKindVideo, ".avi": MediaKindVideo, ".mkv": MediaKindVideo,
	".mp3": MediaKindAudio, ".m4a": MediaKindAudio, ".aac": MediaKindAudio,
	".wav": MediaKindAudio, ".ogg": MediaKindAudio, ".amr": MediaKindAudio,
	".webm_audio": MediaKindAudio,
}

// MediaKindFromName 按扩展名推断媒体类型；无法判断时返回空串。
func MediaKindFromName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	if idx := strings.IndexAny(clean, "?#"); idx >= 0 {
		clean = clean[:idx]
	}
	ext := strings.ToLower(path.Ext(clean))
	if ext == "" {
		// 形如 "voice_1700000000.m4a" 的已带扩展名，path.Ext 可处理；
		// 这里兜底处理没有扩展名但带类型的场景。
		return ""
	}
	return mediaExtensions[ext]
}

// MediaToken 是前端 content 中解析出的媒体引用。
type MediaToken struct {
	Kind   string
	Path   string
	Legacy bool
}

// ParseMediaToken 解析前端 content 里的媒体引用，支持：
//
//	"[xxx.jpg]"          现有前端格式（uploadFile 后发送）
//	"[img]xxx.jpg[/img]"  旧版历史格式
//	"[video]xxx.mp4[/video]"
//
// 非媒体内容返回 ok=false。
func ParseMediaToken(content string) (MediaToken, bool) {
	raw := strings.TrimSpace(content)
	if raw == "" {
		return MediaToken{}, false
	}

	// 旧版双标签格式
	for _, pair := range [][2]string{{"[img]", "[/img]"}, {"[video]", "[/video]"}, {"[audio]", "[/audio]"}} {
		if strings.HasPrefix(raw, pair[0]) && strings.HasSuffix(raw, pair[1]) {
			inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, pair[0]), pair[1]))
			if inner == "" {
				return MediaToken{}, false
			}
			kind := MediaKindFromName(inner)
			if kind == "" {
				kind = strings.Trim(pair[0], "[]")
				if kind == "img" {
					kind = MediaKindImage
				}
			}
			return MediaToken{Kind: kind, Path: inner, Legacy: true}, true
		}
	}

	// "[xxx]" 单标签：仅当内容看起来是媒体文件名时才认（避免把 "[doge]" 表情当媒体）
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := strings.TrimSpace(raw[1 : len(raw)-1])
		if inner == "" || strings.ContainsAny(inner, "[]") {
			return MediaToken{}, false
		}
		if kind := MediaKindFromName(inner); kind != "" {
			return MediaToken{Kind: kind, Path: inner}, true
		}
	}
	return MediaToken{}, false
}

// MediaContent 把 chat2019 远程文件名还原成前端 content 格式。
func MediaContent(remoteFilename string) string {
	name := strings.TrimSpace(remoteFilename)
	if name == "" {
		return ""
	}
	return "[" + name + "]"
}

// SendMediaPayload 构造发送媒体消息的载荷（content 为对方可访问的 URL）。
//
// 字段与对方前端 sendMessage(filePath, type, mediaId, duration, flash) 完全一致：
// content 就是媒体地址，图片/视频不带 mediaId，语音带 duration，阅后即焚带 flash。
func (p Protocol) SendMediaPayload(targetUID, kind, mediaURL string) map[string]any {
	if strings.TrimSpace(kind) == "" {
		kind = MediaKindImage
	}
	payload := map[string]any{
		"targetUid": targetUID,
		"type":      kind,
		"content":   mediaURL,
		"localTid":  newLocalTid(),
	}
	if kind == MediaKindAudio {
		// 语音时长由前端传入；这里先按 0 占位，对方按 content 播放
		payload["duration"] = 0
	}
	return payload
}

// ReceiveMediaInfo 是从 receive_message 帧里提取的媒体信息。
type ReceiveMediaInfo struct {
	Kind string
	URL  string
	Tid  string
	TS   int
	// FromUID 是发送者在对方站点的 uid（自己发的时等于自己）。
	FromUID string
	// TargetUID 是接收者在对方站点的 uid。
	TargetUID string
	// Flash 表示阅后即焚消息。
	Flash bool
	// ReadByRecipient 表示这条消息对方是否已读（仅自己发出的消息有意义）。
	ReadByRecipient bool
}

// ParseReceiveMedia 判断一帧是否媒体消息，并取出 URL。
func (p Protocol) ParseReceiveMedia(data map[string]any) (ReceiveMediaInfo, bool) {
	kind := strings.ToLower(PickString(data, p.TypeKeys...))
	switch kind {
	case MediaKindImage, MediaKindVideo, MediaKindAudio:
	default:
		return ReceiveMediaInfo{}, false
	}

	url := PickString(data, p.MediaURLKeys...)
	if url == "" {
		url = PickString(data, p.ContentKeys...)
	}
	if url == "" {
		return ReceiveMediaInfo{}, false
	}

	readByRecipient, _ := PickBool(data, "readByRecipient", "readByReceiver")

	return ReceiveMediaInfo{
		Kind:            kind,
		URL:             url,
		Tid:             PickString(data, p.MessageIDKeys...),
		TS:              PickInt(data, p.TimeKeys...),
		FromUID:         PickString(data, p.FromUIDKeys...),
		TargetUID:       PickString(data, p.TargetUIDKeys...),
		ReadByRecipient: readByRecipient,
	}, true
}

// MediaURLKey 用于把对方 URL 归一化成稳定缓存键（去掉查询参数与域名）。
func MediaURLKey(rawURL string) string {
	clean := strings.TrimSpace(rawURL)
	if clean == "" {
		return ""
	}
	if idx := strings.IndexAny(clean, "?#"); idx >= 0 {
		clean = clean[:idx]
	}
	if idx := strings.LastIndex(clean, "/"); idx >= 0 {
		clean = clean[idx+1:]
	}
	return clean
}

// MediaMessage 生成前端可渲染的 code=7 媒体消息。
// remoteFilename 是 chat2019 媒体服务器上的文件名（前端据此拼出完整 URL）。
func (p Protocol) MediaMessage(info ReceiveMediaInfo, remoteFilename string, ctx DownstreamContext, self bool) map[string]any {
	tid := strings.TrimSpace(info.Tid)
	if tid == "" {
		tid = newLocalTid()
	}
	msgTime := formatMessageTime(info.TS)
	selfIDMD5 := md5HexLower(ctx.SelfID)
	content := MediaContent(remoteFilename)

	var fromUser, toUser map[string]any
	if self {
		peerUID := ctx.PartnerUID
		fromUser = map[string]any{
			"id":       selfIDMD5,
			"nickname": ctx.SelfNickname,
			"content":  content,
			"time":     msgTime,
			"Tid":      tid,
		}
		toUser = map[string]any{
			"id":       peerUID,
			"nickname": ctx.PartnerNickname,
		}
	} else {
		peerID := strings.TrimSpace(info.FromUID)
		if peerID == "" {
			peerID = ctx.PartnerUID
		}
		fromUser = map[string]any{
			"id":       peerID,
			"nickname": ctx.PartnerNickname,
			"content":  content,
			"time":     msgTime,
			"Tid":      tid,
		}
		toUser = map[string]any{
			"id":       selfIDMD5,
			"nickname": ctx.SelfNickname,
		}
	}

	out := map[string]any{
		"code":     codePrivateMessage,
		"tid":      tid,
		"type":     info.Kind,
		"fromuser": fromUser,
		"touser":   toUser,
	}
	// 自己发出的媒体同样带「对方是否已读」，前端据此显示「已读 / 未读」
	if self {
		out["readByRecipient"] = info.ReadByRecipient
	}
	return out
}
