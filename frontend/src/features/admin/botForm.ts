/**
 * Bot 后台表单与接口负载之间的纯函数转换（便于单元测试）。
 * 字段名严格对应后端 `internal/bot/handlers.go` 的 JSON。
 */
import type { Gender } from '../../lib/api/client'
import type {
  AdminBot,
  AdminBotCreate,
  AdminBotUpdate,
  BotSettings,
  BotSettingsInput,
  CharacterCard,
  CharacterCardInput,
  PersonaMode,
} from './botApi'
import { MAX_AGE, MIN_AGE, PROVINCES, TAGS } from '../../lib/profileConsts'

/** 性别选项（与资料页 ProfileCard 保持一致）。 */
export const GENDER_OPTIONS: Array<{ value: Gender; label: string }> = [
  { value: 'male', label: '男' },
  { value: 'female', label: '女' },
  { value: 'secret', label: '保密' },
]

/** 表单里的省份与标签选项直接复用资料常量。 */
export const PROVINCE_OPTIONS = PROVINCES
export const TAG_OPTIONS = TAGS

/**
 * 编辑表单状态：数字一律用字符串保存，避免受控 input 里出现 "0" 与空串的歧义。
 * 提交前由 formToUpdatePayload 统一转成数字。
 */
export interface BotFormState {
  label: string
  cardId: string
  mediaFolderId: string
  enabled: boolean
  model: string
  temperature: string
  maxOutputTokens: string
  serveMatch: boolean
  serveBottle: boolean
  maxActiveMatches: string
  totalTokenLimit: string
  totalReplyLimit: string
  windowSeconds: string
  windowTokenLimit: string
  windowReplyLimit: string
  convReplyLimit: string
  convMinIntervalSeconds: string
  convMediaLimit: string
  replyDelayMinMs: string
  replyDelayMaxMs: string
  typingMsPerChar: string
  bottleIntervalSeconds: string
  // 对外资料
  nickname: string
  gender: Gender
  age: string
  province: string
  provincePublic: boolean
  tags: string[]
  cleanMode: boolean
}

/** num 把字符串安全转成数字（空串/非法值按 0 处理）。 */
export function num(value: string): number {
  const n = Number(value.trim())
  return Number.isFinite(n) ? n : 0
}

/** botToForm 把接口返回的 bot 转成编辑表单。 */
export function botToForm(bot: AdminBot): BotFormState {
  return {
    label: bot.label,
    cardId: bot.cardId ?? '',
    mediaFolderId: bot.mediaFolderId ?? '',
    enabled: bot.enabled,
    model: bot.model,
    temperature: String(bot.temperature),
    maxOutputTokens: String(bot.maxOutputTokens),
    serveMatch: bot.serveMatch,
    serveBottle: bot.serveBottle,
    maxActiveMatches: String(bot.maxActiveMatches),
    totalTokenLimit: String(bot.totalTokenLimit),
    totalReplyLimit: String(bot.totalReplyLimit),
    windowSeconds: String(bot.windowSeconds),
    windowTokenLimit: String(bot.windowTokenLimit),
    windowReplyLimit: String(bot.windowReplyLimit),
    convReplyLimit: String(bot.convReplyLimit),
    convMinIntervalSeconds: String(bot.convMinIntervalSeconds),
    convMediaLimit: String(bot.convMediaLimit),
    replyDelayMinMs: String(bot.replyDelayMinMs),
    replyDelayMaxMs: String(bot.replyDelayMaxMs),
    typingMsPerChar: String(bot.typingMsPerChar),
    bottleIntervalSeconds: String(bot.bottleIntervalSeconds),
    nickname: bot.profile.nickname,
    gender: bot.profile.gender,
    age: bot.profile.age === null ? '' : String(bot.profile.age),
    province: bot.profile.province ?? '',
    provincePublic: bot.profile.provincePublic,
    tags: [...bot.profile.tags],
    cleanMode: bot.profile.cleanMode,
  }
}

/** 新建 bot 的表单（只含新建接口接受的字段）。 */
export interface NewBotFormState {
  label: string
  nickname: string
  cardId: string
  mediaFolderId: string
  enabled: boolean
}

/** emptyNewBotForm 返回新建表单的初始值。 */
export function emptyNewBotForm(): NewBotFormState {
  return { label: '', nickname: '', cardId: '', mediaFolderId: '', enabled: false }
}

/** formToCreatePayload 把新建表单转成接口负载。 */
export function formToCreatePayload(form: NewBotFormState): AdminBotCreate {
  return {
    label: form.label.trim(),
    nickname: form.nickname.trim(),
    cardId: form.cardId === '' ? null : form.cardId,
    mediaFolderId: form.mediaFolderId === '' ? null : form.mediaFolderId,
    enabled: form.enabled,
  }
}

