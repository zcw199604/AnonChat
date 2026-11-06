import type { ReactNode } from 'react'

interface VipLockProps {
  /** VIP 是否已生效；生效时不显示遮罩。 */
  active: boolean
  onUnlock: () => void
  children: ReactNode
}

/**
 * VIP 遮罩：半透明遮罩 + 居中胶囊按钮「🔒 激活码解锁 VIP 特权 →」（how.md 1.4）。
 * 显示与否完全由 me.vip.active 决定（服务端裁决）。
 */
export function VipLock({ active, onUnlock, children }: VipLockProps) {
  return (
    <div className="relative">
      <div aria-hidden={!active} className={active ? 'pointer-events-none select-none opacity-50' : ''}>
        {children}
      </div>
      {active && (
        <div className="absolute inset-0 flex items-center justify-center rounded-sm bg-lobby-card/40 backdrop-blur-[2px]" data-testid="vip-lock">
          <button
            type="button"
            onClick={onUnlock}
            className="rounded-full bg-brand px-4 py-2 text-sm font-medium text-white transition duration-150 ease-out hover:opacity-90"
          >
            🔒 激活码解锁 VIP 特权 →
          </button>
        </div>
      )}
    </div>
  )
}
