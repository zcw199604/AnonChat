/**
 * AI 机器人（角色卡 / Bot 设置）与媒体库的后台接口。
 *
 * 为什么要单独一个文件：这些路径与文案只在管理后台的 Bot 标签页使用。
 * 放在 `features/admin/` 下会随 AdminPage 的懒加载分包一起打包，普通用户加载的主包
 * （`dist/assets/index-*.js`）里不会出现 `/api/admin/bots`、`character-cards`、
 * `media-folders` 等字样，从而不会暴露站点存在 AI 机器人（how.md §0.1）。
 *
 * 通用请求封装在 `lib/api/client.ts`（`apiRequest` / `readResponse`），这里直接复用。
 */
import { apiRequest, readResponse } from '../../lib/api/client'
import type { Gender } from '../../lib/api/client'

// ---- 角色卡 ----

export interface CharacterCard {
  id: string
  name: string
  description: string
  personality: string
  scenario: string
  firstMessage: string
  exampleDialogue: string
  systemPrompt: string
  createdAt: string
  updatedAt: string
}

export interface CharacterCardInput {
  name: string
  description: string
  personality: string
  scenario: string
  firstMessage: string
  exampleDialogue: string
  systemPrompt: string
}

export function adminCharacterCards(): Promise<CharacterCard[]> {
  return apiRequest<CharacterCard[]>('GET', '/api/admin/character-cards')
}

export function adminCreateCharacterCard(body: CharacterCardInput): Promise<CharacterCard> {
  return apiRequest<CharacterCard>('POST', '/api/admin/character-cards', body)
}

export function adminUpdateCharacterCard(id: string, body: CharacterCardInput): Promise<CharacterCard> {
  return apiRequest<CharacterCard>('PUT', `/api/admin/character-cards/${id}`, body)
}

export function adminDeleteCharacterCard(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/admin/character-cards/${id}`)
}

// ---- Bot 设置 ----

/** Bot 对外的身份资料（与真人共用同一套字段）。 */
export interface AdminBotProfile {
  nickname: string
  avatarSeed: string
  publicCode: string
  gender: Gender
  age: number | null
  province: string | null
  provincePublic: boolean
  tags: string[]
  cleanMode: boolean
}

/** Bot 用量统计。 */
export interface AdminBotUsage {
  usedTokens: number
  usedReplies: number
  windowTokens: number
  windowPromptTokens: number
  windowCachedTokens: number
  windowReplies: number
  activeMatches: number
}

export interface AdminBot {
  id: string
  label: string
  cardId: string | null
  mediaFolderId: string | null
  enabled: boolean
  model: string
  temperature: number
  maxOutputTokens: number
  serveMatch: boolean
  serveBottle: boolean
  maxActiveMatches: number
  totalTokenLimit: number
  totalReplyLimit: number
  windowSeconds: number
  windowTokenLimit: number
  windowReplyLimit: number
  convReplyLimit: number
  convMinIntervalSeconds: number
  convMediaLimit: number
  replyDelayMinMs: number
  replyDelayMaxMs: number
  typingMsPerChar: number
  bottleIntervalSeconds: number
  cardName: string
  folderName: string
  profile: AdminBotProfile
  usage: AdminBotUsage
  createdAt: string
  updatedAt: string
}

export interface AdminBotCreate {
  label: string
  nickname: string
  cardId: string | null
  mediaFolderId: string | null
  enabled: boolean
}

export interface AdminBotUpdate {
  label: string
  cardId: string | null
  mediaFolderId: string | null
  enabled: boolean
  model: string
  temperature: number
  maxOutputTokens: number
  serveMatch: boolean
  serveBottle: boolean
  maxActiveMatches: number
  totalTokenLimit: number
  totalReplyLimit: number
  windowSeconds: number
  windowTokenLimit: number
  windowReplyLimit: number
  convReplyLimit: number
  convMinIntervalSeconds: number
  convMediaLimit: number
  replyDelayMinMs: number
  replyDelayMaxMs: number
  typingMsPerChar: number
  bottleIntervalSeconds: number
  profile: {
    nickname: string
    gender: Gender
    age: number | null
    province: string | null
    provincePublic: boolean
    tags: string[]
    cleanMode: boolean
  }
}

export function adminBots(): Promise<AdminBot[]> {
  return apiRequest<AdminBot[]>('GET', '/api/admin/bots')
}

export function adminCreateBot(body: AdminBotCreate): Promise<AdminBot> {
  return apiRequest<AdminBot>('POST', '/api/admin/bots', body)
}

export function adminUpdateBot(id: string, body: AdminBotUpdate): Promise<AdminBot> {
  return apiRequest<AdminBot>('PUT', `/api/admin/bots/${id}`, body)
}

export function adminResetBotUsage(id: string): Promise<AdminBot> {
  return apiRequest<AdminBot>('POST', `/api/admin/bots/${id}/reset-usage`)
}

export function adminDeleteBot(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/admin/bots/${id}`)
}

