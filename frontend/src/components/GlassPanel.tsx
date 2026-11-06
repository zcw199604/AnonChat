import type { ReactNode } from 'react'

interface GlassPanelProps {
  children: ReactNode
  className?: string
}

/** 聊天面板：暗色玻璃拟态（how.md 1.4）。 */
export function GlassPanel({ children, className }: GlassPanelProps) {
  return (
    <div
      className={`flex min-h-0 flex-col rounded-panel border border-glass-line bg-glass backdrop-blur-[20px] ${
        className ?? ''
      }`}
    >
      {children}
    </div>
  )
}
