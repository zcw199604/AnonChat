import { useCallback, useEffect, useMemo, useState } from 'react'
import { adminBots, adminCharacterCards, adminCreateCharacterCard, adminDeleteCharacterCard, adminUpdateCharacterCard } from './botApi'
import type { AdminBot, CharacterCard, CharacterCardInput } from './botApi'
import { EMPTY_CARD_INPUT, cardToInput, copyCardInput } from './botForm'
import { useUiStore } from '../../stores/ui'
import { Button, ConfirmDialog, EmptyState, LoadingRows, Modal, StatusBadge, inputClass } from './ui'
import { formatTime } from './labels'

const CARD_FIELDS: Array<{ key: keyof CharacterCardInput; label: string; rows: number; hint?: string }> = [
  { key: 'name', label: '角色名', rows: 1 },
  { key: 'description', label: '身份与外貌', rows: 3 },
  { key: 'personality', label: '性格与说话风格', rows: 3 },
  { key: 'scenario', label: '场景设定', rows: 3 },
  { key: 'firstMessage', label: '开场白', rows: 2 },
  { key: 'exampleDialogue', label: '示例对话', rows: 4 },
  { key: 'systemPrompt', label: '额外系统指令', rows: 3 },
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
        <Button variant="primary" size="sm" onClick={() => setDialog({ mode: 'create' })}>
          新建角色卡
        </Button>
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

      {dialog && (
        <CardFormDialog
          // 切换目标卡时重建表单状态
          key={dialog.mode === 'create' ? 'create' : `${dialog.mode}-${dialog.card.id}`}
          title={DIALOG_TITLE[dialog.mode]}
          description={dialog.mode === 'copy' ? `以「${dialog.card.name}」为模板，保存后生成一张新的角色卡。` : undefined}
          submitText={dialog.mode === 'edit' ? '保存' : '创建'}
          initial={
            dialog.mode === 'create'
              ? EMPTY_CARD_INPUT
              : dialog.mode === 'edit'
                ? cardToInput(dialog.card)
                : copyCardInput(dialog.card, cards.map((c) => c.name))
          }
          onSubmit={submit}
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
function CardFormDialog({
  title,
  description,
  submitText,
  initial,
  onSubmit,
  onClose,
}: {
  title: string
  description?: string
  submitText: string
  initial: CharacterCardInput
  onSubmit: (input: CharacterCardInput) => Promise<boolean>
  onClose: () => void
}) {
  const [form, setForm] = useState<CharacterCardInput>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function save() {
    if (form.name.trim() === '') {
      setError('角色名不能为空')
      return
    }
    setError('')
    setBusy(true)
    try {
      await onSubmit({ ...form, name: form.name.trim() })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      size="lg"
      title={title}
      description={description}
      closeDisabled={busy}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} onClick={() => void save()}>
            {submitText}
          </Button>
        </>
      }
    >
      <div className="space-y-3 pb-1">
        {CARD_FIELDS.map((f) => (
          <label key={f.key} className="block space-y-1">
            <span className="block text-xs font-medium text-lobby-sub">{f.label}</span>
            {f.rows === 1 ? (
              <input
                aria-label={f.label}
                className={inputClass}
                value={form[f.key]}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
            ) : (
              <textarea
                aria-label={f.label}
                rows={f.rows}
                className={inputClass}
                value={form[f.key]}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
            )}
          </label>
        ))}
        {error && <div className="text-xs text-danger">{error}</div>}
      </div>
    </Modal>
  )
}
