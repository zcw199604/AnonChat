import type { ReactNode } from 'react'

interface BubbleProps {
  self: boolean
  children: ReactNode
  className?: string
}

/**
 * 聊天气泡：对方用半透明白底，自己用品牌紫→淡紫渐变白字（how.md 1.4）。
 * 圆角 20px，入场 fade-up。
 */
export function Bubble({ self, children, className }: BubbleProps) {
  return (
    <div
      className={`animate-fade-up max-w-[78%] whitespace-pre-wrap break-words rounded-card px-3.5 py-2 text-[15px] leading-relaxed ${
        self
          ? 'text-white'
          : 'border border-glass-line bg-bubble-peer text-night-ink backdrop-blur-[12px]'
      } ${className ?? ''}`}
      style={
        self
          ? { background: 'linear-gradient(135deg, var(--color-brand), var(--color-brand-2))' }
          : undefined
      }
    >
      {children}
    </div>
  )
}
