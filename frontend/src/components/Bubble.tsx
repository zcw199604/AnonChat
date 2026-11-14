import type { ReactNode } from 'react'

interface BubbleProps {
  self: boolean
  children: ReactNode
  className?: string
  first?: boolean
  /** 组尾标识；§2.2 的底部尾角始终为 6px，不随 last 改变。 */
  last?: boolean
}

/** 实色气泡；组首保留完整上圆角，底部尾角始终为 6px。 */
export function Bubble({ self, children, className, first = true }: BubbleProps) {
  return (
    <div
      className={`animate-fade-up whitespace-pre-wrap break-words px-3.5 py-[9px] text-[15px] leading-[1.5] ${
        self
          ? 'bg-bubble-self text-white'
          : 'border border-glass-line bg-bubble-peer text-night-ink'
      } ${className ?? ''}`}
      style={{ borderRadius: self ? `18px ${first ? 18 : 6}px 6px 18px` : `${first ? 18 : 6}px 18px 18px 6px` }}
    >
      {children}
    </div>
  )
}
