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
    <div className="fixed inset-0 bg-slate-950/95 flex flex-col items-center justify-center gap-4 z-[60] text-center px-6">
      <h2 className="text-2xl font-bold text-slate-100">{REASON_TEXT[kicked] ?? '连接已断开'}</h2>
      <p className="text-slate-400">你已被移出当前会话，刷新页面可重新进入。</p>
      <button
        type="button"
        className="px-4 py-2 rounded bg-indigo-600 text-white"
        onClick={() => location.reload()}
      >
        刷新页面
      </button>
    </div>
  )
}
