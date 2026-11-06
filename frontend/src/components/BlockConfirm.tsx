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
    <div
      className="animate-fade-up fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-label="屏蔽确认"
    >
      <div className="w-full max-w-sm rounded-panel bg-lobby-card p-5 shadow-soft">
        <p className="mb-4 text-sm leading-relaxed text-lobby-ink">
          确定要屏蔽{name ? ` ${name} ` : '对方'}吗？屏蔽后双方将互相不可见，也不会再被匹配到一起。
        </p>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="rounded-sm px-3 py-1.5 text-sm text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg"
            onClick={onCancel}
          >
            取消
          </button>
          <button
            type="button"
            className="rounded-sm bg-danger px-3 py-1.5 text-sm text-white transition duration-150 ease-out"
            onClick={onConfirm}
          >
            确认屏蔽
          </button>
        </div>
      </div>
    </div>
  )
}
