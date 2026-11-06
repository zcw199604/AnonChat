import { useUiStore } from '../stores/ui'

const REASON_TEXT: Record<string, string> = {
  banned: '你已被封禁',
  identity_reset: '身份已重置',
}

/** 被踢出后的全屏提示（不再自动重连）。 */
export function KickedOverlay() {
  const kicked = useUiStore((s) => s.kicked)
  if (!kicked) return null
  return (
    <div
      className="fixed inset-0 z-[60] flex flex-col items-center justify-center gap-4 px-6 text-center"
      style={{ background: 'linear-gradient(160deg, var(--color-night-from), var(--color-night-to))' }}
    >
      <h2 className="text-2xl font-bold text-night-ink">{REASON_TEXT[kicked] ?? '连接已断开'}</h2>
      <p className="text-night-sub">你已被移出当前会话，刷新页面可重新进入。</p>
      <button
        type="button"
        className="rounded-full bg-brand px-5 py-2 font-medium text-white transition duration-150 ease-out"
        onClick={() => location.reload()}
      >
        刷新页面
      </button>
    </div>
  )
}
