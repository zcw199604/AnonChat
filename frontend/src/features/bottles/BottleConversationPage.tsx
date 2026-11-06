import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useChat } from '../../lib/useChat'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { blockConversation, createReport } from '../../lib/api/client'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'
import { BlockConfirm } from '../../components/BlockConfirm'
import { ReportDialog } from '../../components/ReportDialog'

export default function BottleConversationPage() {
  const { conversationId } = useParams()
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)
  const chat = useChat(conversationId ?? null)

  const [ended, setEnded] = useState(false)
  const [blockOpen, setBlockOpen] = useState(false)
  const [reportOpen, setReportOpen] = useState(false)

  useEffect(() => {
    if (!conversationId) return
    ws.subscribeConversation(conversationId)
    const off = ws.on('conv.ended', (d) => {
      const x = d as { conversationId: string }
      if (x.conversationId === conversationId) setEnded(true)
    })
    return off
  }, [ws, conversationId])

  const peerMemberId = useMemo(() => {
    const m = chat.messages.find((x) => x.memberId !== chat.myMemberId)
    return m?.memberId ?? null
  }, [chat.messages, chat.myMemberId])

  const peerMessages = chat.messages
    .filter((m) => m.memberId !== chat.myMemberId)
    .map((m) => ({ id: m.id, body: m.body }))

  async function doBlock() {
    setBlockOpen(false)
    if (!conversationId) return
    try {
      await blockConversation(conversationId)
      setEnded(true)
      setToast('已屏蔽对方')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doReport(r: { messageIds: number[]; reason: string; note: string }) {
    setReportOpen(false)
    if (!conversationId || peerMemberId === null) return
    try {
      await createReport({ conversationId, memberId: peerMemberId, ...r })
      setToast('举报已提交')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  if (ended) {
    return <main className="mx-auto max-w-xl px-4 py-10 text-center text-slate-300">会话已结束</main>
  }

  return (
    <main className="mx-auto max-w-2xl px-4 py-4 flex flex-col h-[calc(100vh-56px)]">
      <div className="flex items-center justify-end gap-2 pb-2 border-b border-slate-800 text-sm">
        <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={() => setBlockOpen(true)}>
          屏蔽
        </button>
        <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={() => setReportOpen(true)}>
          举报
        </button>
      </div>
      <MessageList messages={chat.messages} selfMemberId={chat.myMemberId} />
      <Composer onSend={chat.send} />
      <BlockConfirm open={blockOpen} onCancel={() => setBlockOpen(false)} onConfirm={doBlock} />
      <ReportDialog open={reportOpen} peerMessages={peerMessages} onClose={() => setReportOpen(false)} onSubmit={doReport} />
    </main>
  )
}
