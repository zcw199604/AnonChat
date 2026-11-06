import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card } from '../../components/Card'
import { currentBroadcasts } from '../../lib/api/client'
import type { Broadcast } from '../../lib/api/client'
import { useIdentityStore } from '../../stores/identity'

/** 广播卡（how.md 2.2）：有广播时展示内容，没有时显示占位文案并引导去 /vip。 */
export function BroadcastCard() {
  const [list, setList] = useState<Broadcast[]>([])
  const initialized = useIdentityStore((s) => s.initialized)

  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    currentBroadcasts()
      .then((b) => {
        if (!cancelled) setList(b)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [initialized])

  return (
    <Card className="flex items-center gap-3 px-5 py-4">
      <span aria-hidden className="text-lg">
        📣
      </span>
      <div className="min-w-0 flex-1 text-sm">
        {list.length > 0 ? (
          <ul className="space-y-0.5">
            {list.map((b) => (
              <li key={b.id} className="truncate text-lobby-ink">
                {b.content}
              </li>
            ))}
          </ul>
        ) : (
          <span className="text-lobby-sub">还没有广播。用广播卡让你的话出现在这里。</span>
        )}
      </div>
      <Link
        to="/vip"
        className="shrink-0 rounded-sm bg-brand/15 px-3 py-1 text-xs font-medium text-brand transition duration-150 ease-out"
      >
        {list.length > 0 ? '发布广播' : '了解广播卡'}
      </Link>
    </Card>
  )
}
