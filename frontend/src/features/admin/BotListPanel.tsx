import { useCallback, useEffect, useState, type ReactNode } from 'react'
import {
  adminBots,
  adminCharacterCards,
  adminCreateBot,
  adminDeleteBot,
  adminMediaFolders,
  adminResetBotUsage,
  adminUpdateBot,
} from './botApi'
import type { AdminBot, CharacterCard, MediaFolder } from './botApi'
import {
  GENDER_OPTIONS,
  PROVINCE_OPTIONS,
  TAG_OPTIONS,
  botToForm,
  cacheHitRate,
  emptyNewBotForm,
  formToCreatePayload,
  formToUpdatePayload,
  formatLimit,
  validateBotForm,
} from './botForm'
import type { BotFormState } from './botForm'
import { useUiStore } from '../../stores/ui'
import { Toggle } from '../../components/Toggle'
import { BotMemoriesDialog } from './BotMemoriesDialog'
import { Button, ConfirmDialog, EmptyState, LoadingRows, Modal, StatusBadge, inputClass } from './ui'

type Confirm = { kind: 'reset' | 'delete'; bot: AdminBot }

/** Bot 列表：卡片网格；新建、编辑都在弹窗里完成。 */
export function BotListPanel({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [bots, setBots] = useState<AdminBot[]>([])
  const [cards, setCards] = useState<CharacterCard[]>([])
  const [folders, setFolders] = useState<MediaFolder[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<AdminBot | null>(null)
  const [memoryBot, setMemoryBot] = useState<AdminBot | null>(null)
  const [confirm, setConfirm] = useState<Confirm | null>(null)
  const [togglingId, setTogglingId] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [b, c, f] = await Promise.all([adminBots(), adminCharacterCards(), adminMediaFolders()])
      setBots(b)
      setCards(c)
      setFolders(f)
    } catch (e) {
      onError(e)
    } finally {
      setLoading(false)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载 bot 与关联数据，状态更新在异步请求之后
    void load()
  }, [load])

  async function toggleEnabled(bot: AdminBot) {
    setTogglingId(bot.id)
    try {
      await adminUpdateBot(bot.id, { ...formToUpdatePayload(botToForm(bot)), enabled: !bot.enabled })
      setToast(bot.enabled ? 'bot 已停用' : 'bot 已启用')
      await load()
    } catch (e) {
      onError(e)
    } finally {
      setTogglingId(null)
    }
  }

  async function runConfirm() {
    if (!confirm) return
    try {
      if (confirm.kind === 'reset') {
        await adminResetBotUsage(confirm.bot.id)
        setToast('用量已清零')
      } else {
        await adminDeleteBot(confirm.bot.id)
        setToast('bot 已删除')
      }
      setConfirm(null)
      void load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-xs text-lobby-sub">
          共 {bots.length} 个 bot · 已启用 {bots.filter((b) => b.enabled).length} 个
        </div>
        <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
          新建 Bot
        </Button>
      </div>

      {loading ? (
        <LoadingRows rows={3} />
      ) : bots.length === 0 ? (
        <EmptyState>还没有 bot，点击右上角「新建 Bot」创建。</EmptyState>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
          {bots.map((bot) => (
            <BotTile
              key={bot.id}
              bot={bot}
              toggling={togglingId === bot.id}
              onToggle={() => void toggleEnabled(bot)}
              onEdit={() => setEditing(bot)}
              onMemories={() => setMemoryBot(bot)}
              onReset={() => setConfirm({ kind: 'reset', bot })}
              onDelete={() => setConfirm({ kind: 'delete', bot })}
            />
          ))}
        </div>
      )}

      {creating && (
        <CreateBotDialog
          cards={cards}
          folders={folders}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            void load()
          }}
          onError={onError}
        />
      )}

      {editing && (
        <EditBotDialog
          key={editing.id}
          bot={editing}
          cards={cards}
          folders={folders}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
          onError={onError}
        />
      )}

      {memoryBot && (
        <BotMemoriesDialog
          key={memoryBot.id}
          bot={memoryBot}
          onClose={() => setMemoryBot(null)}
          onChanged={() => void load()}
          onError={onError}
        />
      )}

      <ConfirmDialog
        open={confirm !== null}
        title={confirm?.kind === 'delete' ? `删除 bot「${confirm.bot.label}」？` : `清零「${confirm?.bot.label ?? ''}」的用量？`}
        description={
          confirm?.kind === 'delete'
            ? '删除后该 bot 的身份会被吊销，进行中的匹配会话会以「对方离开」结束。'
            : '累计 token、回复条数与窗口用量都会归零。'
        }
        confirmText={confirm?.kind === 'delete' ? '删除' : '清零'}
        danger
        onConfirm={runConfirm}
        onClose={() => setConfirm(null)}
      />
    </div>
  )
}

