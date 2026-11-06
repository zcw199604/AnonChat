import { useEffect, type ReactNode } from 'react'

interface SheetProps {
  open: boolean
  title: string
  onClose: () => void
  children: ReactNode
}

/**
 * 底部抽屉 / 居中对话框：移动端从底部滑入，桌面端居中显示。
 * 带遮罩，支持 Esc 关闭。
 */
export function Sheet({ open, title, onClose, children }: SheetProps) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-black/40 p-0 sm:items-center sm:p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onClose}
    >
      <div
        className="animate-fade-up w-full max-w-md rounded-t-panel bg-lobby-card p-5 shadow-soft sm:rounded-panel"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-base font-semibold text-lobby-ink">{title}</h2>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            className="rounded-sm px-2 py-1 text-sm text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg"
          >
            关闭
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}
