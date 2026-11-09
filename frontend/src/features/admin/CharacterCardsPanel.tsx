import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { adminBots, adminCharacterCards, adminCreateCharacterCard, adminDeleteCharacterCard, adminImportCharacterCard, adminUpdateCharacterCard } from './botApi'
import type { AdminBot, CharacterCard, CharacterCardInput } from './botApi'
import { EMPTY_CARD_INPUT, cardToInput, copyCardInput } from './botForm'
import { useUiStore } from '../../stores/ui'
import { Button, ConfirmDialog, EmptyState, LoadingRows, Modal, StatusBadge, inputClass } from './ui'
import { formatTime } from './labels'
import { CardExpressionsPanel } from './CardExpressionsPanel'

const CARD_FIELDS: Array<{ key: 'name' | 'description' | 'personality' | 'scenario' | 'exampleDialogue' | 'systemPrompt' | 'creatorNotes'; label: string; rows: number; hint?: string }> = [
  { key: 'name', label: '角色名', rows: 1 },
  { key: 'description', label: '身份与外貌', rows: 3 },
  { key: 'personality', label: '性格与说话风格', rows: 3 },
  { key: 'scenario', label: '场景设定', rows: 3 },
  { key: 'exampleDialogue', label: '示例对话', rows: 4 },
  { key: 'systemPrompt', label: '额外系统指令', rows: 3 },
  { key: 'creatorNotes', label: '作者备注', rows: 3 },
]

/** 对话框模式：新建 / 编辑某张卡 / 以某张卡为模板复制。 */
type DialogState =
  | { mode: 'create' }
  | { mode: 'edit'; card: CharacterCard }
  | { mode: 'copy'; card: CharacterCard }

const DIALOG_TITLE: Record<DialogState['mode'], string> = {
  create: '新建角色卡',
  edit: '编辑角色卡',
  copy: '复制角色卡',
}

