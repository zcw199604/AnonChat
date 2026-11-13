import { useEffect, useReducer, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { blockConversation, createReport } from '../../lib/api/client'
import { matchReducer, initialMatchState, endReasonText, filterChips, peerChips } from './machine'
import type { MatchPeer, MatchFilters } from './machine'
import { useChat } from '../../lib/useChat'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'
import { GlassPanel } from '../../components/GlassPanel'
import { BlockConfirm } from '../../components/BlockConfirm'
import { ReportDialog } from '../../components/ReportDialog'
import { codename } from '../../lib/codename'
import { WaitingOrb } from './WaitingOrb'

/** 排队满 30 秒且有硬条件时给出放宽建议（how.md 2.3）。 */
const RELAX_HINT_SECONDS = 30

export default function MatchPage() {
  const ws = getWsClient()
  const nav = useNavigate()
  const [params] = useSearchParams()
  const setToast = useUiStore((s) => s.setToast)
  const [state, dispatch] = useReducer(matchReducer, initialMatchState)
  const chat = useChat(state.conversationId ?? null)
  const selfMemberId = chat.myMemberId
  const peer = state.peer

  const [blockOpen, setBlockOpen] = useState(false)
  const [reportOpen, setReportOpen] = useState(false)
  const [elapsed, setElapsed] = useState(0)

  const statusRef = useRef(state.status)
  useEffect(() => {
    statusRef.current = state.status
  }, [state.status])

  useEffect(() => {
    const offs = [
      ws.on('match.waiting', (d) => {
        const x = d as { filters?: MatchFilters } | undefined
        if (x?.filters) dispatch({ type: 'filters', filters: x.filters })
        dispatch({ type: 'start' })
      }),
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

  // /match?auto=1：从大厅点「匹配」进入时自动开始匹配。
  // 服务端的 match.start 是幂等的（同一身份重复入队以最后一次为准），
  // 因此 StrictMode 下的二次挂载不会造成重复排队。
  const auto = params.get('auto') === '1'
  useEffect(() => {
    if (!auto) return
    ws.send('match.start', {})
  }, [auto, ws])

  // 排队计时：进入 waiting 时从 0 开始每秒刷新。
  useEffect(() => {
    if (state.status !== 'waiting') return
    const startedAt = Date.now()
    const reset = window.setTimeout(() => setElapsed(0), 0)
    const t = window.setInterval(() => setElapsed(Math.floor((Date.now() - startedAt) / 1000)), 1000)
    return () => {
      window.clearTimeout(reset)
      window.clearInterval(t)
    }
  }, [state.status])

  function startMatch() {
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

  const chips = peerChips(peer)
  const activeFilters = filterChips(state.filters)
  const hasHardFilter =
    Boolean(state.filters?.cleanMode) ||
    (state.filters?.gender ?? 'any') !== 'any' ||
    Boolean(state.filters?.sameProvince)

  if (state.status === 'idle') {
    return (
      <main className="flex min-h-[calc(100vh-0px)] items-center justify-center px-4 py-10">
        <div className="flex flex-col items-center gap-4 text-center">
          <h1 className="text-xl font-bold text-night-ink">随机匹配</h1>
          <p className="text-sm text-night-sub">和陌生人一对一随机聊天。</p>
          <button
            type="button"
            onClick={startMatch}
            className="h-32 w-32 rounded-full text-base font-semibold text-white transition duration-150 ease-out"
            style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
          >
            开始匹配
          </button>
          <button
            type="button"
            onClick={() => nav('/')}
            className="text-sm text-night-sub transition duration-150 ease-out hover:text-night-ink"
          >
            返回大厅
          </button>
        </div>
      </main>
    )
  }

  if (state.status === 'waiting') {
    return (
      <main className="flex min-h-screen items-center justify-center px-4 py-10">
        <div className="flex flex-col items-center gap-5 text-center">
          <WaitingOrb />
          <div className="space-y-1">
            <p className="text-base font-medium text-night-ink">正在寻找…</p>
            <p className="text-xs text-night-sub">已等待 {elapsed} 秒</p>
          </div>
          {activeFilters.length > 0 && (
            <div className="flex flex-wrap justify-center gap-2">
              {activeFilters.map((c) => (
                <span key={c} className="rounded-full bg-glass px-3 py-1 text-xs text-night-ink">
                  {c}
                </span>
              ))}
            </div>
          )}
          {elapsed >= RELAX_HINT_SECONDS && hasHardFilter && (
            <p className="text-xs text-night-sub">当前条件较严格，可以返回大厅调整</p>
          )}
          <button
            type="button"
            onClick={cancel}
            className="rounded-full border border-glass-line bg-glass px-5 py-2 text-sm text-night-ink transition duration-150 ease-out"
          >
            取消匹配
          </button>
        </div>
      </main>
    )
  }

  if (state.status === 'ended') {
    return (
      <main className="flex min-h-screen items-center justify-center px-4 py-10">
        <div className="flex flex-col items-center gap-4 text-center">
          <p className="text-base text-night-ink">{endReasonText(state.endReason)}</p>
          <button
            type="button"
            onClick={startMatch}
            className="rounded-full px-6 py-2 text-sm font-medium text-white transition duration-150 ease-out"
            style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
          >
            再来一个
          </button>
          <button
            type="button"
            onClick={() => nav('/')}
            className="text-sm text-night-sub transition duration-150 ease-out hover:text-night-ink"
          >
            返回大厅
          </button>
        </div>
      </main>
    )
  }

  const peerName = state.conversationId ? codename(state.conversationId, peer?.memberId ?? 0) : '对方'

  return (
    <main className="mx-auto flex h-screen max-w-[760px] flex-col p-0 md:p-4">
      <GlassPanel className="h-full rounded-none md:rounded-panel">
        <header className="flex items-center justify-between gap-2 border-b border-glass-line px-4 py-3">
          <div className="flex min-w-0 items-center gap-2">
            <button
              type="button"
              aria-label="返回大厅"
              onClick={() => nav('/')}
              className="h-8 w-8 shrink-0 rounded-full text-center text-sm text-white"
              style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
            >
              ←
            </button>
            <div className="min-w-0">
              <div className="truncate text-sm font-medium text-night-ink">{peerName}</div>
              {chips.length > 0 && (
                <div className="flex flex-wrap gap-x-1.5 text-[11px] text-night-sub">
                  {chips.map((c) => (
                    <span key={c}>{c}</span>
                  ))}
                </div>
              )}
            </div>
          </div>
          <div className="hidden shrink-0 items-center gap-1.5 md:flex">
            <ActionButtons
              onNext={next}
              onLeave={leave}
              onBlock={() => setBlockOpen(true)}
              onReport={() => setReportOpen(true)}
            />
          </div>
          <details className="relative shrink-0 md:hidden">
            <summary className="cursor-pointer list-none rounded-sm px-2 py-1 text-night-ink">⋯</summary>
            <div className="absolute right-0 z-10 mt-1 flex flex-col gap-1 rounded-card border border-glass-line bg-night-to p-2">
              <ActionButtons
                onNext={next}
                onLeave={leave}
                onBlock={() => setBlockOpen(true)}
                onReport={() => setReportOpen(true)}
              />
            </div>
          </details>
        </header>
        <MessageList
          messages={chat.messages}
          selfMemberId={selfMemberId}
          conversationId={state.conversationId}
          anonMode
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

function ActionButtons({
  onNext,
  onLeave,
  onBlock,
  onReport,
}: {
  onNext: () => void
  onLeave: () => void
  onBlock: () => void
  onReport: () => void
}) {
  const cls =
    'rounded-sm px-2.5 py-1 text-xs text-night-ink transition duration-150 ease-out hover:bg-glass'
  return (
    <>
      <button type="button" className={cls} onClick={onNext}>
        下一个
      </button>
      <button type="button" className={cls} onClick={onLeave}>
        离开
      </button>
      <button type="button" className={cls} onClick={onBlock}>
        屏蔽
      </button>
      <button type="button" className={cls} onClick={onReport}>
        举报
      </button>
    </>
  )
}
