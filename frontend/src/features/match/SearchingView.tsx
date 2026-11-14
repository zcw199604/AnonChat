import { useEffect, useState } from 'react'
import { onlineStats } from '../../lib/api/client'
import { GradientAvatar } from '../../components/GradientAvatar'
import { WaitingOrb } from './WaitingOrb'
import { filterChips } from './machine'
import type { MatchFilters } from './machine'

const HINTS = ['耐心一点，好聊的人值得等', '匹配成功后会提醒你，可以先切出去', '换个标签，也许能遇到更合拍的人']

export function SearchingView({ waiting, seed, nickname, filters, onStart, onCancel, onHome }: {
  waiting: boolean
  seed: string
  nickname: string
  filters?: MatchFilters
  onStart: () => void
  onCancel: () => void
  onHome: () => void
}) {
  const [elapsed, setElapsed] = useState(0)
  const [matching, setMatching] = useState<number | null>(null)
  useEffect(() => {
    if (!waiting) return
    let active = true
    const started = Date.now()
    const timer = window.setInterval(() => setElapsed(Math.floor((Date.now() - started) / 1000)), 1000)
    async function refresh() {
      try {
        const stats = await onlineStats()
        if (active) setMatching(stats.matching)
      } catch {
        if (active) setMatching(null)
      }
    }
    void refresh()
    const poll = window.setInterval(() => { void refresh() }, 10_000)
    return () => { active = false; window.clearInterval(timer); window.clearInterval(poll) }
  }, [waiting])
  const chips = filterChips(filters)
  const hard = filters && (filters.cleanMode || filters.gender !== 'any' || filters.sameProvince)
  return (
    <main className="mx-auto flex min-h-[100dvh] max-w-[760px] flex-col px-5 text-night-ink">
      <header className="flex h-14 shrink-0 items-center justify-center text-[15px] font-semibold">随机匹配</header>
      <div className="flex flex-1 flex-col items-center justify-center gap-5 py-6 text-center">
        {waiting ? <WaitingOrb seed={seed} nickname={nickname} /> : <div className="flex h-60 items-center"><GradientAvatar seed={seed || 'anonymous'} size={104} label={nickname} /></div>}
        <div className="space-y-2">
          <h1 className="text-xl font-semibold">{waiting ? '正在寻找有趣的人' : '随机匹配'}</h1>
          <p className="text-sm text-night-sub">{waiting ? HINTS[Math.floor(elapsed / 4) % HINTS.length] : '和陌生人一对一随机聊天'}</p>
        </div>
        {waiting && <p className="flex items-center gap-2 text-xs text-night-sub">
          <span className="tabular-nums">{String(Math.floor(elapsed / 60)).padStart(2, '0')}:{String(elapsed % 60).padStart(2, '0')}</span>
          {matching !== null && <><span aria-hidden="true">·</span><span className="h-1.5 w-1.5 animate-breathe rounded-full bg-chat-acc" /><span>{matching} 人正在匹配</span></>}
        </p>}
        {waiting && chips.length > 0 && <div className="flex flex-wrap justify-center gap-2">
          {chips.map((chip) => <span key={chip} className="flex h-7 items-center rounded-full border border-glass-line bg-glass px-3 text-xs">{chip}</span>)}
        </div>}
        {waiting && elapsed >= 30 && hard && <div className="rounded-card border border-glass-line bg-glass p-4 text-sm text-night-sub">
          <p>条件比较严格，放宽一点会更快匹配到</p>
          <button type="button" onClick={onCancel} className="mt-2 text-chat-acc">调整条件</button>
        </div>}
        {!waiting && <><button type="button" onClick={onStart} className="h-12 w-full rounded-full bg-cta text-base font-bold text-white">开始匹配</button>
          <button type="button" onClick={onHome} className="text-sm text-night-sub">返回大厅</button></>}
      </div>
      {waiting && <button type="button" onClick={onCancel} className="mb-[max(20px,env(safe-area-inset-bottom))] h-12 shrink-0 rounded-full border border-[rgb(var(--c-ov-rgb)/0.14)] text-sm">取消匹配</button>}
    </main>
  )
}
