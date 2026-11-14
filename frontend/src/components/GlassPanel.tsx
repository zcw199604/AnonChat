import type { ReactNode } from 'react'

interface GlassPanelProps {
  children: ReactNode
  className?: string
}

/** 跟随主题的聊天面板。 */
export function GlassPanel({ children, className }: GlassPanelProps) {
  return (
    <div
      className={`flex min-h-0 flex-col rounded-panel border border-glass-line bg-[var(--c-panel)] ${
        className ?? ''
      }`}
    >
      {children}
    </div>
  )
}
