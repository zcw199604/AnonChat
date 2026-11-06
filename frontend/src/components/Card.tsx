import type { ReactNode } from 'react'

interface CardProps {
  children: ReactNode
  className?: string
  /** 是否播放入场动画（默认开启，进入视口时 fade-up）。 */
  animate?: boolean
  /** 供测试定位的 data-testid。 */
  'data-testid'?: string
}

/** 大厅卡片：白底、24px 圆角、极浅边框与唯一允许的 --shadow-soft。 */
export function Card({ children, className, animate = true, 'data-testid': testId }: CardProps) {
  return (
    <div
      data-testid={testId}
      className={`rounded-panel border border-lobby-line bg-lobby-card shadow-soft ${
        animate ? 'animate-fade-up' : ''
      } ${className ?? ''}`}
    >
      {children}
    </div>
  )
}
