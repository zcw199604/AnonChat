/** 骨架占位：用于后端尚未接通的区块（不展示任何假数据）。 */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      data-testid="skeleton"
      className={`animate-pulse rounded-sm bg-lobby-line/70 ${className ?? ''}`}
    />
  )
}
