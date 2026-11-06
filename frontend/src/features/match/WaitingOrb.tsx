/**
 * 匹配等待态的大渐变圆与外圈呼吸光环（how.md 2.3，审查意见 V3）。
 *
 * - 光环是 brand/20（品牌紫 20% 不透明度）的一圈，尺寸比渐变圆大一圈；
 * - 动画用 animate-breathe（1.6s ease-in-out，scale 0.85↔1 + opacity 0.3↔1）；
 * - prefers-reduced-motion 下由 index.css 的全局规则关闭动画。
 */
export function WaitingOrb() {
  return (
    <div className="relative flex h-40 w-40 items-center justify-center">
      <span
        aria-hidden
        data-testid="match-halo"
        className="absolute inset-0 h-40 w-40 animate-breathe rounded-full"
        style={{ background: 'color-mix(in srgb, var(--color-brand) 20%, transparent)' }}
      />
      <div
        aria-hidden
        data-testid="match-orb"
        className="relative flex h-32 w-32 items-center justify-center rounded-full text-white"
        style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
      >
        <span className="text-3xl">🎲</span>
      </div>
    </div>
  )
}
