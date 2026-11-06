import { useEffect, useState } from 'react'
import { Marquee } from '../components/Marquee'
import { listAnnouncements } from '../lib/api/client'
import { useIdentityStore } from '../stores/identity'

/** 公告跑马灯（how.md 2.1）：无公告时整条隐藏。 */
export function AnnouncementBar() {
  const [items, setItems] = useState<string[]>([])
  const initialized = useIdentityStore((s) => s.initialized)

  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    listAnnouncements()
      .then((list) => {
        if (!cancelled) setItems(list.map((a) => `📢 ${a.content}`))
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [initialized])

  if (items.length === 0) return null
  return (
    <div className="mx-auto w-full max-w-[1200px] px-4 pt-3">
      <Marquee items={items} />
    </div>
  )
}
