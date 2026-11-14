import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useChat } from '../../lib/useChat'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { blockConversation, createReport } from '../../lib/api/client'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'
import { GlassPanel } from '../../components/GlassPanel'
import { BlockConfirm } from '../../components/BlockConfirm'
import { ReportDialog } from '../../components/ReportDialog'

/** 匿名信会话（how.md 2.3）：暗色玻璃，1v1 按产品决定显示对方真实昵称。 */
export default function BottleConversationPage() {
  const { conversationId } = useParams()
  const nav = useNavigate()
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
    return m?.memberId ?? chat.peer?.memberId ?? null
  }, [chat.messages, chat.myMemberId, chat.peer])

  const peerNickname = useMemo(() => {
    const fromPeer = chat.peer?.nickname?.trim()
    if (fromPeer) return fromPeer
    for (let i = chat.messages.length - 1; i >= 0; i--) {
      const m = chat.messages[i]
      if (m.memberId === chat.myMemberId) continue
      const nick = m.nickname?.trim()
      if (nick) return nick
    }
    return null
  }, [chat.messages, chat.myMemberId, chat.peer])

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
    return (
      <main className="flex min-h-screen items-center justify-center px-4 text-center">
        <div className="space-y-4">
          <p className="text-night-ink">会话已结束</p>
          <button
            type="button"
            onClick={() => nav('/bottles')}
            className="rounded-full bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out"
          >
            返回匿名信
          </button>
        </div>
      </main>
    )
  }

  // 1v1 会话显示对方真实昵称：优先会话详情里的 peer，其次最近一条对方消息，最后回退「对方」。
  // （conversationId 为空时不渲染会话界面，这里无需再判空。）
  const name = peerNickname ?? '对方'

  return (
    <main className="mx-auto flex h-screen max-w-[760px] flex-col p-0 md:p-4">
      <GlassPanel className="h-full rounded-none md:rounded-panel">
        <header className="flex items-center justify-between gap-2 border-b border-glass-line px-4 py-3">
          <div className="flex items-center gap-2">
            <button
              type="button"
              aria-label="返回匿名信"
              onClick={() => nav('/bottles')}
              className="h-8 w-8 rounded-full text-center text-sm text-white"
              style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
            >
              ←
            </button>
            <span className="text-sm font-medium text-night-ink">{name}</span>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <button
              type="button"
              className="rounded-sm px-2.5 py-1 text-xs text-night-ink transition duration-150 ease-out hover:bg-glass"
              onClick={() => setBlockOpen(true)}
            >
              屏蔽
            </button>
            <button
              type="button"
              className="rounded-sm px-2.5 py-1 text-xs text-night-ink transition duration-150 ease-out hover:bg-glass"
              onClick={() => setReportOpen(true)}
            >
              举报
            </button>
          </div>
        </header>
        <MessageList
          messages={chat.messages}
          selfMemberId={chat.myMemberId}
          typing={chat.typing}
        />
        <Composer onSend={chat.send} onTyping={chat.sendTyping} onSendMedia={chat.sendMedia} />
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