/** 用量条：有上限时画进度，无上限只显示数字。 */
function UsageMeter({ label, used, limit }: { label: string; used: number; limit: number }) {
  const ratio = limit > 0 ? Math.min(1, used / limit) : 0
  return (
    <div className="space-y-1">
      <div className="flex justify-between gap-2 text-[11px]">
        <span className="text-lobby-sub">{label}</span>
        <span className="font-mono text-lobby-ink">{formatLimit(used, limit)}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-lobby-bg">
        {limit > 0 && (
          <div
            className={`h-full rounded-full ${ratio >= 0.9 ? 'bg-danger' : 'bg-brand'}`}
            style={{ width: `${Math.max(ratio * 100, used > 0 ? 2 : 0)}%` }}
          />
        )}
      </div>
    </div>
  )
}

function BotTile({
  bot,
  toggling,
  onToggle,
  onEdit,
  onReset,
  onDelete,
  onMemories,
}: {
  bot: AdminBot
  toggling: boolean
  onToggle: () => void
  onEdit: () => void
  onReset: () => void
  onDelete: () => void
  onMemories: () => void
}) {
  const p = bot.profile
  const profileBits = [
    p.age ? `${p.age} 岁` : '',
    p.province ?? '',
    p.tags.length > 0 ? p.tags.join(' / ') : '',
  ].filter(Boolean)

  return (
    <article
      data-testid="admin-bot"
      className={`flex flex-col rounded-card border border-lobby-line bg-lobby-card p-4 shadow-soft ${bot.enabled ? '' : 'opacity-75'}`}
    >
      <header className="flex items-start gap-3">
        <div
          aria-hidden
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-accent/20 text-base font-semibold text-lobby-ink"
        >
          {(p.nickname || bot.label).slice(0, 1)}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-sm font-semibold text-lobby-ink" title={bot.label}>
              {bot.label}
            </h3>
            <StatusBadge tone={bot.enabled ? 'success' : 'neutral'}>{bot.enabled ? '已启用' : '已停用'}</StatusBadge>
          </div>
          <div className="truncate text-xs text-lobby-sub">
            {p.nickname}
            {p.publicCode && <span className="ml-1 font-mono">#{p.publicCode}</span>}
          </div>
        </div>
        <div className={toggling ? 'pointer-events-none opacity-50' : ''}>
          <Toggle checked={bot.enabled} onChange={onToggle} label={`${bot.label} 启用`} />
        </div>
      </header>

      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-lobby-sub">角色卡</dt>
        <dd className="truncate text-lobby-ink">{bot.cardName || '—'}</dd>
        <dt className="text-lobby-sub">媒体文件夹</dt>
        <dd className="truncate text-lobby-ink">{bot.folderName || '—'}</dd>
        <dt className="text-lobby-sub">驱动模式</dt>
        <dd className="text-lobby-ink">{bot.coreMode === 'dual' ? '双核' : '单核'}</dd>
        <dt className="text-lobby-sub">模型</dt>
        <dd className="truncate font-mono text-lobby-ink">{bot.model || '默认'}</dd>
        {profileBits.length > 0 && (
          <>
            <dt className="text-lobby-sub">对外资料</dt>
            <dd className="truncate text-lobby-ink">{profileBits.join(' · ')}</dd>
          </>
        )}
      </dl>

      <div className="mt-2 flex flex-wrap gap-1">
        {bot.serveMatch && <StatusBadge tone="info">承接匹配</StatusBadge>}
        {bot.serveBottle && <StatusBadge tone="info">承接漂流瓶</StatusBadge>}
      </div>

      <div className="mt-3 grid gap-2 rounded-sm bg-lobby-bg/60 p-3 sm:grid-cols-2">
        <UsageMeter label="总 token" used={bot.usage.usedTokens} limit={bot.totalTokenLimit} />
        <UsageMeter label="总回复条数" used={bot.usage.usedReplies} limit={bot.totalReplyLimit} />
        <UsageMeter label="窗口 token" used={bot.usage.windowTokens} limit={bot.windowTokenLimit} />
        <UsageMeter label="窗口条数" used={bot.usage.windowReplies} limit={bot.windowReplyLimit} />
        <div className="flex justify-between text-[11px]">
          <span className="text-lobby-sub">窗口智核 token</span>
          <span className="font-mono text-lobby-ink">{bot.usage.windowReasonTokens}</span>
        </div>
        <div className="flex justify-between text-[11px]">
          <span className="text-lobby-sub">窗口记忆 token</span>
          <span className="font-mono text-lobby-ink">{bot.usage.windowMemoryTokens}</span>
        </div>
        <div className="flex justify-between text-[11px] sm:col-span-2">
          <span className="text-lobby-sub">记忆条数</span>
          <span className="font-mono text-lobby-ink">{bot.memoryCount}</span>
        </div>
        <div className="flex justify-between text-[11px] sm:col-span-2">
          <span className="text-lobby-sub">
            缓存命中率 <span className="font-mono text-lobby-ink">{cacheHitRate(bot.usage.windowCachedTokens, bot.usage.windowPromptTokens)}</span>
          </span>
          <span className="text-lobby-sub">
            进行中匹配{' '}
            <span className="font-mono text-lobby-ink">
              {bot.usage.activeMatches} / {bot.maxActiveMatches}
            </span>
          </span>
        </div>
      </div>

      <footer className="mt-4 flex flex-wrap justify-end gap-1.5 border-t border-lobby-line pt-3">
        <Button variant="ghost" size="sm" onClick={onMemories}>
          记忆
        </Button>
        <Button variant="ghost" size="sm" onClick={onEdit}>
          编辑
        </Button>
        <Button variant="ghost" size="sm" onClick={onReset}>
          重置用量
        </Button>
        <Button variant="ghost" size="sm" className="text-danger hover:text-danger" onClick={onDelete}>
          删除
        </Button>
      </footer>
    </article>
  )
}

function SelectOptions({ items, emptyLabel }: { items: Array<{ id: string; name: string }>; emptyLabel: string }) {
  return (
    <>
      <option value="">{emptyLabel}</option>
      {items.map((i) => (
        <option key={i.id} value={i.id}>
          {i.name}
        </option>
      ))}
    </>
  )
}

function FieldLabel({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="block text-xs font-medium text-lobby-sub">{label}</span>
      {children}
    </label>
  )
}

/** 单核/双核与记忆配置由新建、编辑表单共用。 */
function CoreFields({ value, onChange }: {
  value: Pick<BotFormState, 'coreMode' | 'reasonModel' | 'memoryEnabled'>
  onChange: (next: Pick<BotFormState, 'coreMode' | 'reasonModel' | 'memoryEnabled'>) => void
}) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <FieldLabel label="驱动模式">
        <select aria-label="驱动模式" className={inputClass} value={value.coreMode}
          onChange={(e) => onChange({ ...value, coreMode: e.target.value as BotFormState['coreMode'] })}>
          <option value="single">单核</option>
          <option value="dual">双核</option>
        </select>
        <span className="block text-[11px] text-lobby-sub">双核每轮至少多一次模型调用</span>
      </FieldLabel>
      <FieldLabel label="智核模型（留空用默认）">
        <input aria-label="智核模型" className={inputClass} value={value.reasonModel}
          onChange={(e) => onChange({ ...value, reasonModel: e.target.value })} />
      </FieldLabel>
      <label className="flex items-center gap-2 text-xs sm:col-span-2">
        <Toggle checked={value.memoryEnabled} onChange={(memoryEnabled) => onChange({ ...value, memoryEnabled })} label="长期记忆" />
        长期记忆
      </label>
    </div>
  )
}

