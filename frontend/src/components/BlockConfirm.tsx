interface BlockConfirmProps {
  open: boolean
  name?: string
  onCancel: () => void
  onConfirm: () => void
}

/** 屏蔽二次确认。 */
export function BlockConfirm({ open, name, onCancel, onConfirm }: BlockConfirmProps) {
  if (!open) return null
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center p-4 z-50" role="dialog" aria-label="屏蔽确认">
      <div className="bg-slate-900 rounded-lg p-4 w-full max-w-sm">
        <p className="mb-4 text-slate-200">
          确定要屏蔽{name ? ` ${name} ` : '对方'}吗？屏蔽后双方将互相不可见，也不会再被匹配到一起。
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="px-3 py-1 rounded bg-slate-700" onClick={onCancel}>
            取消
          </button>
          <button type="button" className="px-3 py-1 rounded bg-red-600" onClick={onConfirm}>
            确认屏蔽
          </button>
        </div>
      </div>
    </div>
  )
}
