import { useEffect, useState } from 'react'
import { Card } from '../../components/Card'
import { onlineStats } from '../../lib/api/client'
import type { OnlineStats } from '../../lib/api/client'
import { useIdentityStore } from '../../stores/identity'
import { useUiStore } from '../../stores/ui'

/** 在线人数（how.md 2.2）：GET /api/stats/online，30 秒轮询。 */
export function OnlineCard() {
  const [stats, setStats] = useState<OnlineStats | null>(null)
  const initialized = useIdentityStore((s) => s.initialized)
  // WS 连上后重新拉一次，避免首屏拿到连接建立之前的缓存值。
  const connected = useUiStore((s) => s.connected)

  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    const load = () => {
      onlineStats()
        .then((s) => {
          if (!cancelled) setStats(s)
        })
        .catch(() => undefined)
    }
    load()
    const t = setInterval(load, 30000)
    return () => {
      cancelled = true
      clearInterval(t)
    }
  }, [initialized, connected])

  return (
    // 按内容自适应高度：不再用 h-full 拉伸（审查意见 V1）。
    <Card data-testid="online-card" className="space-y-1 p-5">
      <div className="text-2xl font-bold tracking-tight text-lobby-ink">
        {stats ? stats.online.toLocaleString() : '—'}
        <span className="ml-1 text-sm font-normal text-lobby-sub">在线</span>
      </div>
      <div className="text-xs text-lobby-sub">
        匹配中 {stats?.matching ?? 0} · 房间 {stats?.inRooms ?? 0}
      </div>
    </Card>
  )
}
