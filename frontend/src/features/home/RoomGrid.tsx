import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card } from '../../components/Card'
import { listRooms } from '../../lib/api/client'
import { useIdentityStore } from '../../stores/identity'
import type { RoomInfo } from '../../lib/api/client'
import { roomEmoji } from './roomEmoji'

/** 话题房间 2×2（how.md 2.2）：取 /api/rooms 的前 4 个。 */
export function RoomGrid() {
  const [rooms, setRooms] = useState<RoomInfo[]>([])
  // 身份初始化完成后再拉取，避免启动时的 401。
  const initialized = useIdentityStore((s) => s.initialized)

  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    listRooms()
      .then((r) => {
        if (!cancelled) setRooms(r.slice(0, 4))
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [initialized])

  return (
    <section className="h-full" aria-label="话题房间">
      <h2 className="mb-3 text-sm font-semibold text-lobby-ink">话题房间</h2>
      <div className="grid grid-cols-2 gap-4">
        {rooms.map((room) => (
          <Link key={room.id} to={`/rooms/${room.id}`} className="group block">
            <Card className="flex h-full flex-col gap-2 p-4 transition duration-150 ease-out group-hover:-translate-y-0.5">
              <span
                aria-hidden
                className="flex h-10 w-10 items-center justify-center rounded-sm text-xl"
                style={{
                  background:
                    'linear-gradient(135deg, color-mix(in srgb, var(--color-brand) 15%, transparent), color-mix(in srgb, var(--color-accent) 15%, transparent))',
                }}
              >
                {roomEmoji(room)}
              </span>
              <span className="text-sm font-semibold text-lobby-ink">{room.name}</span>
              <span className="text-xs text-lobby-sub">{room.online} 人在线</span>
            </Card>
          </Link>
        ))}
      </div>
    </section>
  )
}
