import { describe, it, expect } from 'vitest'
import type { AdminBot, BotSettings } from './botApi'
import {
  baseUrlChanged,
  botToForm,
  formToSettingsInput,
  needsNewKeyForBaseUrl,
  normalizeBaseUrl,
  settingsToForm,
  validateSettingsForm,
  cacheHitRate,
  emptyNewBotForm,
  formatLimit,
  formToCreatePayload,
  formToUpdatePayload,
  num,
  tagsInputToTags,
  tagsToInput,
  validateBotForm,
} from './botForm'

/** sampleBot 返回一份与后端响应同构的 bot。 */
function sampleBot(): AdminBot {
  return {
    id: 'bot-1',
    label: '客服 bot',
    cardId: 'card-1',
    mediaFolderId: null,
    enabled: true,
    model: 'test-model',
    temperature: 0.9,
    maxOutputTokens: 300,
    serveMatch: true,
    serveBottle: false,
    maxActiveMatches: 5,
    totalTokenLimit: 1000,
    totalReplyLimit: 0,
    windowSeconds: 3600,
    windowTokenLimit: 0,
    windowReplyLimit: 10,
    convReplyLimit: 3,
    convMinIntervalSeconds: 5,
    convMediaLimit: 2,
    replyDelayMinMs: 3000,
    replyDelayMaxMs: 12000,
    typingMsPerChar: 180,
    bottleIntervalSeconds: 1800,
    cardName: '小雅',
    folderName: '',
    profile: {
      nickname: '小助手',
      avatarSeed: 'seed',
      publicCode: 'ABCD123456',
      gender: 'female',
      age: 24,
      province: '浙江',
      provincePublic: true,
      tags: ['音乐'],
      cleanMode: false,
    },
    usage: {
      usedTokens: 120,
      usedReplies: 2,
      windowTokens: 120,
      windowPromptTokens: 100,
      windowCachedTokens: 60,
      windowReplies: 2,
      activeMatches: 1,
    },
    createdAt: '2026-11-06T00:00:00Z',
    updatedAt: '2026-11-06T00:00:00Z',
  }
}

