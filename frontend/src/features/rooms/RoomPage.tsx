import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useChat } from '../../lib/useChat'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { blockMember, createReport } from '../../lib/api/client'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'
import { GlassPanel } from '../../components/GlassPanel'
import { ReportDialog } from '../../components/ReportDialog'
import { BlockConfirm } from '../../components/BlockConfirm'

interface JoinAck {
  roomId: string
  conversationId: string
  memberId: number
  online: number
}

/** 房间聊天（how.md 2.3）：暗色玻璃，房间聊天继续显示昵称。 */
export default function RoomPage() {
  const { roomId } = useParams()
  const nav = useNavigate()
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)

  const [joined, setJoined] = useState<{ conversationId: string } | null>(null)
  const [online, setOnline] = useState(0)
  const [full, setFull] = useState(false)
  const [targetMember, setTargetMember] = useState<number | null>(null)
  const [reportOpen, setReportOpen] = useState(false)
  const [blockOpen, setBlockOpen] = useState(false)

  const chat = useChat(joined?.conversationId ?? null)

  useEffect(() => {
    if (!roomId) return
    let active = true
    ws.request('room.join', { roomId })
      .then((d) => {
        const a = d as JoinAck
        if (active) {
          setJoined({ conversationId: a.conversationId })
          setOnline(a.online)
        }
      })
      .catch((e) => {
        const code = (e as { code?: string }).code
        if (code === 'room_full') setFull(true)
        else setToast(errorMessage(code))
      })
    const off = ws.on('room.presence', (d) => {
      const x = d as { roomId: string; online: number }
      if (x.roomId === roomId) setOnline(x.online)
    })
    return () => {
      active = false
      off()
      ws.send('room.leave', { roomId })
    }
  }, [roomId, ws, setToast])

  const peers = useMemo(() => {
    const map = new Map<number, string>()
    for (const m of chat.messages) {
      if (m.memberId !== chat.myMemberId) map.set(m.memberId, m.nickname)
    }
    return [...map.entries()].map(([memberId, name]) => ({ memberId, name }))
  }, [chat.messages, chat.myMemberId])

  const peerMessages = chat.messages
    .filter((m) => targetMember !== null && m.memberId === targetMember)
    .map((m) => ({ id: m.id, body: m.body }))

  async function doBlock() {
    setBlockOpen(false)
    if (!joined || targetMember === null) return
    try {
      await blockMember(joined.conversationId, targetMember)
      setToast('已屏蔽该成员')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doReport(r: { messageIds: number[]; reason: string; note: string }) {
    setReportOpen(false)
    if (!joined || targetMember === null) return
    try {
      await createReport({ conversationId: joined.conversationId, memberId: targetMember, ...r })
      setToast('举报已提交')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  if (full) {
    return (
      <main className="flex min-h-screen items-center justify-center px-4 text-center">
        <div className="space-y-4">
          <p className="text-base text-night-ink">房间已满</p>
          <button
            type="button"
            onClick={() => nav('/rooms')}
            className="rounded-full bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out"
          >
            返回房间列表
          </button>
        </div>
      </main>
    )
  }

  return (
    <main className="mx-auto flex h-[100dvh] max-w-[760px] flex-col p-0 md:p-4">
      <GlassPanel className="h-full rounded-none md:rounded-panel">
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-glass-line px-4 py-3">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => nav('/rooms')}
              className="rounded-sm px-2 py-1 text-xs text-night-ink transition duration-150 ease-out hover:bg-glass"
            >
              返回列表
            </button>
            <span className="text-xs text-night-sub">房间在线 {online} 人</span>
          </div>
          <div className="flex items-center gap-1.5">
            <select
              aria-label="选择成员"
              className="rounded-sm border border-glass-line bg-glass px-2 py-1 text-xs text-night-ink"
              value={targetMember ?? ''}
              onChange={(e) => setTargetMember(e.target.value ? Number(e.target.value) : null)}
            >
              <option value="">选择成员…</option>
              {peers.map((p) => (
                <option key={p.memberId} value={p.memberId}>
                  {p.name}
                </option>
              ))}
            </select>
            <button
              type="button"
              className="rounded-sm px-2 py-1 text-xs text-night-ink transition duration-150 ease-out hover:bg-glass disabled:opacity-40"
              disabled={targetMember === null}
              onClick={() => setBlockOpen(true)}
            >
              屏蔽此人
            </button>
            <button
              type="button"
              className="rounded-sm px-2 py-1 text-xs text-night-ink transition duration-150 ease-out hover:bg-glass disabled:opacity-40"
              disabled={targetMember === null}
              onClick={() => setReportOpen(true)}
            >
              举报
            </button>
          </div>
        </header>
        <MessageList
          conversationKey={roomId}
          messages={chat.messages}
          selfMemberId={chat.myMemberId}
          typing={chat.typing}
        />
        <Composer onSend={chat.send} onTyping={chat.sendTyping} disabled={!joined} />
      </GlassPanel>
      <BlockConfirm open={blockOpen} onCancel={() => setBlockOpen(false)} onConfirm={doBlock} />
      <ReportDialog
        open={reportOpen}
        peerMessages={peerMessages}
        onClose={() => setReportOpen(false)}
        onSubmit={doReport}
      />
    </main>
  )
}