/** formToUpdatePayload 把编辑表单转成全量更新负载。 */
export function formToUpdatePayload(form: BotFormState): AdminBotUpdate {
  return {
    label: form.label.trim(),
    cardId: form.cardId === '' ? null : form.cardId,
    mediaFolderId: form.mediaFolderId === '' ? null : form.mediaFolderId,
    enabled: form.enabled,
    model: form.model.trim(),
    temperature: num(form.temperature),
    maxOutputTokens: num(form.maxOutputTokens),
    serveMatch: form.serveMatch,
    serveBottle: form.serveBottle,
    maxActiveMatches: num(form.maxActiveMatches),
    totalTokenLimit: num(form.totalTokenLimit),
    totalReplyLimit: num(form.totalReplyLimit),
    windowSeconds: num(form.windowSeconds),
    windowTokenLimit: num(form.windowTokenLimit),
    windowReplyLimit: num(form.windowReplyLimit),
    convReplyLimit: num(form.convReplyLimit),
    convMinIntervalSeconds: num(form.convMinIntervalSeconds),
    convMediaLimit: num(form.convMediaLimit),
    replyDelayMinMs: num(form.replyDelayMinMs),
    replyDelayMaxMs: num(form.replyDelayMaxMs),
    typingMsPerChar: num(form.typingMsPerChar),
    bottleIntervalSeconds: num(form.bottleIntervalSeconds),
    profile: {
      nickname: form.nickname.trim(),
      gender: form.gender,
      age: form.age.trim() === '' ? null : num(form.age),
      province: form.province === '' ? null : form.province,
      provincePublic: form.provincePublic,
      tags: form.tags,
      cleanMode: form.cleanMode,
    },
  }
}

/** validateBotForm 返回表单错误文案（空数组表示通过）；约束与后端 CHECK 一致。 */
export function validateBotForm(form: BotFormState): string[] {
  const errors: string[] = []
  if (form.label.trim() === '') errors.push('备注名不能为空')
  if (form.label.trim().length > 40) errors.push('备注名最多 40 字')
  const temperature = num(form.temperature)
  if (temperature < 0 || temperature > 2) errors.push('温度需在 0–2 之间')
  const maxTokens = num(form.maxOutputTokens)
  if (maxTokens < 16 || maxTokens > 4000) errors.push('单次最大输出 token 需在 16–4000 之间')
  const windowSeconds = num(form.windowSeconds)
  if (windowSeconds < 60 || windowSeconds > 604800) errors.push('窗口时长需在 60–604800 秒之间')
  if (num(form.replyDelayMaxMs) < num(form.replyDelayMinMs)) errors.push('最大延迟不能小于最小延迟')
  if (form.age.trim() !== '') {
    const age = num(form.age)
    if (age < MIN_AGE || age > MAX_AGE) errors.push(`年龄需在 ${MIN_AGE}–${MAX_AGE} 之间`)
  }
  if (form.provincePublic && form.province === '') errors.push('先选择省份才能公开')
  if (form.tags.length > 5) errors.push('标签最多 5 个')
  return errors
}

/** formatLimit 把「已用 / 上限」格式化，上限为 0 显示「不限」。 */
export function formatLimit(used: number, limit: number): string {
  return limit > 0 ? `${used} / ${limit}` : `${used} / 不限`
}

/** cacheHitRate 返回缓存命中率（分母为 0 时显示「—」）。 */
export function cacheHitRate(cached: number, prompt: number): string {
  if (prompt <= 0) return '—'
  return `${Math.round((cached / prompt) * 100)}%`
}

/** tagsInputToTags 把逗号分隔的输入（支持全角逗号）拆成去重标签。 */
export function tagsInputToTags(raw: string): string[] {
  const seen = new Set<string>()
  for (const part of raw.split(/[,，、]/)) {
    const tag = part.trim()
    if (tag !== '') seen.add(tag)
  }
  return [...seen]
}

/** tagsToInput 把标签数组转成逗号分隔的输入串。 */
export function tagsToInput(tags: string[]): string {
  return tags.join(',')
}

/** 全局设置表单：数字用字符串保存，瓶龄用「分钟」展示（数据库按秒存）。 */
export interface SettingsFormState {
  enabled: boolean
  baseUrl: string
  model: string
  apiKey: string
  timeoutSeconds: string
  sendCacheKey: boolean
  fallbackSeconds: string
  bottleMinAgeMinutes: string
  timezone: string
  personaMode: PersonaMode
}

