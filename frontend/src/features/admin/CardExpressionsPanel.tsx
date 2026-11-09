import { useEffect, useState } from 'react'
import { adminCardExpressions, adminCreateCardExpression, adminDeleteCardExpression, adminUpdateCardExpression } from './botApi'
import type { CardExpression } from './botApi'
import { Button, EmptyState, LoadingRows, inputClass } from './ui'

/** 表情独立保存；角色卡表单关闭前等待写入完成。 */
export function CardExpressionsPanel({ cardId, disabled, onBusy, onChanged, onError }: {
  cardId: string
  disabled: boolean
  onBusy: (busy: boolean) => void
  onChanged: () => void
  onError: (error: unknown) => void
}) {
  const [items, setItems] = useState<CardExpression[]>([])
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [file, setFile] = useState<File | null>(null)
  const [fileVersion, setFileVersion] = useState(0)
  const [key, setKey] = useState('')
  const [description, setDescription] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    adminCardExpressions(cardId).then((rows) => {
      if (active) setItems(rows)
    }).catch((e: unknown) => {
      if (active) { setFailed(true); onError(e) }
    }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [cardId, onError])

  async function run(action: () => Promise<void>) {
    setBusy(true)
    onBusy(true)
    setError('')
    try {
      await action()
      onChanged()
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
      onBusy(false)
    }
  }

  async function upload() {
    if (!file || !key.trim()) { setError('请选择表情图片并填写标识'); return }
    await run(async () => {
      const item = await adminCreateCardExpression(cardId, file, key.trim(), description)
      setItems((rows) => [...rows, item])
      setFile(null)
      setFileVersion((v) => v + 1)
      setKey('')
      setDescription('')
    })
  }

  return (
    <div className="space-y-3">
      <p className="text-xs text-lobby-sub">表情操作会立即保存，取消角色卡编辑不会撤销表情修改。</p>
      {loading ? <LoadingRows rows={2} /> : failed ? <p className="text-xs text-danger">表情加载失败，请关闭后重试。</p> : items.length === 0 ? <EmptyState>还没有表情。</EmptyState> : items.map((item) => (
        <ExpressionRow key={item.id} item={item} disabled={busy || disabled} onSave={(body) => run(async () => {
          const updated = await adminUpdateCardExpression(item.id, body)
          setItems((rows) => rows.map((row) => row.id === updated.id ? updated : row))
        })} onDelete={() => run(async () => {
          await adminDeleteCardExpression(item.id)
          setItems((rows) => rows.filter((row) => row.id !== item.id))
        })} />
      ))}
      {!failed && <fieldset disabled={busy || disabled || loading} className="space-y-2 rounded-sm border border-lobby-line p-3">
        <legend className="px-1 text-xs text-lobby-sub">上传表情</legend>
        <label className="block text-xs text-lobby-sub">表情图片<input key={fileVersion} aria-label="表情图片" className={inputClass} type="file" accept="image/png,image/jpeg,image/gif,image/webp" onChange={(e) => setFile(e.target.files?.[0] ?? null)} /></label>
        <label className="block text-xs text-lobby-sub">新表情标识<input aria-label="新表情标识" maxLength={32} className={inputClass} value={key} onChange={(e) => setKey(e.target.value)} /></label>
        <label className="block text-xs text-lobby-sub">新表情说明<input aria-label="新表情说明" maxLength={200} className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} /></label>
        <Button size="sm" loading={busy} onClick={() => void upload()}>上传表情</Button>
      </fieldset>}
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
    </div>
  )
}

function ExpressionRow({ item, disabled, onSave, onDelete }: {
  item: CardExpression
  disabled: boolean
  onSave: (body: { key: string; description: string; sort: number }) => Promise<void>
  onDelete: () => Promise<void>
}) {
  const [key, setKey] = useState(item.key)
  const [description, setDescription] = useState(item.description)
  const [sort, setSort] = useState(String(item.sort))
  const [error, setError] = useState('')

  function save() {
    const value = Number(sort)
    if (!key.trim() || sort.trim() === '' || !Number.isInteger(value) || value < -2147483648 || value > 2147483647) {
      setError('请填写标识和有效的整数排序')
      return
    }
    setError('')
    void onSave({ key: key.trim(), description, sort: value })
  }

  return <fieldset disabled={disabled} className="space-y-2 rounded-sm border border-lobby-line p-3">
    <div className="flex items-start gap-3">
      <img className="h-16 w-16 rounded-sm object-contain" src={`/api/admin/media-items/${item.mediaItemId}/file`} alt={`表情 ${item.key}`} />
      <div className="min-w-0 flex-1 space-y-2">
        <label className="block text-xs text-lobby-sub">标识<input aria-label={`表情标识 ${item.key}`} maxLength={32} className={inputClass} value={key} onChange={(e) => setKey(e.target.value)} /></label>
        <label className="block text-xs text-lobby-sub">说明<input aria-label={`表情说明 ${item.key}`} maxLength={200} className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} /></label>
        <label className="block text-xs text-lobby-sub">排序<input aria-label={`表情排序 ${item.key}`} className={inputClass} type="number" step={1} value={sort} onChange={(e) => setSort(e.target.value)} /></label>
      </div>
    </div>
    <div className="flex justify-end gap-2">
      <Button size="sm" onClick={save} aria-label={`保存表情 ${item.key}`}>保存表情</Button>
      <Button size="sm" variant="ghost" className="text-danger" onClick={() => void onDelete()} aria-label={`删除表情 ${item.key}`}>删除表情</Button>
    </div>
    {error && <p role="alert" className="text-xs text-danger">{error}</p>}
  </fieldset>
}