/** 角色卡：卡片网格展示，新建 / 编辑 / 复制都在弹窗里完成。 */
export function CharacterCardsPanel({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const importInput = useRef<HTMLInputElement>(null)
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<{ name: string; warnings: string[] } | null>(null)
  const [cards, setCards] = useState<CharacterCard[]>([])
  const [bots, setBots] = useState<AdminBot[]>([])
  const [loading, setLoading] = useState(true)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [removeTarget, setRemoveTarget] = useState<CharacterCard | null>(null)

  const load = useCallback(async () => {
    try {
      const [c, b] = await Promise.all([adminCharacterCards(), adminBots()])
      setCards(c)
      setBots(b)
    } catch (e) {
      onError(e)
    } finally {
      setLoading(false)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载角色卡，状态更新在异步请求之后
    void load()
  }, [load])

  /** 每张卡被多少个 bot 绑定，用于卡片角标与删除提示。 */
  const usage = useMemo(() => {
    const m = new Map<string, string[]>()
    for (const b of bots) {
      if (!b.cardId) continue
      m.set(b.cardId, [...(m.get(b.cardId) ?? []), b.label])
    }
    return m
  }, [bots])

  async function submit(input: CharacterCardInput): Promise<boolean> {
    if (!dialog) return false
    try {
      if (dialog.mode === 'edit') {
        await adminUpdateCharacterCard(dialog.card.id, input)
        setToast('角色卡已更新')
      } else {
        await adminCreateCharacterCard(input)
        setToast(dialog.mode === 'copy' ? '角色卡已复制' : '角色卡已创建')
      }
      setDialog(null)
      void load()
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  async function importCard(file: File) {
    setImporting(true)
    try {
      const result = await adminImportCharacterCard(file)
      setImportResult({ name: result.card.name, warnings: result.warnings })
      setToast('角色卡已导入')
      await load()
    } catch (e) {
      onError(e)
    } finally {
      setImporting(false)
      if (importInput.current) importInput.current.value = ''
    }
  }

  async function remove() {
    if (!removeTarget) return
    try {
      await adminDeleteCharacterCard(removeTarget.id)
      setToast('角色卡已删除')
      setRemoveTarget(null)
      void load()
    } catch (e) {
      onError(e)
    }
  }

  const removeUsers = removeTarget ? (usage.get(removeTarget.id) ?? []) : []

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-xs text-lobby-sub">共 {cards.length} 张角色卡</div>
        <div className="flex gap-2">
          <input ref={importInput} className="hidden" type="file" aria-label="导入角色卡文件" accept=".json,.png,.zip,.krp" disabled={importing} onChange={(e) => { const file = e.target.files?.[0]; if (file) void importCard(file) }} />
          <Button size="sm" loading={importing} onClick={() => importInput.current?.click()}>导入角色卡</Button>
          <Button variant="primary" size="sm" onClick={() => setDialog({ mode: 'create' })}>新建角色卡</Button>
        </div>
      </div>

      {loading ? (
        <LoadingRows rows={3} />
      ) : cards.length === 0 ? (
        <EmptyState>还没有角色卡，点击右上角「新建角色卡」创建第一张。</EmptyState>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {cards.map((c) => (
            <CharacterCardTile
              key={c.id}
              card={c}
              usedBy={usage.get(c.id) ?? []}
              onEdit={() => setDialog({ mode: 'edit', card: c })}
              onCopy={() => setDialog({ mode: 'copy', card: c })}
              onRemove={() => setRemoveTarget(c)}
            />
          ))}
        </div>
      )}

      <Modal open={importResult !== null} title="导入结果" size="md" onClose={() => setImportResult(null)} footer={<Button onClick={() => setImportResult(null)}>完成</Button>}>
        <div className="space-y-2 pb-2 text-sm text-lobby-ink">
          <p>已导入「{importResult?.name}」。</p>
          {importResult?.warnings.length ? <ul className="list-disc space-y-1 pl-5">{importResult.warnings.map((warning, i) => <li key={i}>{warning}</li>)}</ul> : <p>没有导入警告。</p>}
        </div>
      </Modal>

      {dialog && (
        <CardFormDialog
          // 切换目标卡时重建表单状态
          key={dialog.mode === 'create' ? 'create' : `${dialog.mode}-${dialog.card.id}`}
          title={DIALOG_TITLE[dialog.mode]}
          description={dialog.mode === 'copy' ? `以「${dialog.card.name}」为模板，保存后生成一张新的角色卡；不复制表情，来源记为手动。` : undefined}
          submitText={dialog.mode === 'edit' ? '保存' : '创建'}
          initial={
            dialog.mode === 'create'
              ? EMPTY_CARD_INPUT
              : dialog.mode === 'edit'
                ? cardToInput(dialog.card)
                : copyCardInput(dialog.card, cards.map((c) => c.name))
          }
          onSubmit={submit}
          savedCardId={dialog.mode === 'edit' ? dialog.card.id : undefined}
          onExpressionsChanged={() => void load()}
          onError={onError}
          onClose={() => setDialog(null)}
        />
      )}

      <ConfirmDialog
        open={removeTarget !== null}
        title={`删除角色卡「${removeTarget?.name ?? ''}」？`}
        description={
          removeUsers.length > 0
            ? `该角色卡正被 ${removeUsers.length} 个 bot 使用（${removeUsers.join('、')}），删除后它们会失去角色卡。`
            : '删除后不可恢复。'
        }
        confirmText="删除"
        danger
        onConfirm={remove}
        onClose={() => setRemoveTarget(null)}
      />
    </div>
  )
}

function CharacterCardTile({
  card,
  usedBy,
  onEdit,
  onCopy,
  onRemove,
}: {
  card: CharacterCard
  usedBy: string[]
  onEdit: () => void
  onCopy: () => void
  onRemove: () => void
}) {
  const filled = [
    card.firstMessage && '开场白',
    card.exampleDialogue && '示例对话',
    card.systemPrompt && '额外指令',
  ].filter(Boolean) as string[]

  return (
    <article
      data-testid="admin-card"
      className="flex flex-col rounded-card border border-lobby-line bg-lobby-card p-4 shadow-soft"
    >
      <header className="flex items-start gap-3">
        <div
          aria-hidden
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-brand/10 text-base font-semibold text-brand"
        >
          {card.name.slice(0, 1) || '?'}
        </div>
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-sm font-semibold text-lobby-ink" title={card.name}>
            {card.name}
          </h3>
          <div className="text-[11px] text-lobby-sub">更新于 {formatTime(card.updatedAt)}</div>
        </div>
        <StatusBadge tone={usedBy.length > 0 ? 'info' : 'neutral'}>
          {usedBy.length > 0 ? `${usedBy.length} 个 bot 在用` : '未使用'}
        </StatusBadge>
      </header>

      <div className="mt-3 flex-1 space-y-1.5 text-xs">
        <p className="line-clamp-2 text-lobby-ink">{card.description || '（未填写身份与外貌）'}</p>
        <p className="line-clamp-2 text-lobby-sub">{card.personality || '（未填写性格）'}</p>
      </div>

      <div className="mt-3 flex gap-1">
        <StatusBadge tone="neutral">{{ manual: '手动', st: 'ST', krp: 'KRP' }[card.sourceFormat] ?? '手动'}</StatusBadge>
        <StatusBadge tone="neutral">{card.expressionCount ?? 0} 个表情</StatusBadge>
      </div>

      {filled.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1">
          {filled.map((f) => (
            <span key={f} className="rounded-full bg-lobby-bg px-2 py-0.5 text-[11px] text-lobby-sub">
              {f}
            </span>
          ))}
        </div>
      )}

      <footer className="mt-4 flex justify-end gap-1.5 border-t border-lobby-line pt-3">
        <Button variant="ghost" size="sm" onClick={onEdit}>
          编辑
        </Button>
        <Button variant="ghost" size="sm" onClick={onCopy}>
          复制
        </Button>
        <Button variant="ghost" size="sm" className="text-danger hover:text-danger" onClick={onRemove}>
          删除
        </Button>
      </footer>
    </article>
  )
}

/** 新建 / 编辑 / 复制共用的角色卡表单弹窗。失败时保持打开，已填内容不丢。 */
function CardFormDialog({ title, description, submitText, initial, savedCardId, onSubmit, onClose, onError, onExpressionsChanged }: {
  title: string
  description?: string
  submitText: string
  initial: CharacterCardInput
  savedCardId?: string
  onSubmit: (input: CharacterCardInput) => Promise<boolean>
  onClose: () => void
  onError: (error: unknown) => void
  onExpressionsChanged: () => void
}) {
  const [form, setForm] = useState<CharacterCardInput>(initial)
  // 关键词输入保留未完成的逗号分隔内容，提交时再转数组。
  const [loreKeys, setLoreKeys] = useState(() => initial.lorebook.map((entry) => entry.keys.join(',')))
  const [busy, setBusy] = useState(false)
  const [expressionBusy, setExpressionBusy] = useState(false)
  const [error, setError] = useState('')
  const blocked = busy || expressionBusy

  function text(key: 'heartPrompt' | 'reasonPrompt' | 'firstMessage' | 'postHistoryInstructions', label: string, rows = 3) {
    return <label className="block space-y-1"><span className="block text-xs font-medium text-lobby-sub">{label}</span>
      <textarea aria-label={label} rows={rows} maxLength={20000} className={inputClass} value={form[key]} onChange={(e) => setForm({ ...form, [key]: e.target.value })} />
    </label>
  }

  async function save() {
    if (form.name.trim() === '') { setError('角色名不能为空'); return }
    setError('')
    setBusy(true)
    try {
      await onSubmit({ ...form, name: form.name.trim(), lorebook: form.lorebook.map((entry, i) => ({ ...entry, keys: loreKeys[i].split(/[,，]/).map((key) => key.trim()).filter(Boolean) })) })
    } finally { setBusy(false) }
  }

  return <Modal open size="lg" title={title} description={description} closeDisabled={blocked} onClose={onClose} footer={<>
    <Button variant="ghost" disabled={blocked} onClick={onClose}>取消</Button>
    <Button variant="primary" loading={busy} disabled={expressionBusy} onClick={() => void save()}>{submitText}</Button>
  </>}>
    <fieldset disabled={blocked} className="space-y-5 pb-2">
      <section className="space-y-3">
        <h3 className="border-b border-lobby-line pb-2 text-sm font-semibold">基础</h3>
        {CARD_FIELDS.map((f) => <label key={f.key} className="block space-y-1">
          <span className="block text-xs font-medium text-lobby-sub">{f.label}</span>
          {f.rows === 1 ? <input aria-label={f.label} maxLength={40} className={inputClass} value={form[f.key]} onChange={(e) => setForm({ ...form, [f.key]: e.target.value })} /> :
            <textarea aria-label={f.label} rows={f.rows} maxLength={20000} className={inputClass} value={form[f.key]} onChange={(e) => setForm({ ...form, [f.key]: e.target.value })} />}
        </label>)}
      </section>
      <section className="space-y-3">
        <h3 className="border-b border-lobby-line pb-2 text-sm font-semibold">双核</h3>
        {text('heartPrompt', '心核人设')}
        {text('reasonPrompt', '智核人设')}
        <p className="text-xs text-lobby-sub">智核人设只在 Bot 设为双核时生效。</p>
      </section>
      <section className="space-y-3">
        <h3 className="border-b border-lobby-line pb-2 text-sm font-semibold">开场白</h3>
        {text('firstMessage', '开场白', 2)}
        <p className="text-xs text-lobby-sub">每次开场会从主开场白和备选项中随机选择一条。</p>
        {form.alternateGreetings.map((greeting, i) => <div key={i} className="space-y-1">
          <label className="block text-xs text-lobby-sub">备选开场白 {i + 1}<textarea aria-label={`备选开场白 ${i + 1}`} rows={2} maxLength={2000} className={inputClass} value={greeting} onChange={(e) => setForm({ ...form, alternateGreetings: form.alternateGreetings.map((g, j) => j === i ? e.target.value : g) })} /></label>
          <Button size="sm" variant="ghost" aria-label={`删除备选开场白 ${i + 1}`} onClick={() => setForm({ ...form, alternateGreetings: form.alternateGreetings.filter((_, j) => j !== i) })}>删除备选开场白</Button>
        </div>)}
        <Button size="sm" disabled={form.alternateGreetings.length >= 20} onClick={() => setForm({ ...form, alternateGreetings: [...form.alternateGreetings, ''] })}>添加备选开场白</Button>
        {text('postHistoryInstructions', '历史后指令')}
      </section>
      <section className="space-y-3">
        <h3 className="border-b border-lobby-line pb-2 text-sm font-semibold">世界书</h3>
        {form.lorebook.map((entry, i) => <div key={i} className="space-y-2 rounded-sm border border-lobby-line p-3">
          <label className="block text-xs text-lobby-sub">关键词（逗号分隔）<input aria-label={`关键词 ${i + 1}`} className={inputClass} value={loreKeys[i]} onChange={(e) => setLoreKeys(loreKeys.map((key, j) => j === i ? e.target.value : key))} /></label>
          <label className="block text-xs text-lobby-sub">内容<textarea aria-label={`内容 ${i + 1}`} rows={3} maxLength={2000} className={inputClass} value={entry.content} onChange={(e) => setForm({ ...form, lorebook: form.lorebook.map((v, j) => j === i ? { ...v, content: e.target.value } : v) })} /></label>
          <label className="block text-xs text-lobby-sub">顺序<input aria-label={`顺序 ${i + 1}`} type="number" step={1} className={inputClass} value={entry.order} onChange={(e) => setForm({ ...form, lorebook: form.lorebook.map((v, j) => j === i ? { ...v, order: Number(e.target.value) } : v) })} /></label>
          <label className="block text-xs text-lobby-sub">备注<input aria-label={`世界书备注 ${i + 1}`} className={inputClass} value={entry.comment} onChange={(e) => setForm({ ...form, lorebook: form.lorebook.map((v, j) => j === i ? { ...v, comment: e.target.value } : v) })} /></label>
          <div className="flex flex-wrap gap-3 text-xs">
            {(['enabled', 'constant', 'case_sensitive'] as const).map((key) => <label key={key} className="flex gap-1"><input type="checkbox" aria-label={`${{ enabled: '启用', constant: '常驻', case_sensitive: '大小写敏感' }[key]} ${i + 1}`} checked={entry[key]} onChange={(e) => setForm({ ...form, lorebook: form.lorebook.map((v, j) => j === i ? { ...v, [key]: e.target.checked } : v) })} />{{ enabled: '启用', constant: '常驻', case_sensitive: '大小写敏感' }[key]}</label>)}
          </div>
          <Button size="sm" variant="ghost" aria-label={`删除世界书条目 ${i + 1}`} onClick={() => {
            setForm({ ...form, lorebook: form.lorebook.filter((_, j) => j !== i) })
            setLoreKeys(loreKeys.filter((_, j) => j !== i))
          }}>删除世界书条目</Button>
        </div>)}
        <Button size="sm" disabled={form.lorebook.length >= 100} onClick={() => {
          setForm({ ...form, lorebook: [...form.lorebook, { keys: [], content: '', enabled: true, constant: false, case_sensitive: false, order: 0, comment: '' }] })
          setLoreKeys([...loreKeys, ''])
        }}>添加世界书条目</Button>
      </section>
      <section className="space-y-3">
        <h3 className="border-b border-lobby-line pb-2 text-sm font-semibold">工具与表情</h3>
        <p className="text-xs text-lobby-sub">全部不勾选 = 全部启用</p>
        <div className="flex flex-wrap gap-3 text-xs">{[['recall_memory', '回忆记忆'], ['get_time', '获取时间'], ['lookup_lore', '查找世界书']].map(([key, label]) => <label key={key} className="flex gap-1">
          <input type="checkbox" aria-label={label} checked={form.tools.includes(key)} onChange={(e) => setForm({ ...form, tools: e.target.checked ? [...form.tools, key] : form.tools.filter((tool) => tool !== key) })} />{label}
        </label>)}</div>
        {!savedCardId && <p className="text-xs text-lobby-sub">保存角色卡后可管理表情。</p>}
      </section>
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
    </fieldset>
    {savedCardId && <div className="pb-2"><CardExpressionsPanel cardId={savedCardId} disabled={busy} onBusy={setExpressionBusy} onError={onError} onChanged={onExpressionsChanged} /></div>}
  </Modal>
}