describe('botForm', () => {
  it('botToForm 与 formToUpdatePayload 往返保持一致', () => {
    const bot = sampleBot()
    const form = botToForm(bot)
    expect(form.cardId).toBe('card-1')
    expect(form.mediaFolderId).toBe('')
    expect(form.age).toBe('24')
    expect(form.province).toBe('浙江')
    expect(form.tags).toEqual(['音乐'])

    const payload = formToUpdatePayload(form)
    expect(payload.label).toBe('客服 bot')
    expect(payload.cardId).toBe('card-1')
    expect(payload.mediaFolderId).toBeNull()
    expect(payload.enabled).toBe(true)
    expect(payload.temperature).toBe(0.9)
    expect(payload.maxOutputTokens).toBe(300)
    expect(payload.serveBottle).toBe(false)
    expect(payload.windowSeconds).toBe(3600)
    expect(payload.replyDelayMaxMs).toBe(12000)
    expect(payload.profile).toEqual({
      nickname: '小助手',
      gender: 'female',
      age: 24,
      province: '浙江',
      provincePublic: true,
      tags: ['音乐'],
      cleanMode: false,
    })
  })

  it('formToUpdatePayload 把空年龄/空省份转成 null，数字字符串转成数字', () => {
    const form = botToForm(sampleBot())
    form.age = ''
    form.province = ''
    form.provincePublic = false
    form.temperature = '0.5'
    form.totalTokenLimit = ''
    const payload = formToUpdatePayload(form)
    expect(payload.profile.age).toBeNull()
    expect(payload.profile.province).toBeNull()
    expect(payload.temperature).toBe(0.5)
    expect(payload.totalTokenLimit).toBe(0)
  })

  it('formToCreatePayload 只带新建接口接受的字段', () => {
    const form = { ...emptyNewBotForm(), label: ' 新 bot ', nickname: '小新', cardId: 'c1' }
    expect(formToCreatePayload(form)).toEqual({
      label: '新 bot',
      nickname: '小新',
      cardId: 'c1',
      mediaFolderId: null,
      enabled: false,
    })
  })

  it('validateBotForm 校验备注名、温度、token、窗口、延迟与资料', () => {
    const ok = botToForm(sampleBot())
    expect(validateBotForm(ok)).toEqual([])

    const bad = { ...ok, label: '', temperature: '3', maxOutputTokens: '10', windowSeconds: '30' }
    const errors = validateBotForm(bad)
    expect(errors.some((e) => e.includes('备注名'))).toBe(true)
    expect(errors.some((e) => e.includes('温度'))).toBe(true)
    expect(errors.some((e) => e.includes('token'))).toBe(true)
    expect(errors.some((e) => e.includes('窗口时长'))).toBe(true)

    const badDelay = { ...ok, replyDelayMinMs: '5000', replyDelayMaxMs: '1000' }
    expect(validateBotForm(badDelay).some((e) => e.includes('最大延迟'))).toBe(true)

    const badAge = { ...ok, age: '10' }
    expect(validateBotForm(badAge).some((e) => e.includes('年龄'))).toBe(true)

    const badProvince = { ...ok, province: '', provincePublic: true }
    expect(validateBotForm(badProvince).some((e) => e.includes('省份'))).toBe(true)
  })

  it('num 对空串与非法值返回 0', () => {
    expect(num('')).toBe(0)
    expect(num('abc')).toBe(0)
    expect(num(' 42 ')).toBe(42)
    expect(num('-1')).toBe(-1)
  })

  it('formatLimit 上限为 0 时显示不限', () => {
    expect(formatLimit(120, 1000)).toBe('120 / 1000')
    expect(formatLimit(0, 0)).toBe('0 / 不限')
    expect(formatLimit(5, 0)).toBe('5 / 不限')
  })

  it('cacheHitRate 分母为 0 时显示破折号', () => {
    expect(cacheHitRate(60, 100)).toBe('60%')
    expect(cacheHitRate(0, 0)).toBe('—')
    expect(cacheHitRate(1, 3)).toBe('33%')
  })

  it('tagsInputToTags 支持半角/全角逗号与顿号，并去重', () => {
    expect(tagsInputToTags('自拍, 微笑，自拍、夜景')).toEqual(['自拍', '微笑', '夜景'])
    expect(tagsInputToTags('  ')).toEqual([])
    expect(tagsToInput(['自拍', '微笑'])).toBe('自拍,微笑')
  })
})

/** sampleSettings 返回一份与后端响应同构的全局设置。 */
function sampleSettings(): BotSettings {
  return {
    enabled: true,
    baseUrl: 'https://api.example.com/v1',
    model: 'test-model',
    timeoutSeconds: 60,
    sendCacheKey: true,
    matchFallbackSeconds: 10,
    bottleMinAgeSeconds: 1800,
    timezone: 'Asia/Shanghai',
    personaMode: 'user',
    apiKeySet: true,
    apiKeyHint: '••••abcd',
    apiKeySource: 'db',
    secretStorageAvailable: true,
    ready: true,
    notReadyReason: '',
    updatedAt: '2026-11-07T00:00:00Z',
  }
}

