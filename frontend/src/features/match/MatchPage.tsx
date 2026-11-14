import { Fragment, useCallback, useEffect, useReducer, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useUiStore } from '../../stores/ui'
import { useIdentityStore } from '../../stores/identity'
import { errorMessage } from '../../lib/api/errorMessages'
import { blockConversation, createReport, uploadMedia } from '../../lib/api/client'
import { matchReducer, initialMatchState, endReasonText, commonTags, icebreakers, peerSummary } from './machine'
import type { MatchAction, MatchState, MatchPeer, MatchFilters } from './machine'
import { useChat } from '../../lib/useChat'
import { MessageList } from '../../components/MessageList'
import { Icon } from '../../components/Icon'
import { Composer } from '../../components/Composer'
import { GlassPanel } from '../../components/GlassPanel'
import { GradientAvatar } from '../../components/GradientAvatar'
import { BlockConfirm } from '../../components/BlockConfirm'
import { ReportDialog } from '../../components/ReportDialog'
import { Sheet } from '../../components/Sheet'
import { SearchingView } from './SearchingView'
import { FoundOverlay, MatchReminder } from './FoundOverlay'
import { NextButton } from './NextButton'

export default function MatchPage() {
  const ws = getWsClient()
  const nav = useNavigate()
  const [params, setParams] = useSearchParams()
  const auto = params.get('auto') === '1'
  const setToast = useUiStore((s) => s.setToast)
  const nickname = useIdentityStore((s) => s.nickname)
  const avatarSeed = useIdentityStore((s) => s.avatarSeed)
  const me = useIdentityStore((s) => s.me)
  const myTags = me?.profile.tags ?? []
  // auto 首次渲染即为 waiting；失败快照仍是发起请求前的 idle。
  const [state, dispatch] = useReducer(matchReducer, { ...initialMatchState, status: auto ? 'waiting' : 'idle' })
  const stateRef = useRef(state)
  const apply = useCallback((action: MatchAction) => {
    stateRef.current = matchReducer(stateRef.current, action)
    dispatch(action)
  }, [])
  const beforeStart = useRef<MatchState>(initialMatchState)
  const pendingStartId = useRef<string | null>(null)
  const pendingNextId = useRef<string | null>(null)
  const autoStartedRef = useRef(false)
  const mountedRef = useRef(false)
  const [foundId, setFoundId] = useState<string | null>(null)
  const [overlayId, setOverlayId] = useState<string | null>(null)
  const [nextReset, setNextReset] = useState(0)
  const [menuOpen, setMenuOpen] = useState(false)
  const [endOpen, setEndOpen] = useState(false)
  const [blockOpen, setBlockOpen] = useState(false)
  const [reportOpen, setReportOpen] = useState(false)
  const chat = useChat(state.conversationId ?? null)
  const peer: MatchPeer | undefined = state.peer ?? chat.peer
  const name = peer?.nickname?.trim() || '对方'
  const seed = peer?.avatarSeed || 'anonymous'
  const summary = peerSummary(peer)
  const demographics = peerSummary(peer && { ...peer, province: null }).join(' · ')
  const cardChips = [demographics, peer?.province].filter(Boolean)
  const common = commonTags(peer?.tags ?? [], myTags)
  const chatting = state.status === 'chatting'
  const overlayOpen = chatting && overlayId === state.conversationId
  const closeOverlay = useCallback(() => setOverlayId(null), [])

  const startMatch = useCallback((snapshot?: MatchState) => {
    beforeStart.current = snapshot ?? stateRef.current
    apply({ type: 'start' })
    pendingNextId.current = null
    pendingStartId.current = ws.send('match.start', {})
  }, [apply, ws])

  useEffect(() => {
    function clearFound() { setFoundId(null); setOverlayId(null) }
    function clearPending() { pendingStartId.current = null; pendingNextId.current = null }
    const offs = [
      ws.on('match.waiting', (data) => {
        const x = data as { filters?: MatchFilters } | undefined
        clearFound()
        if (x?.filters) apply({ type: 'filters', filters: x.filters })
        apply({ type: 'start' })
        // waiting 先于真正入队，保留请求 id 以接收随后同 id 的入队错误。
      }),
      ws.on('match.found', (data) => {
        const x = data as { conversationId: string; peer: MatchPeer }
        clearPending()
        ws.subscribeConversation(x.conversationId)
        apply({ type: 'found', conversationId: x.conversationId, peer: x.peer })
        setFoundId(x.conversationId); setOverlayId(x.conversationId)
        setMenuOpen(false); setEndOpen(false); setBlockOpen(false); setReportOpen(false)
      }),
      ...['match.ended', 'conv.ended'].map((type) => ws.on(type, (data) => {
        const x = data as { conversationId?: string; reason: string }
        if (x.conversationId && x.conversationId !== stateRef.current.conversationId) return
        // next 会先结束旧会话再入队，必须保留 pendingNextId 直到后续 waiting/error。
        pendingStartId.current = null
        clearFound()
        apply({ type: 'ended', reason: x.reason })
      })),
      ws.on('hello', (data) => {
        const x = data as { resumable?: string[] }
        if (!x.resumable?.length) return
        clearPending(); clearFound()
        ws.subscribeConversation(x.resumable[0])
        apply({ type: 'resume', conversationId: x.resumable[0] })
      }),
      ws.on('error', (_data, env) => {
        if (env.id && env.id === pendingStartId.current && stateRef.current.status === 'waiting') {
          pendingStartId.current = null
          apply({ type: 'restore', state: beforeStart.current })
        } else if (env.id && env.id === pendingNextId.current) {
          pendingNextId.current = null
          if (stateRef.current.status === 'waiting') apply({ type: 'cancel' })
          else if (stateRef.current.status === 'chatting') setNextReset((value) => value + 1)
        }
      }),
    ]
    return () => offs.forEach((off) => off())
  }, [ws, apply])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      // StrictMode 的下一次 setup 会在 microtask 前恢复标志；真实卸载才取消。
      queueMicrotask(() => {
        if (!mountedRef.current && stateRef.current.status === 'waiting') ws.send('match.cancel', {})
      })
    }
  }, [ws])

  useEffect(() => {
    if (!auto || autoStartedRef.current) return
    autoStartedRef.current = true
    startMatch(initialMatchState)
    const next = new URLSearchParams(params)
    next.delete('auto')
    setParams(next, { replace: true })
  }, [auto, params, setParams, startMatch])

  function cancel() {
    pendingStartId.current = null; pendingNextId.current = null
    apply({ type: 'cancel' })
    ws.send('match.cancel', {})
    nav('/')
  }
  function leave() {
    pendingStartId.current = null; pendingNextId.current = null
    apply({ type: 'leave' })
    ws.send('match.leave', {})
    nav('/')
  }
  function next() {
    if (stateRef.current.status !== 'chatting') return
    pendingNextId.current = ws.send('match.next', {})
  }
  function canSend() {
    return stateRef.current.status === 'chatting' && stateRef.current.conversationId === state.conversationId
  }
  function send(body: string) { if (canSend()) chat.send(body) }
  async function sendMedia(file: File) {
    if (!canSend() || !state.conversationId) return
    const media = await uploadMedia(file)
    if (!canSend()) throw new Error('会话已结束或切换，请在当前会话重新发送')
    ws.sendMessage(state.conversationId, '', media)
  }
  async function doBlock() {
    setBlockOpen(false)
    if (!state.conversationId) return
    try {
      await blockConversation(state.conversationId)
      if (stateRef.current.conversationId === state.conversationId) {
        apply({ type: 'ended', reason: 'blocked' }); setFoundId(null); setOverlayId(null)
      }
      setToast('已屏蔽对方')
    } catch (e) { setToast(errorMessage((e as { code?: string }).code)) }
  }
  async function doReport(report: { messageIds: number[]; reason: string; note: string }) {
    setReportOpen(false)
    if (!state.conversationId || peer?.memberId === undefined) return
    try {
      await createReport({ conversationId: state.conversationId, memberId: peer.memberId, ...report })
      setToast('举报已提交')
    } catch (e) { setToast(errorMessage((e as { code?: string }).code)) }
  }

  if (state.status === 'idle' || state.status === 'waiting') {
    return <SearchingView key={state.status} waiting={state.status === 'waiting'} seed={avatarSeed} nickname={nickname}
      filters={state.filters} onStart={() => startMatch()} onCancel={cancel} onHome={() => nav('/')} />
  }
  const peerMessages = chat.messages.filter((m) => m.memberId !== chat.myMemberId && m.kind !== 'system').map((m) => ({ id: m.id, body: m.body }))
  const showIcebreakers = chatting && !chat.messages.some((m) => m.memberId === chat.myMemberId)
  const peerCard = (
    <div className="mb-4 rounded-[20px] border border-glass-line bg-[rgb(var(--c-ov-rgb)/0.04)] p-5 text-center">
      <GradientAvatar seed={seed} size={56} />
      <p className="mt-2 text-[17px] font-semibold text-night-ink">{name}</p>
      <div className="mt-2 flex flex-wrap justify-center gap-2 text-xs">
        {cardChips.map((item) => <span key={item} className="rounded-full bg-[rgb(var(--c-ov-rgb)/0.07)] px-2.5 py-1 text-night-sub">{item}</span>)}
        {peer?.tags?.map((tag) => <span key={tag} className="rounded-full bg-[rgb(var(--c-acc-rgb)/0.16)] px-2.5 py-1 text-chat-acc-soft">#{tag}</span>)}
      </div>
      {showIcebreakers && <>
        <p className="mt-4 text-xs text-night-sub">{common.length ? `你们都喜欢 ${common[0]}，打个招呼吧` : '打个招呼吧'}</p>
        <div className="mt-3 flex flex-wrap justify-center gap-2">
          {icebreakers(peer?.tags ?? [], myTags).map((text) => <button type="button" key={text} onClick={() => send(text)}
            className="h-9 rounded-full border border-[rgb(var(--c-acc-rgb)/0.4)] bg-[rgb(var(--c-acc-rgb)/0.1)] px-3 text-xs text-chat-acc-soft">{text}</button>)}
        </div>
      </>}
    </div>
  )
  return (
    <>
      <main inert={overlayOpen} className="mx-auto flex h-[100dvh] max-w-[760px] flex-col p-0 md:p-4">
        <GlassPanel className="relative h-full rounded-none md:rounded-panel">
          <header className="flex h-[60px] shrink-0 items-center gap-2 border-b border-glass-line px-2">
            <button type="button" aria-label="返回" onClick={() => chatting ? setEndOpen(true) : nav('/')} className="flex h-11 w-11 shrink-0 items-center justify-center text-night-ink"><Icon name="back" /></button>
            <GradientAvatar seed={seed} size={36} />
            <div className="min-w-0 flex-1">
              <p className="truncate text-[15px] font-semibold text-night-ink">{name}</p>
              <div className={`truncate text-xs ${chatting && chat.typing ? 'text-chat-acc' : 'text-night-sub'}`}>
                {!chatting ? '已离开' : chat.typing ? '正在输入…' : summary.map((item, i) => <Fragment key={item}>{i > 0 && <span aria-hidden="true" className="px-1">·</span>}<span>{item}</span></Fragment>)}
              </div>
            </div>
            {chatting && <NextButton key={`${state.status}:${state.conversationId}:${nextReset}`} onNext={next} />}
            <button type="button" aria-label="更多操作" onClick={() => setMenuOpen(true)} className="flex h-11 w-11 shrink-0 items-center justify-center text-night-ink"><Icon name="more" /></button>
          </header>
          <MessageList conversationKey={state.conversationId} messages={chat.messages} selfMemberId={chat.myMemberId}
            showSender={false} header={peerCard} typing={chatting && chat.typing}
            footer={!chatting ? <div className="my-4 flex justify-center"><p className="rounded-full bg-glass px-3 py-1.5 text-xs text-chat-sys">{endReasonText(state.endReason)}</p></div> : undefined} />
          {chatting ? <Composer key={state.conversationId} onSend={send} onTyping={() => { if (canSend()) chat.sendTyping() }} onSendMedia={sendMedia} /> : (
            <div className="border-t border-glass-line p-3 pb-[max(12px,env(safe-area-inset-bottom))]">
              <p className="mb-3 text-center text-xs text-night-sub">对话已结束，聊天记录仍可往上翻看</p>
              <div className="flex gap-3">
                <button type="button" onClick={() => nav('/')} className="h-12 flex-1 rounded-full border border-glass-line text-sm text-night-ink">返回大厅</button>
                <button type="button" onClick={() => startMatch()} className="flex h-12 flex-1 items-center justify-center gap-2 rounded-full bg-cta text-base font-bold text-white"><Icon name="shuffle" size={20} />再来一个</button>
              </div>
            </div>
          )}
        </GlassPanel>
        <Sheet open={menuOpen} title="更多操作" onClose={() => setMenuOpen(false)}>
          <div className="mb-5 text-center text-lobby-ink"><GradientAvatar seed={seed} size={56} /><p className="mt-2 font-semibold">{name}</p>
            <p className="mt-1 text-xs text-lobby-sub">{summary.join(' · ')} {peer?.tags?.map((tag) => `#${tag}`).join(' ')}</p></div>
          <button type="button" aria-label="举报" onClick={() => { setMenuOpen(false); setReportOpen(true) }} className="flex w-full items-center gap-3 rounded-sm p-3 text-left text-lobby-ink">
            <Icon name="flag" /><span><span className="block text-sm">举报</span><span className="block text-xs text-lobby-sub">勾选对方的消息并说明原因</span></span>
          </button>
          <button type="button" aria-label="屏蔽" onClick={() => { setMenuOpen(false); setBlockOpen(true) }} className="flex w-full items-center gap-3 rounded-sm p-3 text-left text-chat-danger">
            <Icon name="ban" /><span><span className="block text-sm">屏蔽</span><span className="block text-xs text-lobby-sub">屏蔽对方并结束这段对话</span></span>
          </button>
          <button type="button" onClick={() => setMenuOpen(false)} className="mt-3 h-11 w-full rounded-full border border-lobby-line text-sm text-lobby-sub">取消</button>
        </Sheet>
        <Sheet open={endOpen} title="结束这段对话？" onClose={() => setEndOpen(false)}>
          <p className="mb-4 text-sm text-lobby-sub">离开后对方会看到「对方已离开」，这段对话无法再继续。</p>
          <button type="button" onClick={leave} className="mb-3 h-12 w-full rounded-full bg-[rgb(var(--c-danger-rgb)/0.14)] text-sm text-chat-danger">结束聊天</button>
          <button type="button" onClick={() => setEndOpen(false)} className="h-12 w-full rounded-full border border-lobby-line text-sm text-lobby-ink">继续聊</button>
        </Sheet>
        <BlockConfirm open={blockOpen} name={name} onCancel={() => setBlockOpen(false)} onConfirm={doBlock} />
        <ReportDialog open={reportOpen} peerMessages={peerMessages} onClose={() => setReportOpen(false)} onSubmit={doReport} />
      </main>
      {chatting && foundId === state.conversationId && <MatchReminder key={`reminder:${foundId}`} />}
      {overlayOpen && <FoundOverlay key={`overlay:${overlayId}`} seed={avatarSeed} nickname={nickname} peer={peer} myTags={myTags} onClose={closeOverlay} />}
    </>
  )
}
