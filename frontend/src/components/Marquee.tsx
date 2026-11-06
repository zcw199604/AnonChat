import { useEffect, useRef, useState } from 'react'
import { marqueeNeedsScroll } from './marqueeScroll'

interface MarqueeProps {
  items: string[]
}

/** 滚动速度：40px/s（how.md 1.4）。 */
const SPEED_PX_PER_S = 40

/**
 * 公告跑马灯：CSS transform 匀速滚动，hover 时暂停；
 * 只有一条且放得下时不滚动。
 */
export function Marquee({ items }: MarqueeProps) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const innerRef = useRef<HTMLDivElement>(null)
  const [scroll, setScroll] = useState(false)
  const [duration, setDuration] = useState(20)

  useEffect(() => {
    const wrap = wrapRef.current
    const inner = innerRef.current
    if (!wrap || !inner) return
    const measure = () => {
      const contentWidth = inner.scrollWidth
      const containerWidth = wrap.clientWidth
      const need = marqueeNeedsScroll(items.length, contentWidth, containerWidth)
      setScroll(need)
      if (need) setDuration(Math.max(6, contentWidth / SPEED_PX_PER_S))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(wrap)
    return () => ro.disconnect()
  }, [items])

  if (items.length === 0) return null

  return (
    <div
      ref={wrapRef}
      data-testid="marquee"
      className="group flex h-10 items-center overflow-hidden rounded-sm bg-brand/10 px-4 text-sm text-brand"
    >
      <div
        ref={innerRef}
        className={`flex shrink-0 items-center gap-8 ${scroll ? 'animate-none group-hover:[animation-play-state:paused]' : ''}`}
        style={
          scroll
            ? { animation: `marquee-scroll ${duration}s linear infinite`, animationPlayState: 'running' }
            : undefined
        }
      >
        {items.map((text, i) => (
          <span key={i} className="whitespace-nowrap">
            {text}
          </span>
        ))}
      </div>
      <style>{`@keyframes marquee-scroll { from { transform: translateX(0); } to { transform: translateX(-50%); } }
[data-testid="marquee"]:hover > div { animation-play-state: paused !important; }
@media (prefers-reduced-motion: reduce) { [data-testid="marquee"] > div { animation: none !important; } }`}</style>
    </div>
  )
}
