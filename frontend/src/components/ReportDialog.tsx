import { useState } from 'react'

export interface PeerMessage {
  id: number
  body: string
}

export type ReportReason = 'spam' | 'harassment' | 'porn' | 'illegal' | 'other'

const REASONS: Array<{ value: ReportReason; label: string }> = [
  { value: 'spam', label: '广告' },
  { value: 'harassment', label: '骚扰' },
  { value: 'porn', label: '色情' },
  { value: 'illegal', label: '违法' },
  { value: 'other', label: '其他' },
]

const MAX_REPORT_MESSAGES = 20
const MAX_NOTE = 500

interface ReportDialogProps {
  open: boolean
  peerMessages: PeerMessage[]
  onClose: () => void
  onSubmit: (r: { messageIds: number[]; reason: string; note: string }) => void
}

/** 举报弹窗：多选对方消息（1–20 条）、选择原因、可填备注（≤500 字）。 */
export function ReportDialog({ open, peerMessages, onClose, onSubmit }: ReportDialogProps) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [reason, setReason] = useState('')
  const [note, setNote] = useState('')

  if (!open) return null

  function toggle(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else if (next.size < MAX_REPORT_MESSAGES) next.add(id)
      return next
    })
  }

  const canSubmit = selected.size >= 1 && selected.size <= MAX_REPORT_MESSAGES && reason !== ''

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center p-4 z-50" role="dialog" aria-label="举报">
      <div className="bg-slate-900 rounded-lg p-4 w-full max-w-md max-h-[80vh] overflow-y-auto">
        <h2 className="text-lg font-semibold mb-2">举报</h2>
        <div className="space-y-1 mb-3">
          {peerMessages.map((m) => (
            <label key={m.id} className="flex items-start gap-2 text-sm">
              <input type="checkbox" checked={selected.has(m.id)} onChange={() => toggle(m.id)} />
              <span className="whitespace-pre-wrap break-words text-slate-300">{m.body}</span>
            </label>
          ))}
        </div>
        <div className="mb-3">
          <label className="block text-sm text-slate-400 mb-1">原因</label>
          <select
            aria-label="举报原因"
            className="w-full bg-slate-800 rounded px-2 py-1"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          >
            <option value="">请选择</option>
            {REASONS.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </div>
        <div className="mb-3">
          <label className="block text-sm text-slate-400 mb-1">备注（可选，最多 500 字）</label>
          <textarea
            aria-label="举报备注"
            className="w-full bg-slate-800 rounded px-2 py-1"
            rows={3}
            maxLength={MAX_NOTE}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </div>
        <div className="flex justify-end gap-2">
          <button type="button" className="px-3 py-1 rounded bg-slate-700" onClick={onClose}>
            取消
          </button>
          <button
            type="button"
            className="px-3 py-1 rounded bg-red-600 disabled:opacity-40"
            disabled={!canSubmit}
            onClick={() => onSubmit({ messageIds: [...selected], reason, note })}
          >
            提交举报
          </button>
        </div>
      </div>
    </div>
  )
}
