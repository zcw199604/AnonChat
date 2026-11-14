import { useEffect, useState } from 'react'
import { Icon } from '../../components/Icon'

/** 调用方以 status + conversationId 为 key，确保旧会话定时器不能影响新会话。 */
export function NextButton({ onNext }: { onNext: () => void }) {
  const [mode, setMode] = useState<'idle' | 'confirm' | 'pending'>('idle')
  useEffect(() => {
    if (mode === 'idle') return
    const timer = window.setTimeout(() => setMode('idle'), mode === 'confirm' ? 3000 : 5000)
    return () => window.clearTimeout(timer)
  }, [mode])
  return (
    <div className="shrink-0">
      <button type="button" disabled={mode === 'pending'}
        className={`flex h-[34px] items-center gap-1 rounded-full border px-3 text-xs ${mode === 'confirm' ? 'border-[rgb(var(--c-skip-rgb)/0.6)] bg-[rgb(var(--c-skip-rgb)/0.14)] text-skip' : 'border-glass-line text-night-ink'}`}
        onClick={() => {
          if (mode === 'idle') setMode('confirm')
          else if (mode === 'confirm') { setMode('pending'); onNext() }
        }}>
        <Icon name="shuffle" size={16} />{mode === 'idle' ? '换一个' : mode === 'confirm' ? '确认换人？' : '换人中…'}
      </button>
      {mode === 'confirm' && <p className="absolute top-[60px] right-0 left-0 z-10 border-b border-glass-line bg-sheet px-3 py-2 text-center text-xs text-night-sub">再点一次换下一个人，3 秒后自动取消</p>}
    </div>
  )
}