function CreateBotDialog({
  cards,
  folders,
  onClose,
  onCreated,
  onError,
}: {
  cards: CharacterCard[]
  folders: MediaFolder[]
  onClose: () => void
  onCreated: () => void
  onError: (e: unknown) => void
}) {
  const setToast = useUiStore((s) => s.setToast)
  const [form, setForm] = useState(emptyNewBotForm())
  const [busy, setBusy] = useState(false)

  async function create() {
    setBusy(true)
    try {
      await adminCreateBot(formToCreatePayload(form))
      setToast('bot 已创建')
      onCreated()
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      size="md"
      title="新建 Bot"
      description="创建后可在「编辑」里调整模型、限额与对外资料。"
      closeDisabled={busy}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} onClick={() => void create()}>
            创建
          </Button>
        </>
      }
    >
      <div className="grid gap-3 pb-1 sm:grid-cols-2">
        <FieldLabel label="备注名">
          <input
            aria-label="备注名"
            className={inputClass}
            placeholder="仅后台可见"
            value={form.label}
            onChange={(e) => setForm({ ...form, label: e.target.value })}
          />
        </FieldLabel>
        <FieldLabel label="对外昵称">
          <input
            aria-label="对外昵称"
            className={inputClass}
            placeholder="留空自动生成"
            value={form.nickname}
            onChange={(e) => setForm({ ...form, nickname: e.target.value })}
          />
        </FieldLabel>
        <FieldLabel label="角色卡">
          <select
            aria-label="绑定角色卡"
            className={inputClass}
            value={form.cardId}
            onChange={(e) => setForm({ ...form, cardId: e.target.value })}
          >
            <SelectOptions items={cards} emptyLabel="不绑定角色卡" />
          </select>
        </FieldLabel>
        <FieldLabel label="媒体文件夹">
          <select
            aria-label="绑定媒体文件夹"
            className={inputClass}
            value={form.mediaFolderId}
            onChange={(e) => setForm({ ...form, mediaFolderId: e.target.value })}
          >
            <SelectOptions items={folders} emptyLabel="不绑定文件夹" />
          </select>
        </FieldLabel>
        <div className="sm:col-span-2">
          <CoreFields value={form} onChange={(next) => setForm({ ...form, ...next })} />
        </div>
        <div className="flex items-center gap-2 sm:col-span-2">
          <Toggle checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} label="创建后启用" />
          <span className="text-xs text-lobby-sub">创建后启用</span>
        </div>
      </div>
    </Modal>
  )
}