/** settingsToForm 把接口返回的全局设置转成表单（apiKey 永远留空，表示不修改）。 */
export function settingsToForm(s: BotSettings): SettingsFormState {
  return {
    enabled: s.enabled,
    baseUrl: s.baseUrl,
    model: s.model,
    apiKey: '',
    timeoutSeconds: String(s.timeoutSeconds),
    sendCacheKey: s.sendCacheKey,
    fallbackSeconds: String(s.matchFallbackSeconds),
    bottleMinAgeMinutes: String(Math.round(s.bottleMinAgeSeconds / 60)),
    timezone: s.timezone,
    personaMode: 'user',
  }
}

/** formToSettingsInput 把设置表单转成保存负载（瓶龄分钟 → 秒；apiKey 为空表示保持不变）。 */
export function formToSettingsInput(form: SettingsFormState, clearApiKey = false): BotSettingsInput {
  const input: BotSettingsInput = {
    enabled: form.enabled,
    baseUrl: form.baseUrl.trim(),
    model: form.model.trim(),
    timeoutSeconds: num(form.timeoutSeconds),
    sendCacheKey: form.sendCacheKey,
    matchFallbackSeconds: num(form.fallbackSeconds),
    bottleMinAgeSeconds: num(form.bottleMinAgeMinutes) * 60,
    timezone: form.timezone.trim(),
    personaMode: 'user',
  }
  const key = form.apiKey.trim()
  if (key !== '') input.apiKey = key
  if (clearApiKey) input.clearApiKey = true
  return input
}

/** normalizeBaseUrl 与后端一致的规范化：去空白与末尾斜杠。 */
export function normalizeBaseUrl(raw: string): string {
  return raw.trim().replace(/\/+$/, '')
}

/** baseUrlChanged 判断表单里的 Base URL 是否与已保存的不同。 */
export function baseUrlChanged(form: SettingsFormState, savedBaseUrl: string): boolean {
  return normalizeBaseUrl(form.baseUrl) !== normalizeBaseUrl(savedBaseUrl)
}

/**
 * needsNewKeyForBaseUrl 判断"改了 Base URL 但没填新 key"这种必须阻止保存的情况。
 * API Key 与 Base URL 绑定（how.md §15.1）：换地址时必须重新填写 key，否则可能把旧 key 带到新地址。
 */
export function needsNewKeyForBaseUrl(
  form: SettingsFormState,
  savedBaseUrl: string,
  clearApiKey = false,
): boolean {
  if (clearApiKey) return false
  if (!baseUrlChanged(form, savedBaseUrl)) return false
  return form.apiKey.trim() === ''
}

/** validateSettingsForm 返回设置表单的错误文案（与后端校验一致）。 */
export function validateSettingsForm(form: SettingsFormState, savedBaseUrl = ''): string[] {
  const errors: string[] = []
  if (needsNewKeyForBaseUrl(form, savedBaseUrl)) {
    errors.push('修改 Base URL 需要重新填写 API Key')
  }
  const base = form.baseUrl.trim()
  if (base !== '' && !/^https?:\/\/[^/\s]+/.test(base)) errors.push('Base URL 必须是 http 或 https 的绝对地址')
  const timeout = num(form.timeoutSeconds)
  if (timeout < 5 || timeout > 300) errors.push('超时需在 5–300 秒之间')
  const fallback = num(form.fallbackSeconds)
  if (fallback < 3 || fallback > 600) errors.push('匹配兜底时间需在 3–600 秒之间')
  const minutes = num(form.bottleMinAgeMinutes)
  if (minutes < 0 || minutes * 60 > 604800) errors.push('漂流瓶最短瓶龄需在 0–10080 分钟之间')
  if (form.timezone.trim() === '') errors.push('时区不能为空')
  return errors
}

// ---- 角色卡 ----

/** 空白角色卡表单。 */
export const EMPTY_CARD_INPUT: CharacterCardInput = {
  name: '',
  description: '',
  personality: '',
  scenario: '',
  firstMessage: '',
  exampleDialogue: '',
  systemPrompt: '',
}

/** 已有角色卡 → 表单（去掉 id / 时间戳）。 */
export function cardToInput(card: CharacterCard): CharacterCardInput {
  return {
    name: card.name,
    description: card.description,
    personality: card.personality,
    scenario: card.scenario,
    firstMessage: card.firstMessage,
    exampleDialogue: card.exampleDialogue,
    systemPrompt: card.systemPrompt,
  }
}

/**
 * 复制角色卡：内容原样带过来，名称加「副本」后缀；
 * 若该名称已存在则依次尝试「副本 2」「副本 3」…，避免列表里出现同名卡。
 */
export function copyCardInput(card: CharacterCard, existingNames: string[] = []): CharacterCardInput {
  const taken = new Set(existingNames)
  const base = `${card.name} 副本`
  let name = base
  for (let i = 2; taken.has(name); i++) name = `${base} ${i}`
  return { ...cardToInput(card), name }
}
