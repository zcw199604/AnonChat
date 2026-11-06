import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card } from '../../components/Card'
import { listRooms } from '../../lib/api/client'
import type { RoomInfo } from '../../lib/api/client'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { roomEmoji } from '../home/roomEmoji'

/** 聊天室列表（浅色）：进入拉取一次，之后每 15 秒刷新在线人数。 */
export default function RoomsPage() {
  const setToast = useUiStore((s) => s.setToast)
  const [rooms, setRooms] = useState<RoomInfo[]>([])

  useEffect(() => {
    let cancelled = false
    const load = () => {
      listRooms()
        .then((r) => {
          if (!cancelled) setRooms(r)
        })
        .catch((e) => setToast(errorMessage((e as { code?: string }).code)))
    }
    load()
    const t = setInterval(load, 15000)
    return () => {
      cancelled = true
      clearInterval(t)
    }
  }, [setToast])

  return (
    <main className="mx-auto max-w-[1200px] px-4 py-8">
      <h1 className="mb-6 text-2xl font-bold tracking-tight text-lobby-ink">话题房间</h1>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {rooms.map((r) => (
          <Link key={r.id} to={`/rooms/${r.id}`} className="group block">
            <Card className="flex h-full flex-col gap-3 p-5 transition duration-150 ease-out group-hover:-translate-y-0.5">
              <span
                aria-hidden
                className="flex h-11 w-11 items-center justify-center rounded-sm text-xl"
                style={{
                  background:
                    'linear-gradient(135deg, color-mix(in srgb, var(--color-brand) 15%, transparent), color-mix(in srgb, var(--color-accent) 15%, transparent))',
                }}
              >
                {roomEmoji(r)}
              </span>
              <span className="text-sm font-semibold text-lobby-ink">{r.name}</span>
              <span className="text-xs text-lobby-sub">{r.description}</span>
              <span className="mt-auto text-xs text-lobby-sub">{r.online} 人在线</span>
            </Card>
          </Link>
        ))}
      </div>
    </main>
  )
}
