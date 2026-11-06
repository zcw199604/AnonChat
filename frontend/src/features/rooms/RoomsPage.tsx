import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listRooms } from '../../lib/api/client'
import type { RoomInfo } from '../../lib/api/client'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'

/** 聊天室列表：进入拉取一次，之后每 15 秒刷新在线人数。 */
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
    <main className="mx-auto max-w-3xl px-4 py-6">
      <h1 className="text-2xl font-bold mb-4">聊天室</h1>
      <div className="space-y-2">
        {rooms.map((r) => (
          <Link key={r.id} to={`/rooms/${r.id}`} className="block rounded-lg bg-slate-900 hover:bg-slate-800 p-4 border border-slate-800">
            <div className="flex items-center justify-between">
              <span className="font-semibold">{r.name}</span>
              <span className="text-sm text-slate-400">{r.online} 人在线</span>
            </div>
            <p className="text-sm text-slate-400 mt-1">{r.description}</p>
          </Link>
        ))}
      </div>
    </main>
  )
}
