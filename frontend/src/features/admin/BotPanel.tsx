import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../../lib/api/client'
import {
  adminBotSettings,
  adminBots,
  adminCharacterCards,
  adminCreateBot,
  adminCreateCharacterCard,
  adminCreateMediaFolder,
  adminDeleteBot,
  adminDeleteCharacterCard,
  adminDeleteMediaFolder,
  adminDeleteMediaItem,
  adminMediaFolders,
  adminMediaItems,
  adminRenameMediaFolder,
  adminResetBotUsage,
  adminSaveBotSettings,
  adminTestBotConnection,
  adminUpdateBot,
  adminUpdateCharacterCard,
  adminUpdateMediaItem,
  adminUploadMedia,
} from './botApi'
import type { AdminBot, BotSettings, CharacterCard, CharacterCardInput, MediaFolder, MediaItem } from './botApi'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { Toggle } from '../../components/Toggle'
import {
  GENDER_OPTIONS,
  PROVINCE_OPTIONS,
  TAG_OPTIONS,
  botToForm,
  cacheHitRate,
  formToSettingsInput,
  needsNewKeyForBaseUrl,
  settingsToForm,
  validateSettingsForm,
  emptyNewBotForm,
  formatLimit,
  formToCreatePayload,
  formToUpdatePayload,
  tagsInputToTags,
  tagsToInput,
  validateBotForm,
} from './botForm'
import type { BotFormState } from './botForm'

/** 错误提示：优先用后端返回的文案，其次查错误码映射。 */
function describeError(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.message && e.message !== '请求失败') return e.message
    return errorMessage(e.code)
  }
  return errorMessage(undefined)
}

const inputCls = 'rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1 text-sm'
const btnCls = 'rounded-sm bg-brand px-3 py-1 text-sm text-white disabled:opacity-40'
const dangerBtnCls = 'rounded-sm bg-danger px-2 py-1 text-xs text-white'
const ghostBtnCls = 'rounded-sm bg-lobby-bg px-2 py-1 text-xs text-lobby-ink'

type SubPanel = 'settings' | 'bots' | 'cards' | 'media'

/** Bot 标签页：角色卡、Bot 设置、媒体库三个子面板。 */
export function BotPanel({ onError }: { onError: (e: unknown) => void }) {
  const [sub, setSub] = useState<SubPanel>('settings')
  return (
    <div className="space-y-3 text-sm">
      <div className="flex flex-wrap gap-2">
        {(
          [
            ['settings', '全局设置'],
            ['bots', 'Bot'],
            ['cards', '角色卡'],
            ['media', '媒体库'],
          ] as Array<[SubPanel, string]>
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            className={`px-3 py-1 rounded-sm text-xs ${sub === key ? 'bg-brand text-white' : 'bg-lobby-bg'}`}
            onClick={() => setSub(key)}
          >
            {label}
          </button>
        ))}
      </div>
      {sub === 'settings' && <SettingsPanel onError={onError} />}
      {sub === 'bots' && <BotsPanel onError={onError} />}
      {sub === 'cards' && <CardsPanel onError={onError} />}
      {sub === 'media' && <MediaPanel onError={onError} />}
    </div>
  )
}

// ---- 全局设置（how.md §15.4）----

const KEY_SOURCE_LABEL: Record<BotSettings['apiKeySource'], string> = {
  db: '数据库',
  env: '环境变量',
  none: '未设置',
}