// ---- 媒体库 ----

export interface MediaFolder {
  id: string
  name: string
  itemCount: number
  createdAt: string
}

export type MediaKind = 'image' | 'video'

export interface MediaItem {
  id: string
  folderId: string
  kind: MediaKind
  mime: string
  sizeBytes: number
  originalName: string
  tags: string[]
  caption: string
  /** 后台预览地址。 */
  fileUrl: string
  createdAt: string
}

export function adminMediaFolders(): Promise<MediaFolder[]> {
  return apiRequest<MediaFolder[]>('GET', '/api/admin/media-folders')
}

export function adminCreateMediaFolder(name: string): Promise<MediaFolder> {
  return apiRequest<MediaFolder>('POST', '/api/admin/media-folders', { name })
}

export function adminRenameMediaFolder(id: string, name: string): Promise<MediaFolder> {
  return apiRequest<MediaFolder>('PATCH', `/api/admin/media-folders/${id}`, { name })
}

export function adminDeleteMediaFolder(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/admin/media-folders/${id}`)
}

export function adminMediaItems(folderId: string): Promise<MediaItem[]> {
  return apiRequest<MediaItem[]>('GET', `/api/admin/media-folders/${folderId}/items`)
}

/**
 * 上传媒体（multipart）：files 可多个，tags 为逗号分隔并应用到这批所有文件。
 * 不手动设置 Content-Type，交给浏览器补 multipart boundary。
 */
export async function adminUploadMedia(
  folderId: string,
  files: File[],
  tags: string,
  caption: string,
): Promise<MediaItem[]> {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  if (tags.trim() !== '') form.append('tags', tags.trim())
  if (caption.trim() !== '') form.append('caption', caption.trim())
  const res = await fetch(`/api/admin/media-folders/${folderId}/items`, {
    method: 'POST',
    credentials: 'same-origin',
    body: form,
  })
  return readResponse<MediaItem[]>(res)
}

export function adminUpdateMediaItem(
  id: string,
  body: { tags: string[]; caption: string },
): Promise<MediaItem> {
  return apiRequest<MediaItem>('PATCH', `/api/admin/media-items/${id}`, body)
}

export function adminDeleteMediaItem(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/admin/media-items/${id}`)
}

// ---- 全局设置（how.md §15.3）----

export interface BotSettings {
  enabled: boolean
  baseUrl: string
  model: string
  timeoutSeconds: number
  sendCacheKey: boolean
  matchFallbackSeconds: number
  bottleMinAgeSeconds: number
  timezone: string
  /** 运行时固定为 user；system 类型仅用于兼容旧数据。 */
  personaMode: PersonaMode
  /** key 只暴露"是否设置""末 4 位""来源"，永远不返回明文或密文。 */
  apiKeySet: boolean
  apiKeyHint: string
  apiKeySource: 'db' | 'env' | 'none'
  /** 已保存的 key 绑定在别的 Base URL 上（当前不生效，需要重新填写）。 */
  apiKeyStale?: boolean
  secretStorageAvailable: boolean
  ready: boolean
  notReadyReason: string
  updatedAt: string
}

export interface BotSettingsInput {
  enabled: boolean
  baseUrl: string
  model: string
  /** 空字符串表示保持原 key 不变。 */
  apiKey?: string
  clearApiKey?: boolean
  timeoutSeconds: number
  sendCacheKey: boolean
  matchFallbackSeconds: number
  bottleMinAgeSeconds: number
  timezone: string
  personaMode: PersonaMode
}

export type PersonaMode = 'user' | 'system'

export interface BotConnectionTest {
  ok: boolean
  latencyMs: number
  model: string
  error: string
}

export function adminBotSettings(): Promise<BotSettings> {
  return apiRequest<BotSettings>('GET', '/api/admin/bot-settings')
}

export function adminSaveBotSettings(body: BotSettingsInput): Promise<BotSettings> {
  return apiRequest<BotSettings>('PUT', '/api/admin/bot-settings', body)
}

/** 测试连接：可用未保存的 baseUrl/model/apiKey 临时覆盖，只发一次极短请求。 */
export function adminTestBotConnection(
  body: { baseUrl?: string; model?: string; apiKey?: string } = {},
): Promise<BotConnectionTest> {
  return apiRequest<BotConnectionTest>('POST', '/api/admin/bot-settings/test', body)
}
