import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ReactNode,
} from 'react'
import { Skeleton } from '../../components/Skeleton'
import { Segmented } from '../../components/Segmented'
import type { Tone } from './labels'

/** 后台按钮：四个变体 + 两种尺寸 + loading 态。 */
export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  className,
  children,
  disabled,
  type = 'button',
  ...rest
}: {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
  size?: 'sm' | 'md'
  loading?: boolean
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  const variants: Record<string, string> = {
    primary: 'bg-brand text-white hover:bg-brand-2',
    secondary: 'border border-lobby-line bg-lobby-bg text-lobby-ink hover:border-brand/40',
    danger: 'bg-danger text-white hover:opacity-90',
    ghost: 'text-lobby-sub hover:bg-lobby-bg hover:text-lobby-ink',
  }
  const sizes: Record<string, string> = {
    sm: 'px-2.5 py-1 text-xs',
    md: 'px-3.5 py-1.5 text-sm',
  }
  return (
    <button
      {...rest}
      type={type}
      disabled={disabled || loading}
      className={`inline-flex items-center justify-center gap-1 rounded-sm font-medium transition duration-150 ease-out disabled:cursor-not-allowed disabled:opacity-40 ${variants[variant]} ${sizes[size]} ${className ?? ''}`}
    >
      {loading && typeof children === 'string' ? `${children}…` : children}
    </button>
  )
}

/** 页面头部：标题 + 一句描述 + 右侧操作区。 */
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <header className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold text-lobby-ink">{title}</h1>
        {description && <p className="mt-1 text-sm text-lobby-sub">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}

/** 后台白底卡片容器。 */
export function Panel({
  children,
  className,
  'data-testid': testId,
}: {
  children: ReactNode
  className?: string
  'data-testid'?: string
}) {
  return (
    <section
      data-testid={testId}
      className={`rounded-card border border-lobby-line bg-lobby-card shadow-soft ${className ?? ''}`}
    >
      {children}
    </section>
  )
}

const TONE_CLASSES: Record<Tone, string> = {
  neutral: 'bg-lobby-bg text-lobby-sub',
  info: 'bg-brand/10 text-brand',
  success: 'bg-brand/15 text-brand',
  warning: 'bg-accent/20 text-lobby-ink',
  danger: 'bg-danger/10 text-danger',
}