describe('settingsForm', () => {
  it('settingsToForm 把瓶龄秒转成分钟，且 apiKey 留空', () => {
    const form = settingsToForm(sampleSettings())
    expect(form.bottleMinAgeMinutes).toBe('30')
    expect(form.timeoutSeconds).toBe('60')
    expect(form.fallbackSeconds).toBe('10')
    expect(form.apiKey).toBe('')
    expect(form.enabled).toBe(true)
  })

  it('formToSettingsInput 转回负载：分钟 → 秒、空 key 不带 apiKey', () => {
    const form = settingsToForm(sampleSettings())
    form.bottleMinAgeMinutes = '45'
    form.timeoutSeconds = '30'
    const input = formToSettingsInput(form)
    expect(input.bottleMinAgeSeconds).toBe(45 * 60)
    expect(input.timeoutSeconds).toBe(30)
    expect('apiKey' in input).toBe(false)
    expect(input.clearApiKey).toBeUndefined()
  })

  it('旧 system 设置和未知值均使用 user，保存时固定 user', () => {
    const sys = settingsToForm({ ...sampleSettings(), personaMode: 'system' })
    expect(sys.personaMode).toBe('user')
    expect(formToSettingsInput({ ...sys, personaMode: 'system' }).personaMode).toBe('user')
    const weird = settingsToForm({ ...sampleSettings(), personaMode: 'x' as never })
    expect(weird.personaMode).toBe('user')
  })

  it('formToSettingsInput 在填写新 key 或清除时带上对应字段', () => {
    const form = settingsToForm(sampleSettings())
    form.apiKey = ' sk-new-1234 '
    expect(formToSettingsInput(form).apiKey).toBe('sk-new-1234')
    expect(formToSettingsInput(form, true).clearApiKey).toBe(true)
  })

  it('validateSettingsForm 校验 URL、超时、兜底、瓶龄与时区', () => {
    const ok = settingsToForm(sampleSettings())
    expect(validateSettingsForm(ok, ok.baseUrl)).toEqual([])

    const bad = { ...ok, baseUrl: 'ftp://x', timeoutSeconds: '2', fallbackSeconds: '1', bottleMinAgeMinutes: '-5', timezone: ' ' }
    const errors = validateSettingsForm(bad, bad.baseUrl)
    expect(errors.some((e) => e.includes('Base URL'))).toBe(true)
    expect(errors.some((e) => e.includes('超时'))).toBe(true)
    expect(errors.some((e) => e.includes('兜底'))).toBe(true)
    expect(errors.some((e) => e.includes('瓶龄'))).toBe(true)
    expect(errors.some((e) => e.includes('时区'))).toBe(true)

    // Base URL 允许留空（未配置即不可用）；用空地址作为"已保存"值时不触发重填规则。
    expect(validateSettingsForm({ ...ok, baseUrl: '' }, '')).toEqual([])
  })
})

describe('settingsForm 的 Base URL 绑定', () => {
  it('normalizeBaseUrl 去掉首尾空白与末尾斜杠', () => {
    expect(normalizeBaseUrl('  https://a.example/v1/  ')).toBe('https://a.example/v1')
    expect(normalizeBaseUrl('https://a.example/v1///')).toBe('https://a.example/v1')
    expect(normalizeBaseUrl('')).toBe('')
  })

  it('baseUrlChanged 用规范化后的地址比较', () => {
    const form = settingsToForm(sampleSettings())
    expect(baseUrlChanged(form, 'https://api.example.com/v1')).toBe(false)
    expect(baseUrlChanged(form, 'https://api.example.com/v1/')).toBe(false)
    form.baseUrl = 'https://evil.example/v1'
    expect(baseUrlChanged(form, 'https://api.example.com/v1')).toBe(true)
  })

  it('改 Base URL 且没填 key 时需要重填（清除 key 时除外）', () => {
    const form = settingsToForm(sampleSettings())
    const saved = sampleSettings().baseUrl
    expect(needsNewKeyForBaseUrl(form, saved)).toBe(false)

    form.baseUrl = 'https://other.example/v1'
    expect(needsNewKeyForBaseUrl(form, saved)).toBe(true)
    // 填了新 key 就允许。
    form.apiKey = 'sk-new-1234'
    expect(needsNewKeyForBaseUrl(form, saved)).toBe(false)
    // 或者明确清除 key。
    form.apiKey = ''
    expect(needsNewKeyForBaseUrl(form, saved, true)).toBe(false)
  })

  it('validateSettingsForm 在改地址且未填 key 时给出提示', () => {
    const form = settingsToForm(sampleSettings())
    const saved = sampleSettings().baseUrl
    form.baseUrl = 'https://other.example/v1'
    const errors = validateSettingsForm(form, saved)
    expect(errors.some((e) => e.includes('修改 Base URL 需要重新填写 API Key'))).toBe(true)
    // 清除 key 时不报该错误。
    expect(validateSettingsForm(form, form.baseUrl)).toEqual([])
  })
})
