import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card } from '../../components/Card'
import { getFeed } from '../../lib/api/client'
import type { FeedItem } from '../../lib/api/client'
import { useIdentityStore } from '../../stores/identity'

const ICON: Record<FeedItem['type'], string> = {
  announcement: '📢',
  broadcast: '📣',
  work: '📖',
}

/** 匿名动态（how.md 2.2）：GET /api/feed，作品条目可点击进入阅读页。 */
export function FeedCard() {
  const [items, setItems] = useState<FeedItem[]>([])
  const initialized = useIdentityStore((s) => s.initialized)

  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    getFeed()
      .then((list) => {
        if (!cancelled) setItems(list)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [initialized])

  return (
    <Card className="h-full space-y-2 p-5">
      <h2 className="text-sm font-semibold text-lobby-ink">匿名动态</h2>
      {items.length === 0 && <p className="text-xs text-lobby-sub">还没有动态，晚点再来看看。</p>}
      <ul className="space-y-2">
        {items.slice(0, 8).map((item) => (
          <li key={`${item.type}-${item.id}`} className="text-xs text-lobby-sub">
            <span aria-hidden className="mr-1">
              {ICON[item.type]}
            </span>
            {item.type === 'work' ? (
              <Link to={`/works/${item.id}`} className="text-lobby-ink transition duration-150 ease-out hover:text-brand">
                {item.text}
              </Link>
            ) : (
              <span className="text-lobby-ink">{item.text}</span>
            )}
          </li>
        ))}
      </ul>
    </Card>
  )
}
