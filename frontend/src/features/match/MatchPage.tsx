import { useEffect, useReducer, useRef, useState } from 'react'
import { getWsClient } from '../../lib/ws'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { blockConversation, createReport } from '../../lib/api/client'
import { matchReducer, initialMatchState, endReasonText } from './machine'
import type { MatchPeer } from './machine'
import { useChat } from '../../lib/useChat'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'
import { Avatar } from '../../components/Avatar'
import { BlockConfirm } from '../../components/BlockConfirm'
import { ReportDialog } from '../../components/ReportDialog'

export default function MatchPage() {
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)
  const [state, dispatch] = useReducer(matchReducer, initialMatchState)
  const chat = useChat(state.conversationId ?? null)
  const selfMemberId = chat.myMemberId
  const peer = state.peer ?? chat.peer

  const [blockOpen, setBlockOpen] = useState(false)
  const [reportOpen, setReportOpen] = useState(false)

  const statusRef = useRef(state.status)
  useEffect(() => {
    statusRef.current = state.status
  }, [state.status])

  useEffect(() => {
    const offs = [
      ws.on('match.waiting', () => dispatch({ type: 'start' })),
      ws.on('match.found', (d) => {
        const x = d as { conversationId: string; peer: MatchPeer }
        ws.subscribeConversation(x.conversationId)
        dispatch({
          type: 'found',
          conversationId: x.conversationId,
          peer: x.peer,
        })
      }),
      ws.on('match.ended', (d) => {
        const x = d as { reason: string }
        dispatch({ type: 'ended', reason: x.reason })
      }),
      ws.on('conv.ended', (d) => {
        const x = d as { reason: string }
        dispatch({ type: 'ended', reason: x.reason })
      }),
      ws.on('hello', (d) => {
        const x = d as { resumable?: string[] }
        if (x.resumable && x.resumable.length > 0) {
          ws.subscribeConversation(x.resumable[0])
          dispatch({ type: 'resume', conversationId: x.resumable[0] })
        }
      }),
    ]
    return () => offs.forEach((f) => f())
  }, [ws])

  // 卸载时若仍在排队则取消排队；聊天中保留会话（依赖服务端宽限期）。
  useEffect(() => {
    return () => {
      if (statusRef.current === 'waiting') ws.send('match.cancel', {})
    }
  }, [ws])

  function startMatch() {
    dispatch({ type: 'start' })
    ws.send('match.start', {})
  }
  function cancel() {
    dispatch({ type: 'cancel' })
    ws.send('match.cancel', {})
  }
  function next() {
    dispatch({ type: 'next' })
    ws.send('match.next', {})
  }
  function leave() {
    dispatch({ type: 'leave' })
    ws.send('match.leave', {})
  }

  async function doBlock() {
    setBlockOpen(false)
    if (!state.conversationId) return
    try {
      await blockConversation(state.conversationId)
      dispatch({ type: 'ended', reason: 'blocked' })
      setToast('已屏蔽对方')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doReport(r: { messageIds: number[]; reason: string; note: string }) {
    setReportOpen(false)
    const peerMember = peer?.memberId
    if (!state.conversationId || peerMember === undefined) return
    try {
      await createReport({ conversationId: state.conversationId, memberId: peerMember, ...r })
      setToast('举报已提交')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  const peerMessages = chat.messages
    .filter((m) => m.memberId !== selfMemberId)
    .map((m) => ({ id: m.id, body: m.body }))

  if (state.status === 'idle') {
    return (
      <main className="mx-auto max-w-xl px-4 py-10 text-center">
        <h1 className="text-2xl font-bold mb-2">随机匹配</h1>
        <p className="text-slate-400 mb-8">和陌生人一对一随机聊天。</p>
        <button type="button" className="px-6 py-3 rounded-full bg-indigo-600 text-white text-lg" onClick={startMatch}>
          开始匹配
        </button>
      </main>
    )
  }

  if (state.status === 'waiting') {
    return (
      <main className="mx-auto max-w-xl px-4 py-10 text-center">
        <div className="animate-pulse text-5xl mb-6">🔍</div>
        <p className="text-slate-300 mb-8">正在为你寻找聊天对象…</p>
        <button type="button" className="px-4 py-2 rounded bg-slate-700" onClick={cancel}>
          取消匹配
        </button>
      </main>
    )
  }

  if (state.status === 'ended') {
    return (
      <main className="mx-auto max-w-xl px-4 py-10 text-center">
        <p className="text-lg text-slate-200 mb-6">{endReasonText(state.endReason)}</p>
        <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white" onClick={startMatch}>
          再来一个
        </button>
      </main>
    )
  }

  // chatting
  return (
    <main className="mx-auto max-w-2xl px-4 py-4 flex flex-col h-[calc(100vh-56px)]">
      <div className="flex items-center justify-between gap-2 pb-2 border-b border-slate-800">
        <div className="flex items-center gap-2">
          <Avatar seed={peer?.avatarSeed ?? ''} size={32} />
          <span className="text-sm text-slate-200">{peer?.nickname ?? '对方'}</span>
        </div>
        <div className="flex gap-2 text-sm">
          <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={next}>
            下一个
          </button>
          <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={leave}>
            离开
          </button>
          <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={() => setBlockOpen(true)}>
            屏蔽
          </button>
          <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={() => setReportOpen(true)}>
            举报
          </button>
        </div>
      </div>
      <MessageList messages={chat.messages} selfMemberId={selfMemberId} />
      <Composer onSend={chat.send} />
      <BlockConfirm open={blockOpen} name={peer?.nickname} onCancel={() => setBlockOpen(false)} onConfirm={doBlock} />
      <ReportDialog open={reportOpen} peerMessages={peerMessages} onClose={() => setReportOpen(false)} onSubmit={doReport} />
    </main>
  )
}