const NUM_FIELDS: Array<{ key: keyof BotFormState; label: string; step?: string }> = [
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

/** bot 全量编辑弹窗（设置 + 限额 + 对外资料）。 */
function EditBotDialog({
  bot,
  cards,
  folders,
  onClose,
  onSaved,
  onError,
}: {
  bot: AdminBot
  cards: CharacterCard[]
  folders: MediaFolder[]
  onClose: () => void
  onSaved: () => void
  onError: (e: unknown) => void
}) {
  const setToast = useUiStore((s) => s.setToast)
  const [form, setForm] = useState<BotFormState>(() => botToForm(bot))
  const [errors, setErrors] = useState<string[]>([])
  const [busy, setBusy] = useState(false)

  async function save() {
    const errs = validateBotForm(form)
    setErrors(errs)
    if (errs.length > 0) return
    setBusy(true)
    try {
      await adminUpdateBot(bot.id, formToUpdatePayload(form))
      setToast('bot 已保存')
      onSaved()
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  function toggleTag(tag: string) {
    const has = form.tags.includes(tag)
    setForm({ ...form, tags: has ? form.tags.filter((t) => t !== tag) : [...form.tags, tag] })
  }

  const sectionTitle = 'text-xs font-semibold text-lobby-ink'

  return (
    <Modal
      open
      size="xl"
      title={`编辑 bot「${bot.label}」`}
      closeDisabled={busy}
      onClose={onClose}
      footer={
        <>
          {errors.length > 0 && <div className="mr-auto self-center text-xs text-danger">{errors.join('；')}</div>}
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} onClick={() => void save()}>
            保存
          </Button>
        </>
      }
    >
      <div className="space-y-5 pb-1">
        <section className="space-y-3">
          <h4 className={sectionTitle}>基础设置</h4>
          <div className="grid gap-3 sm:grid-cols-2">
            <FieldLabel label="备注名">
              <input aria-label="备注名" className={inputClass} value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })} />
            </FieldLabel>
            <FieldLabel label="模型（留空用默认）">
              <input aria-label="模型" className={inputClass} value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })} />
            </FieldLabel>
            <FieldLabel label="角色卡">
              <select aria-label="角色卡" className={inputClass} value={form.cardId} onChange={(e) => setForm({ ...form, cardId: e.target.value })}>
                <SelectOptions items={cards} emptyLabel="不绑定" />
              </select>
            </FieldLabel>
            <FieldLabel label="媒体文件夹">
              <select
                aria-label="媒体文件夹"
                className={inputClass}
                value={form.mediaFolderId}
                onChange={(e) => setForm({ ...form, mediaFolderId: e.target.value })}
              >
                <SelectOptions items={folders} emptyLabel="不绑定" />
              </select>
            </FieldLabel>
          </div>
          <CoreFields value={form} onChange={(next) => setForm({ ...form, ...next })} />
          <div className="flex flex-wrap items-center gap-4 text-xs">
            <label className="flex items-center gap-2">
              <Toggle checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} label="启用" /> 启用
            </label>
            <label className="flex items-center gap-2">
              <Toggle checked={form.serveMatch} onChange={(v) => setForm({ ...form, serveMatch: v })} label="承接匹配" /> 承接匹配
            </label>
            <label className="flex items-center gap-2">
              <Toggle checked={form.serveBottle} onChange={(v) => setForm({ ...form, serveBottle: v })} label="承接漂流瓶" /> 承接漂流瓶
            </label>
          </div>
        </section>

        <section className="space-y-3">
          <h4 className={sectionTitle}>限额与节奏</h4>
          <div className="grid gap-3 sm:grid-cols-3">
            {NUM_FIELDS.map((f) => (
              <FieldLabel key={f.key} label={f.label}>
                <input
                  aria-label={f.label}
                  type="number"
                  step={f.step}
                  className={inputClass}
                  value={form[f.key] as string}
                  onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
                />
              </FieldLabel>
            ))}
          </div>
        </section>

        <section className="space-y-3">
          <h4 className={sectionTitle}>对外资料</h4>
          <p className="text-xs text-lobby-sub">与真人完全一致，用户侧看不出区别。</p>
          <div className="grid gap-3 sm:grid-cols-2">
            <FieldLabel label="昵称">
              <input aria-label="昵称" className={inputClass} value={form.nickname} onChange={(e) => setForm({ ...form, nickname: e.target.value })} />
            </FieldLabel>
            <FieldLabel label="性别">
              <select
                aria-label="性别"
                className={inputClass}
                value={form.gender}
                onChange={(e) => setForm({ ...form, gender: e.target.value as BotFormState['gender'] })}
              >
                {GENDER_OPTIONS.map((g) => (
                  <option key={g.value} value={g.value}>
                    {g.label}
                  </option>
                ))}
              </select>
            </FieldLabel>
            <FieldLabel label="年龄（留空不设置）">
              <input aria-label="年龄" type="number" className={inputClass} value={form.age} onChange={(e) => setForm({ ...form, age: e.target.value })} />
            </FieldLabel>
            <FieldLabel label="省份">
              <select aria-label="省份" className={inputClass} value={form.province} onChange={(e) => setForm({ ...form, province: e.target.value })}>
                <option value="">不设置</option>
                {PROVINCE_OPTIONS.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </FieldLabel>
          </div>
          <div className="flex flex-wrap items-center gap-4 text-xs">
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
        </section>
      </div>
    </Modal>
  )
}