/** 状态胶囊：色调由 labels.ts 决定。 */
export function StatusBadge({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs whitespace-nowrap ${TONE_CLASSES[tone]}`}
    >
      {children}
    </span>
  )
}

/** 状态筛选：复用 Segmented 的原生 button 组，替代下拉框。 */
export function FilterTabs<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: {
  options: Array<{ value: T; label: string }>
  value: T
  onChange: (v: T) => void
  ariaLabel: string
}) {
  return <Segmented options={options} value={value} onChange={onChange} ariaLabel={ariaLabel} />
}

/** 空态：居中一句说明。 */
export function EmptyState({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-card border border-dashed border-lobby-line px-4 py-10 text-center text-sm text-lobby-sub">
      {children}
    </div>
  )
}

/** 加载态：若干骨架行。 */
export function LoadingRows({ rows = 3 }: { rows?: number }) {
  return (
    <div className="space-y-2" aria-busy>
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  )
}

/** 游标分页的「加载更多」。 */
export function LoadMore({
  hasMore,
  loading,
  onClick,
}: {
  hasMore: boolean
  loading?: boolean
  onClick: () => void
}) {
  if (!hasMore) return null
  return (
    <div className="pt-1">
      <Button variant="secondary" size="sm" loading={loading} onClick={onClick}>
        加载更多
      </Button>
    </div>
  )
}

/** 表单字段：可见 label 包住输入框，保证 getByLabel 可用。 */
export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className="block space-y-1">
      <span className="block text-xs font-medium text-lobby-sub">{label}</span>
      {children}
      {hint && <span className="block text-[11px] text-lobby-sub">{hint}</span>}
    </label>
  )
}

/** 共享输入框样式。 */
export const inputClass =
  'w-full rounded-sm border border-lobby-line bg-lobby-bg px-2.5 py-1.5 text-sm text-lobby-ink placeholder:text-lobby-sub/70'

const MODAL_WIDTH = {
  sm: 'max-w-sm',
  md: 'max-w-lg',
  lg: 'max-w-2xl',
  xl: 'max-w-3xl',
} as const

/**
 * 通用模态框：遮罩点击 / ESC 关闭，打开时焦点移入；
 * 内容区可滚动，footer 固定在底部（适合长表单）。
 */
export function Modal({
  open,
  title,
  description,
  size = 'sm',
  closeDisabled = false,
  children,
  footer,
  onClose,
}: {
  open: boolean
  title: string
  description?: ReactNode
  size?: keyof typeof MODAL_WIDTH
  /** 提交中禁止关闭（遮罩与 ESC 都无效）。 */
  closeDisabled?: boolean
  children?: ReactNode
  footer?: ReactNode
  onClose: () => void
}) {
  const boxRef = useRef<HTMLDivElement>(null)
  const titleId = useId()

  // 打开时把焦点移进对话框，方便键盘操作。
  useEffect(() => {
    if (!open) return
    boxRef.current?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !closeDisabled) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose, closeDisabled])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={() => {
        if (!closeDisabled) onClose()
      }}
    >
      <div
        ref={boxRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={`animate-fade-up flex max-h-[90vh] w-full flex-col rounded-panel border border-lobby-line bg-lobby-card shadow-soft outline-none ${MODAL_WIDTH[size]}`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="shrink-0 px-5 pt-5">
          <h2 id={titleId} className="text-base font-semibold text-lobby-ink">
            {title}
          </h2>
          {description && <div className="mt-2 text-sm text-lobby-sub">{description}</div>}
        </div>
        {children && <div className="min-h-0 flex-1 overflow-y-auto px-5 pt-3">{children}</div>}
        {footer && <div className="flex shrink-0 justify-end gap-2 px-5 pt-4 pb-5">{footer}</div>}
      </div>
    </div>
  )
}

/** 受控确认对话框：遮罩/ESC 关闭，确认按钮带 loading。 */
export function ConfirmDialog({
  open,
  title,
  description,
  confirmText,
  cancelText = '取消',
  danger = false,
  children,
  onConfirm,
  onClose,
}: {
  open: boolean
  title: string
  description?: ReactNode
  confirmText: string
  cancelText?: string
  danger?: boolean
  children?: ReactNode
  onConfirm: () => Promise<void>
  onClose: () => void
}) {
  const [busy, setBusy] = useState(false)

  async function handleConfirm() {
    setBusy(true)
    try {
      await onConfirm()
    } catch {
      // 错误提示由调用方通过 toast 下发，这里只负责收起 loading。
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      title={title}
      description={description}
      closeDisabled={busy}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            {cancelText}
          </Button>
          <Button variant={danger ? 'danger' : 'primary'} loading={busy} onClick={() => void handleConfirm()}>
            {confirmText}
          </Button>
        </>
      }
    >
      {children}
    </Modal>
  )
}

/** 驳回对话框：在确认框里加一个可选的驳回原因。 */
export function ReasonDialog({
  open,
  title,
  description,
  confirmText = '确认驳回',
  onConfirm,
  onClose,
}: {
  open: boolean
  title: string
  description?: ReactNode
  confirmText?: string
  onConfirm: (reason: string) => Promise<void>
  onClose: () => void
}) {
  const [reason, setReason] = useState('')

  const handleClose = useCallback(() => {
    setReason('')
    onClose()
  }, [onClose])

  return (
    <ConfirmDialog
      open={open}
      title={title}
      description={description}
      confirmText={confirmText}
      danger
      onClose={handleClose}
      onConfirm={async () => {
        await onConfirm(reason.trim())
        setReason('')
      }}
    >
      <label className="block space-y-1">
        <span className="block text-xs font-medium text-lobby-sub">驳回原因（可选）</span>
        <textarea
          aria-label="驳回原因"
          rows={3}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="会展示给用户，可留空"
          className={inputClass}
        />
      </label>
    </ConfirmDialog>
  )
}