function SettingsPanel({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [settings, setSettings] = useState<BotSettings | null>(null)
  const [form, setForm] = useState<ReturnType<typeof settingsToForm> | null>(null)
  const [errors, setErrors] = useState<string[]>([])
  const [testResult, setTestResult] = useState<string>('')
  const [testing, setTesting] = useState(false)

  const load = useCallback(async () => {
    try {
      const s = await adminBotSettings()
      setSettings(s)
      setForm(settingsToForm(s))
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载全局设置
    void load()
  }, [load])

  if (!settings || !form) return <p className="text-lobby-sub">加载中…</p>

  // 改了 Base URL 又没填新 key：必须重填（或明确清除 key）才能保存/测试，避免把旧 key 带到新地址。
  const savedBaseUrl = settings.baseUrl
  const needsNewKey = needsNewKeyForBaseUrl(form, savedBaseUrl)

  async function save(clearApiKey = false) {
    if (!form) return
    const errs = validateSettingsForm(form, clearApiKey ? form.baseUrl : savedBaseUrl)
    setErrors(errs)
    if (errs.length > 0) return
    try {
      const saved = await adminSaveBotSettings(formToSettingsInput(form, clearApiKey))
      setSettings(saved)
      setForm(settingsToForm(saved))
      setToast(clearApiKey ? '已清除 API Key' : '全局设置已保存')
    } catch (e) {
      onError(e)
    }
  }

  async function test() {
    if (!form) return
    setTesting(true)
    setTestResult('')
    try {
      const r = await adminTestBotConnection({
        baseUrl: form.baseUrl.trim(),
        model: form.model.trim(),
        apiKey: form.apiKey.trim() || undefined,
      })
      setTestResult(r.ok ? `连接成功（${r.latencyMs} 毫秒，模型 ${r.model}）` : `连接失败：${r.error}`)
    } catch (e) {
      setTestResult(describeError(e))
    } finally {
      setTesting(false)
    }
  }

  const keyPlaceholder = settings.apiKeySet
    ? `已设置 ${settings.apiKeyHint}（来自${KEY_SOURCE_LABEL[settings.apiKeySource]}），留空表示不修改`
    : '输入模型 API Key'

  return (
    <div className="space-y-3">
      <div
        data-testid="bot-settings-status"
        className={`rounded-sm border p-3 text-xs ${settings.ready ? 'border-brand/40 bg-brand/5 text-brand' : 'border-danger/40 bg-danger/5 text-danger'}`}
      >
        {settings.ready ? '当前状态：可用' : `当前状态：不可用（${settings.notReadyReason || '配置不完整'}）`}
        <span className="ml-2 text-lobby-sub">
          API Key：{KEY_SOURCE_LABEL[settings.apiKeySource]}
          {settings.apiKeySet ? `（${settings.apiKeyHint}）` : ''}
        </span>
      </div>

      <div className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-3">
        <div className="flex items-center gap-2">
          <Toggle checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} label="启用 bot 功能" />
          <span className="text-xs text-lobby-sub">启用 bot 功能（总开关）</span>
        </div>

        <div className="grid gap-2 sm:grid-cols-2">
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">Base URL</span>
            <input
              aria-label="Base URL"
              className={`w-full ${inputCls}`}
              placeholder="https://api.openai.com/v1"
              value={form.baseUrl}
              onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">模型</span>
            <input
              aria-label="模型"
              className={`w-full ${inputCls}`}
              placeholder="默认模型，例如 deepseek-chat"
              value={form.model}
              onChange={(e) => setForm({ ...form, model: e.target.value })}
            />
          </label>
          <label className="block space-y-1 sm:col-span-2">
            <span className="text-xs text-lobby-sub">API Key</span>
            <div className="flex gap-2">
              <input
                aria-label="API Key"
                type="password"
                autoComplete="off"
                className={`flex-1 ${inputCls}`}
                placeholder={keyPlaceholder}
                disabled={!settings.secretStorageAvailable}
                value={form.apiKey}
                onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
              />
              {settings.apiKeySet && (
                <button
                  type="button"
                  className={dangerBtnCls}
                  onClick={() => {
                    if (confirm('确定要清除已保存的 API Key 吗？清除后 bot 会停止工作。')) void save(true)
                  }}
                >
                  清除
                </button>
              )}
            </div>
            {settings.apiKeyStale && !needsNewKey && (
              <span className="text-xs text-danger">已保存的 API Key 绑定在旧的 Base URL 上，当前不生效，请重新填写。</span>
            )}
            {needsNewKey && (
              <span className="text-xs text-danger">修改 Base URL 需要重新填写 API Key（或点击「清除」明确删除已保存的 key）。</span>
            )}
            {!settings.secretStorageAvailable && (
              <span className="text-xs text-danger">服务器未配置 SETTINGS_ENC_KEY，无法保存密钥（其余设置仍可保存）。</span>
            )}
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">超时（秒）</span>
            <input
              aria-label="超时秒数"
              type="number"
              className={`w-full ${inputCls}`}
              value={form.timeoutSeconds}
              onChange={(e) => setForm({ ...form, timeoutSeconds: e.target.value })}
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">时区</span>
            <input
              aria-label="时区"
              className={`w-full ${inputCls}`}
              placeholder="Asia/Shanghai"
              value={form.timezone}
              onChange={(e) => setForm({ ...form, timezone: e.target.value })}
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">人设注入方式</span>
            <select
              aria-label="人设注入方式"
              className={`w-full ${inputCls}`}
              value={form.personaMode}
              onChange={(e) => setForm({ ...form, personaMode: e.target.value === 'system' ? 'system' : 'user' })}
            >
              <option value="user">user 消息（兼容中转网关，推荐）</option>
              <option value="system">system 消息（官方接口）</option>
            </select>
            <span className="block text-[11px] text-lobby-sub">
              bot 不认角色卡、自称 AI 助手时，说明网关丢弃了 system 消息，请用 user 方式。
            </span>
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">匹配兜底时间（秒）</span>
            <input
              aria-label="匹配兜底秒数"
              type="number"
              className={`w-full ${inputCls}`}
              value={form.fallbackSeconds}
              onChange={(e) => setForm({ ...form, fallbackSeconds: e.target.value })}
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">漂流瓶最短瓶龄（分钟）</span>
            <input
              aria-label="瓶龄分钟"
              type="number"
              className={`w-full ${inputCls}`}
              value={form.bottleMinAgeMinutes}
              onChange={(e) => setForm({ ...form, bottleMinAgeMinutes: e.target.value })}
            />
          </label>
        </div>

        <div className="flex flex-wrap items-center gap-3 text-xs">
          <label className="flex items-center gap-2">
            <Toggle checked={form.sendCacheKey} onChange={(v) => setForm({ ...form, sendCacheKey: v })} label="发送 prompt_cache_key" />
            发送 prompt_cache_key
          </label>
          <span className="text-lobby-sub">服务商拒绝未知参数时请关闭</span>
        </div>

        {errors.length > 0 && <div className="text-xs text-danger">{errors.join('；')}</div>}
        {testResult && <div className="text-xs text-lobby-sub">{testResult}</div>}
        <div className="flex gap-2">
          <button type="button" className={btnCls} disabled={needsNewKey} onClick={() => void save(false)}>
            保存
          </button>
          <button type="button" className={ghostBtnCls} disabled={testing || needsNewKey} onClick={() => void test()}>
            {testing ? '测试中…' : '测试连接'}
          </button>
        </div>
      </div>
    </div>
  )
}

// ---- 角色卡 ----

const emptyCard: CharacterCardInput = {
  name: '',
  description: '',
  personality: '',
  scenario: '',
  firstMessage: '',
  exampleDialogue: '',
  systemPrompt: '',
}

const cardFields: Array<{ key: keyof CharacterCardInput; label: string; rows: number }> = [
  { key: 'name', label: '角色名', rows: 1 },
  { key: 'description', label: '身份与外貌', rows: 2 },
  { key: 'personality', label: '性格与说话风格', rows: 2 },
  { key: 'scenario', label: '场景设定', rows: 2 },
  { key: 'firstMessage', label: '开场白', rows: 2 },
  { key: 'exampleDialogue', label: '示例对话', rows: 3 },
  { key: 'systemPrompt', label: '额外系统指令', rows: 2 },
]

function CardsPanel({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [cards, setCards] = useState<CharacterCard[]>([])
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState<CharacterCardInput>(emptyCard)

  const load = useCallback(async () => {
    try {
      setCards(await adminCharacterCards())
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载角色卡
    void load()
  }, [load])

  async function submit() {
    if (form.name.trim() === '') {
      setToast('角色名不能为空')
      return
    }
    try {
      if (editing) await adminUpdateCharacterCard(editing, form)
      else await adminCreateCharacterCard(form)
      setToast(editing ? '角色卡已更新' : '角色卡已创建')
      setEditing(null)
      setForm(emptyCard)
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(id: string) {
    if (!confirm('删除角色卡后，绑定它的 bot 会失去角色卡，确定删除吗？')) return
    try {
      await adminDeleteCharacterCard(id)
      setToast('角色卡已删除')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3">
      <div className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-3">
        <div className="text-xs text-lobby-sub">{editing ? '编辑角色卡' : '新建角色卡'}</div>
        {cardFields.map((f) => (
          <label key={f.key} className="block space-y-1">
            <span className="text-xs text-lobby-sub">{f.label}</span>
            {f.rows === 1 ? (
              <input
                aria-label={f.label}
                className={`w-full ${inputCls}`}
                value={form[f.key]}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
            ) : (
              <textarea
                aria-label={f.label}
                rows={f.rows}
                className={`w-full ${inputCls}`}
                value={form[f.key]}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
            )}
          </label>
        ))}
        <div className="flex gap-2">
          <button type="button" className={btnCls} onClick={() => void submit()}>
            {editing ? '保存' : '创建'}
          </button>
          {editing && (
            <button
              type="button"
              className={ghostBtnCls}
              onClick={() => {
                setEditing(null)
                setForm(emptyCard)
              }}
            >
              取消
            </button>
          )}
        </div>
      </div>

      {cards.length === 0 && <p className="text-lobby-sub">暂无角色卡</p>}
      {cards.map((c) => (
        <div
          key={c.id}
          data-testid="admin-card"
          className="flex items-start justify-between gap-3 rounded-sm border border-lobby-line bg-lobby-card p-3"
        >
          <div className="min-w-0">
            <div className="text-lobby-ink">{c.name}</div>
            <div className="truncate text-xs text-lobby-sub">{c.personality || '（未填写性格）'}</div>
          </div>
          <div className="flex shrink-0 gap-2">
            <button
              type="button"
              className={ghostBtnCls}
              onClick={() => {
                setEditing(c.id)
                setForm({
                  name: c.name,
                  description: c.description,
                  personality: c.personality,
                  scenario: c.scenario,
                  firstMessage: c.firstMessage,
                  exampleDialogue: c.exampleDialogue,
                  systemPrompt: c.systemPrompt,
                })
              }}
            >
              编辑
            </button>
            <button type="button" className={dangerBtnCls} onClick={() => void remove(c.id)}>
              删除
            </button>
          </div>
        </div>
      ))}
    </div>
  )
}

// ---- Bot ----

function BotsPanel({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [bots, setBots] = useState<AdminBot[]>([])
  const [cards, setCards] = useState<CharacterCard[]>([])
  const [folders, setFolders] = useState<MediaFolder[]>([])
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState<BotFormState | null>(null)
  const [creating, setCreating] = useState(false)
  const [newForm, setNewForm] = useState(emptyNewBotForm())
  const [errors, setErrors] = useState<string[]>([])

  const load = useCallback(async () => {
    try {
      const [b, c, f] = await Promise.all([adminBots(), adminCharacterCards(), adminMediaFolders()])
      setBots(b)
      setCards(c)
      setFolders(f)
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载 bot 与关联数据
    void load()
  }, [load])

  async function create() {
    try {
      await adminCreateBot(formToCreatePayload(newForm))
      setToast('bot 已创建')
      setCreating(false)
      setNewForm(emptyNewBotForm())
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function save() {
    if (!form || !editing) return
    const errs = validateBotForm(form)
    setErrors(errs)
    if (errs.length > 0) return
    try {
      await adminUpdateBot(editing, formToUpdatePayload(form))
      setToast('bot 已保存')
      setEditing(null)
      setForm(null)
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function toggleEnabled(bot: AdminBot) {
    try {
      await adminUpdateBot(bot.id, {
        ...formToUpdatePayload(botToForm(bot)),
        enabled: !bot.enabled,
      })
      setToast(bot.enabled ? 'bot 已停用' : 'bot 已启用')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function resetUsage(id: string) {
    if (!confirm('确定要把该 bot 的累计用量清零吗？')) return
    try {
      await adminResetBotUsage(id)
      setToast('用量已清零')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(id: string) {
    if (!confirm('删除后该 bot 的身份会被吊销，进行中的匹配会话会以「对方离开」结束。确定删除吗？')) return
    try {
      await adminDeleteBot(id)
      setToast('bot 已删除')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3">
      <div className="rounded-sm border border-lobby-line bg-lobby-card p-3 space-y-2">
        <div className="flex items-center justify-between">
          <div className="text-xs text-lobby-sub">{creating ? '新建 bot' : '新建 bot（角色卡可在创建后绑定）'}</div>
          <button type="button" className={ghostBtnCls} onClick={() => setCreating((v) => !v)}>
            {creating ? '收起' : '新建'}
          </button>
        </div>
        {creating && (
          <div className="grid gap-2 sm:grid-cols-2">
            <input
              aria-label="备注名"
              className={inputCls}
              placeholder="备注名（仅后台可见）"
              value={newForm.label}
              onChange={(e) => setNewForm({ ...newForm, label: e.target.value })}
            />
            <input
              aria-label="对外昵称"
              className={inputCls}
              placeholder="对外昵称（留空自动生成）"
              value={newForm.nickname}
              onChange={(e) => setNewForm({ ...newForm, nickname: e.target.value })}
            />
            <select
              aria-label="绑定角色卡"
              className={inputCls}
              value={newForm.cardId}
              onChange={(e) => setNewForm({ ...newForm, cardId: e.target.value })}
            >
              <option value="">不绑定角色卡</option>
              {cards.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <select
              aria-label="绑定媒体文件夹"
              className={inputCls}
              value={newForm.mediaFolderId}
              onChange={(e) => setNewForm({ ...newForm, mediaFolderId: e.target.value })}
            >
              <option value="">不绑定文件夹</option>
              {folders.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name}
                </option>
              ))}
            </select>
            <div className="flex items-center gap-2">
              <Toggle checked={newForm.enabled} onChange={(v) => setNewForm({ ...newForm, enabled: v })} label="创建后启用" />
              <span className="text-xs text-lobby-sub">创建后启用</span>
            </div>
            <button type="button" className={btnCls} onClick={() => void create()}>
              创建
            </button>
          </div>
        )}
      </div>

      {bots.length === 0 && <p className="text-lobby-sub">暂无 bot</p>}
      {bots.map((bot) => (
        <div key={bot.id} data-testid="admin-bot" className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-lobby-ink">{bot.label}</span>
                <span className={`text-xs ${bot.enabled ? 'text-brand' : 'text-lobby-sub'}`}>
                  {bot.enabled ? '已启用' : '已停用'}
                </span>
                <span className="text-xs text-lobby-sub">角色卡：{bot.cardName || '—'}</span>
                <span className="text-xs text-lobby-sub">文件夹：{bot.folderName || '—'}</span>
              </div>
              <div className="text-xs text-lobby-sub">
                对外资料：{bot.profile.nickname}
                {bot.profile.publicCode ? ` · 编号 ${bot.profile.publicCode}` : ''}
                {bot.profile.age ? ` · ${bot.profile.age} 岁` : ''}
                {bot.profile.province ? ` · ${bot.profile.province}` : ''}
                {bot.profile.tags.length > 0 ? ` · ${bot.profile.tags.join('/')}` : ''}
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              <Toggle checked={bot.enabled} onChange={() => void toggleEnabled(bot)} label={`${bot.label} 启用`} />
              <button
                type="button"
                className={ghostBtnCls}
                onClick={() => {
                  setEditing(bot.id)
                  setForm(botToForm(bot))
                  setErrors([])
                }}
              >
                编辑
              </button>
              <button type="button" className={ghostBtnCls} onClick={() => void resetUsage(bot.id)}>
                重置用量
              </button>
              <button type="button" className={dangerBtnCls} onClick={() => void remove(bot.id)}>
                删除
              </button>
            </div>
          </div>

          <div className="grid gap-1 text-xs text-lobby-sub sm:grid-cols-2 lg:grid-cols-4">
            <div>token（总量）：{formatLimit(bot.usage.usedTokens, bot.totalTokenLimit)}</div>
            <div>回复条数（总量）：{formatLimit(bot.usage.usedReplies, bot.totalReplyLimit)}</div>
            <div>
              窗口 token：{formatLimit(bot.usage.windowTokens, bot.windowTokenLimit)} · 窗口条数：
              {formatLimit(bot.usage.windowReplies, bot.windowReplyLimit)}
            </div>
            <div>
              缓存命中率：{cacheHitRate(bot.usage.windowCachedTokens, bot.usage.windowPromptTokens)} · 进行中匹配：
              {bot.usage.activeMatches} / {bot.maxActiveMatches}
            </div>
          </div>

          {editing === bot.id && form && (
            <BotEditForm form={form} setForm={setForm} cards={cards} folders={folders} errors={errors} onSave={save} onCancel={() => {
              setEditing(null)
              setForm(null)
            }} />
          )}
        </div>
      ))}
    </div>
  )
}

const numFields: Array<{ key: keyof BotFormState; label: string; step?: string }> = [
  { key: 'temperature', label: '温度（0–2）', step: '0.1' },
  { key: 'maxOutputTokens', label: '单次最大输出 token' },
  { key: 'maxActiveMatches', label: '最大并发匹配' },
  { key: 'totalTokenLimit', label: '总 token 上限（0 不限）' },
  { key: 'totalReplyLimit', label: '总回复条数上限（0 不限）' },
  { key: 'windowSeconds', label: '窗口秒数' },
  { key: 'windowTokenLimit', label: '窗口 token 上限（0 不限）' },
  { key: 'windowReplyLimit', label: '窗口回复条数上限（0 不限）' },
  { key: 'convReplyLimit', label: '单对话回复条数上限（0 不限）' },
  { key: 'convMinIntervalSeconds', label: '单对话最小间隔（秒）' },
  { key: 'convMediaLimit', label: '单对话媒体上限' },
  { key: 'replyDelayMinMs', label: '读消息最小延迟（毫秒）' },
  { key: 'replyDelayMaxMs', label: '读消息最大延迟（毫秒）' },
  { key: 'typingMsPerChar', label: '每字打字时长（毫秒）' },
  { key: 'bottleIntervalSeconds', label: '回瓶间隔（秒）' },
]

/** BotEditForm 是 bot 的全量编辑表单（设置 + 对外资料）。 */
function BotEditForm({
  form,
  setForm,
  cards,
  folders,
  errors,
  onSave,
  onCancel,
}: {
  form: BotFormState
  setForm: (f: BotFormState) => void
  cards: CharacterCard[]
  folders: MediaFolder[]
  errors: string[]
  onSave: () => void
  onCancel: () => void
}) {
  function toggleTag(tag: string) {
    const has = form.tags.includes(tag)
    setForm({ ...form, tags: has ? form.tags.filter((t) => t !== tag) : [...form.tags, tag] })
  }

  return (
    <div className="space-y-3 rounded-sm border border-lobby-line bg-lobby-bg/40 p-3">
      <div className="grid gap-2 sm:grid-cols-2">
        <label className="block space-y-1">
          <span className="text-xs text-lobby-sub">备注名</span>
          <input aria-label="备注名" className={`w-full ${inputCls}`} value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })} />
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-lobby-sub">模型（留空用默认）</span>
          <input aria-label="模型" className={`w-full ${inputCls}`} value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })} />
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-lobby-sub">角色卡</span>
          <select aria-label="角色卡" className={`w-full ${inputCls}`} value={form.cardId} onChange={(e) => setForm({ ...form, cardId: e.target.value })}>
            <option value="">不绑定</option>
            {cards.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-lobby-sub">媒体文件夹</span>
          <select aria-label="媒体文件夹" className={`w-full ${inputCls}`} value={form.mediaFolderId} onChange={(e) => setForm({ ...form, mediaFolderId: e.target.value })}>
            <option value="">不绑定</option>
            {folders.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}
              </option>
            ))}
          </select>
        </label>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-xs">
          <Toggle checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} label="启用" /> 启用
        </label>
        <label className="flex items-center gap-2 text-xs">
          <Toggle checked={form.serveMatch} onChange={(v) => setForm({ ...form, serveMatch: v })} label="承接匹配" /> 承接匹配
        </label>
        <label className="flex items-center gap-2 text-xs">
          <Toggle checked={form.serveBottle} onChange={(v) => setForm({ ...form, serveBottle: v })} label="承接漂流瓶" /> 承接漂流瓶
        </label>
      </div>

      <div className="grid gap-2 sm:grid-cols-3">
        {numFields.map((f) => (
          <label key={f.key} className="block space-y-1">
            <span className="text-xs text-lobby-sub">{f.label}</span>
            <input
              aria-label={f.label}
              type="number"
              step={f.step}
              className={`w-full ${inputCls}`}
              value={form[f.key] as string}
              onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
            />
          </label>
        ))}
      </div>

      <div className="space-y-2 rounded-sm border border-lobby-line p-2">
        <div className="text-xs text-lobby-sub">对外资料（与真人完全一致，用户侧看不出区别）</div>
        <div className="grid gap-2 sm:grid-cols-2">
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">昵称</span>
            <input aria-label="昵称" className={`w-full ${inputCls}`} value={form.nickname} onChange={(e) => setForm({ ...form, nickname: e.target.value })} />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">性别</span>
            <select aria-label="性别" className={`w-full ${inputCls}`} value={form.gender} onChange={(e) => setForm({ ...form, gender: e.target.value as BotFormState['gender'] })}>
              {GENDER_OPTIONS.map((g) => (
                <option key={g.value} value={g.value}>
                  {g.label}
                </option>
              ))}
            </select>
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">年龄（留空不设置）</span>
            <input aria-label="年龄" type="number" className={`w-full ${inputCls}`} value={form.age} onChange={(e) => setForm({ ...form, age: e.target.value })} />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">省份</span>
            <select aria-label="省份" className={`w-full ${inputCls}`} value={form.province} onChange={(e) => setForm({ ...form, province: e.target.value })}>
              <option value="">不设置</option>
              {PROVINCE_OPTIONS.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-3 text-xs">
          <label className="flex items-center gap-2">
            <Toggle checked={form.provincePublic} onChange={(v) => setForm({ ...form, provincePublic: v })} label="公开省份" /> 公开省份
          </label>
          <label className="flex items-center gap-2">
            <Toggle checked={form.cleanMode} onChange={(v) => setForm({ ...form, cleanMode: v })} label="清流模式" /> 清流模式
          </label>
        </div>
        <div className="flex flex-wrap gap-1.5" aria-label="资料标签">
          {TAG_OPTIONS.map((tag) => (
            <button
              key={tag}
              type="button"
              aria-pressed={form.tags.includes(tag)}
              className={`rounded-sm px-2 py-0.5 text-xs ${form.tags.includes(tag) ? 'bg-brand/15 text-brand' : 'bg-lobby-bg text-lobby-sub'}`}
              onClick={() => toggleTag(tag)}
            >
              {tag}
            </button>
          ))}
        </div>
      </div>

      {errors.length > 0 && <div className="text-xs text-danger">{errors.join('；')}</div>}
      <div className="flex gap-2">
        <button type="button" className={btnCls} onClick={onSave}>
          保存
        </button>
        <button type="button" className={ghostBtnCls} onClick={onCancel}>
          取消
        </button>
      </div>
    </div>
  )
}

// ---- 媒体库 ----

function MediaPanel({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [folders, setFolders] = useState<MediaFolder[]>([])
  const [folderId, setFolderId] = useState<string>('')
  const [items, setItems] = useState<MediaItem[]>([])
  const [newName, setNewName] = useState('')
  const [rename, setRename] = useState('')
  const [tags, setTags] = useState('')
  const [caption, setCaption] = useState('')
  const [uploadErr, setUploadErr] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const loadFolders = useCallback(async () => {
    try {
      const list = await adminMediaFolders()
      setFolders(list)
      setFolderId((cur) => (cur === '' && list.length > 0 ? list[0].id : cur))
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载文件夹
    void loadFolders()
  }, [loadFolders])

  const loadItems = useCallback(
    async (id: string) => {
      if (id === '') {
        setItems([])
        return
      }
      try {
        setItems(await adminMediaItems(id))
      } catch (e) {
        onError(e)
      }
    },
    [onError],
  )

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换文件夹时加载媒体
    void loadItems(folderId)
  }, [folderId, loadItems])

  const current = useMemo(() => folders.find((f) => f.id === folderId), [folders, folderId])

  async function createFolder() {
    if (newName.trim() === '') return
    try {
      const folder = await adminCreateMediaFolder(newName.trim())
      setNewName('')
      setToast('文件夹已创建')
      await loadFolders()
      setFolderId(folder.id)
    } catch (e) {
      onError(e)
    }
  }

  async function renameFolder() {
    if (!current || rename.trim() === '') return
    try {
      await adminRenameMediaFolder(current.id, rename.trim())
      setRename('')
      setToast('文件夹已重命名')
      void loadFolders()
    } catch (e) {
      onError(e)
    }
  }

  async function removeFolder() {
    if (!current) return
    if (!confirm('删除文件夹会同时删除其中的全部媒体文件，确定删除吗？')) return
    try {
      await adminDeleteMediaFolder(current.id)
      setToast('文件夹已删除')
      setFolderId('')
      await loadFolders()
    } catch (e) {
      onError(e)
    }
  }

  async function upload() {
    const input = fileRef.current
    const files = input?.files ? Array.from(input.files) : []
    if (folderId === '' || files.length === 0) {
      setUploadErr('请选择文件夹和文件')
      return
    }
    try {
      const created = await adminUploadMedia(folderId, files, tags, caption)
      setUploadErr('')
      setToast(`已上传 ${created.length} 个文件`)
      if (input) input.value = ''
      await loadItems(folderId)
      void loadFolders()
    } catch (e) {
      // 上传失败时优先展示后端文案（media_rejected 等）
      setUploadErr(describeError(e))
    }
  }

  async function saveTags(item: MediaItem, nextTags: string, nextCaption: string) {
    try {
      await adminUpdateMediaItem(item.id, {
        tags: tagsInputToTags(nextTags),
        caption: nextCaption.trim(),
      })
      setToast('标签已保存')
      await loadItems(folderId)
    } catch (e) {
      onError(e)
    }
  }

  async function removeItem(id: string) {
    if (!confirm('确定删除这个媒体文件吗？引用它的消息会失去图片/视频。')) return
    try {
      await adminDeleteMediaItem(id)
      setToast('媒体已删除')
      await loadItems(folderId)
      void loadFolders()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end gap-2 rounded-sm border border-lobby-line bg-lobby-card p-3">
        <label className="space-y-1">
          <span className="block text-xs text-lobby-sub">文件夹</span>
          <select aria-label="媒体文件夹选择" className={inputCls} value={folderId} onChange={(e) => setFolderId(e.target.value)}>
            <option value="">（未选择）</option>
            {folders.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}（{f.itemCount}）
              </option>
            ))}
          </select>
        </label>
        <input aria-label="新文件夹名" className={inputCls} placeholder="新文件夹名" value={newName} onChange={(e) => setNewName(e.target.value)} />
        <button type="button" className={btnCls} onClick={() => void createFolder()}>
          新建文件夹
        </button>
        {current && (
          <>
            <input aria-label="重命名文件夹" className={inputCls} placeholder="重命名" value={rename} onChange={(e) => setRename(e.target.value)} />
            <button type="button" className={ghostBtnCls} onClick={() => void renameFolder()}>
              重命名
            </button>
            <button type="button" className={dangerBtnCls} onClick={() => void removeFolder()}>
              删除文件夹
            </button>
          </>
        )}
      </div>

      {current && (
        <div className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-3">
          <div className="text-xs text-lobby-sub">上传到「{current.name}」：可多选文件，标签会应用到这批文件</div>
          <div className="flex flex-wrap items-end gap-2">
            <input ref={fileRef} aria-label="选择媒体文件" type="file" multiple accept="image/*,video/*" className="text-xs" />
            <input aria-label="上传标签" className={inputCls} placeholder="标签（逗号分隔）" value={tags} onChange={(e) => setTags(e.target.value)} />
            <input aria-label="上传描述" className={inputCls} placeholder="描述（给模型看）" value={caption} onChange={(e) => setCaption(e.target.value)} />
            <button type="button" className={btnCls} onClick={() => void upload()}>
              上传
            </button>
          </div>
          {uploadErr && <div className="text-xs text-danger">{uploadErr}</div>}
        </div>
      )}

      {folderId !== '' && items.length === 0 && <p className="text-lobby-sub">该文件夹还没有媒体</p>}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {items.map((item) => (
          <MediaCard key={item.id} item={item} onSave={saveTags} onDelete={removeItem} />
        ))}
      </div>
    </div>
  )
}

/** MediaCard 是媒体网格里的一项：预览 + 标签/描述编辑 + 删除。 */
function MediaCard({
  item,
  onSave,
  onDelete,
}: {
  item: MediaItem
  onSave: (item: MediaItem, tags: string, caption: string) => Promise<void>
  onDelete: (id: string) => Promise<void>
}) {
  const [tags, setTags] = useState(tagsToInput(item.tags))
  const [caption, setCaption] = useState(item.caption)

  return (
    <div data-testid="media-item" className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-2">
      <div className="flex h-32 items-center justify-center overflow-hidden rounded-sm bg-lobby-bg">
        {item.kind === 'video' ? (
          <video src={item.fileUrl} preload="metadata" playsInline className="max-h-32" />
        ) : (
          <img src={item.fileUrl} alt={item.originalName || '媒体'} loading="lazy" className="max-h-32" />
        )}
      </div>
      <div className="truncate text-xs text-lobby-sub" title={item.originalName}>
        {item.kind === 'video' ? '视频' : '图片'} · {item.originalName || item.id}
      </div>
      <input aria-label="媒体标签" className={`w-full ${inputCls}`} value={tags} onChange={(e) => setTags(e.target.value)} placeholder="标签（逗号分隔）" />
      <input aria-label="媒体描述" className={`w-full ${inputCls}`} value={caption} onChange={(e) => setCaption(e.target.value)} placeholder="描述（给模型看）" />
      <div className="flex gap-2">
        <button type="button" className={ghostBtnCls} onClick={() => void onSave(item, tags, caption)}>
          保存
        </button>
        <button type="button" className={dangerBtnCls} onClick={() => void onDelete(item.id)}>
          删除
        </button>
      </div>
    </div>
  )
}
