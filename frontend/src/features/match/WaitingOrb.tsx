import { GradientAvatar } from '../../components/GradientAvatar'

/** 三道错开的搜索涟漪；全局 reduced-motion 规则关闭动画。 */
export function WaitingOrb({ seed, nickname }: { seed: string; nickname: string }) {
  return (
    <div className="relative flex h-60 w-60 items-center justify-center">
      {[0, 1, 2].map((i) => (
        <span key={i} aria-hidden="true" data-testid="match-ripple"
          className={`absolute h-[120px] w-[120px] animate-match-ripple rounded-full border-[1.5px] ${i === 1 ? 'border-accent/55' : 'border-[rgb(var(--c-acc-rgb)/0.6)]'}`}
          style={{ animationDelay: `${i * 0.8}s` }} />
      ))}
      <div className="relative rounded-full border border-[rgb(var(--c-acc-rgb)/0.6)] p-1.5" style={{ background: 'var(--c-bg-from)' }}>
        <GradientAvatar seed={seed || 'anonymous'} size={104} label={nickname} />
      </div>
    </div>
  )
}
