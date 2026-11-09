import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../../lib/api/client'
import {
  adminBotSettings,
  adminCreateMediaFolder,
  adminDeleteMediaFolder,
  adminDeleteMediaItem,
  adminMediaFolders,
  adminMediaItems,
  adminRenameMediaFolder,
  adminSaveBotSettings,
  adminTestBotConnection,
  adminUpdateMediaItem,
  adminUploadMedia,
} from './botApi'
import type { BotSettings, MediaFolder, MediaItem } from './botApi'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { Toggle } from '../../components/Toggle'
import { Segmented } from '../../components/Segmented'
import { BotListPanel } from './BotListPanel'
import { CharacterCardsPanel } from './CharacterCardsPanel'
import {
  formToSettingsInput,
  needsNewKeyForBaseUrl,
  settingsToForm,
  validateSettingsForm,
  tagsInputToTags,
  tagsToInput,
} from './botForm'

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
      <Segmented<SubPanel>
        ariaLabel="Bot 配置分区"
        value={sub}
        onChange={setSub}
        options={[
          { value: 'settings', label: '全局设置' },
          { value: 'bots', label: 'Bot' },
          { value: 'cards', label: '角色卡' },
          { value: 'media', label: '媒体库' },
        ]}
      />
      {sub === 'settings' && <SettingsPanel onError={onError} />}
      {sub === 'bots' && <BotListPanel onError={onError} />}
      {sub === 'cards' && <CharacterCardsPanel onError={onError} />}
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
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">智核默认模型</span>
            <input aria-label="智核默认模型" className={`w-full ${inputCls}`} placeholder="留空使用心核模型"
              value={form.reasonModel} onChange={(e) => setForm({ ...form, reasonModel: e.target.value })} />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">记忆模型</span>
            <input aria-label="记忆模型" className={`w-full ${inputCls}`} placeholder="留空使用默认模型"
              value={form.memoryModel} onChange={(e) => setForm({ ...form, memoryModel: e.target.value })} />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">记忆保留天数（1–365）</span>
            <input aria-label="记忆保留天数" type="number" min="1" max="365" step="1" className={`w-full ${inputCls}`}
              value={form.memoryRetentionDays} onChange={(e) => setForm({ ...form, memoryRetentionDays: e.target.value })} />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-lobby-sub">记忆抽取间隔（5–200 条）</span>
            <input aria-label="记忆抽取间隔" type="number" min="5" max="200" step="1" className={`w-full ${inputCls}`}
              value={form.memoryExtractEvery} onChange={(e) => setForm({ ...form, memoryExtractEvery: e.target.value })} />
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
          <div className="block space-y-1">
            <span className="text-xs text-lobby-sub">人设注入方式</span>
            <p className="text-sm text-lobby-text">user 消息</p>
            <span className="block text-[11px] text-lobby-sub">
              角色设定和额外指令会随每轮对话一起发送。
            </span>
          </div>
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
          <label className="flex items-center gap-2">
            <Toggle checked={form.oocGuard} onChange={(v) => setForm({ ...form, oocGuard: v })} label="跑偏检测" />
            跑偏检测
          </label>
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
