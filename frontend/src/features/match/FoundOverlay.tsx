import { useEffect } from 'react'
import { GradientAvatar } from '../../components/GradientAvatar'
import { Icon } from '../../components/Icon'
import { commonTags, peerSummary } from './machine'
import type { MatchPeer } from './machine'

/** 仅由当前 found 会话挂载；会话改变或结束时卸载并清理全部资源。 */
export function MatchReminder() {
  useEffect(() => {
    const original = document.title
    let timer: number | undefined
    function stop() {
      window.clearInterval(timer)
      timer = undefined
      document.title = original
    }
    function onVisibility() { if (!document.hidden) stop() }
    if (document.hidden) {
      navigator.vibrate?.(80)
      let flash = false
      timer = window.setInterval(() => {
        flash = !flash
        document.title = flash ? '【匹配成功】' : original
      }, 1000)
    }
    document.addEventListener('visibilitychange', onVisibility)
    return () => { stop(); document.removeEventListener('visibilitychange', onVisibility) }
  }, [])
  return null
}

export function FoundOverlay({ seed, nickname, peer, myTags, onClose }: {
  seed: string; nickname: string; peer?: MatchPeer; myTags: string[]; onClose: () => void
}) {
  useEffect(() => {
    const timer = window.setTimeout(onClose, 900)
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKey)
    return () => { window.clearTimeout(timer); document.removeEventListener('keydown', onKey) }
  }, [onClose])
  const common = commonTags(peer?.tags ?? [], myTags)
  return (
    <button type="button" aria-label="进入聊天" onClick={onClose}
      className="fixed inset-0 z-40 flex w-full flex-col items-center justify-center gap-4 text-night-ink"
      style={{ background: 'radial-gradient(ellipse at center, rgb(var(--c-acc-rgb) / 0.22), transparent 70%), linear-gradient(160deg, var(--c-bg-from), var(--c-bg-to))' }}>
      <span className="relative mb-3 flex items-center">
        <GradientAvatar seed={seed || 'anonymous'} size={96} label={nickname} className="animate-found-left" />
        <GradientAvatar seed={peer?.avatarSeed || 'anonymous'} size={96} label={peer?.nickname} className="-ml-[18px] animate-found-right" />
        <span className="absolute -bottom-3 left-1/2 flex h-9 w-9 -translate-x-1/2 items-center justify-center rounded-full bg-bubble-self text-white"><Icon name="sparkle" size={22} /></span>
      </span>
      <span className="text-[13px] tracking-[0.08em] text-chat-acc">匹配成功</span>
      <span className="text-2xl font-bold">{peer?.nickname?.trim() || '对方'}</span>
      <span className="flex gap-2 text-sm text-night-sub">{peerSummary(peer).map((item) => <span key={item}>{item}</span>)}</span>
      {common.length > 0 && <span className="text-sm text-chat-acc-soft">你们都喜欢 {common[0]}</span>}
      <span className="h-1 w-40 overflow-hidden rounded-full bg-glass"><span className="block h-full origin-left animate-found-progress bg-bubble-self" /></span>
    </button>
  )
}
