import { useEffect, useRef, useState, type ReactNode } from 'react'
import { GradientAvatar } from './GradientAvatar'
import { Bubble } from './Bubble'
import { TypingDots } from './TypingDots'
import { Icon } from './Icon'
import { senderLabel } from './senderLabel'

export type MessageState = 'sending' | 'sent' | 'failed'

export interface DisplayMessage {
  id: number
  memberId: number
  nickname: string
  avatarSeed: string
  kind: string
  body: string
  /** 媒体消息的访问地址（/api/media/<id>），缺失时显示「媒体已失效」。 */
  mediaUrl?: string
  createdAt?: string
  state?: MessageState
  /** 阅后即焚倒计时（秒），由父组件计算；<=0 或未设置时不显示。 */
  burnCountdown?: number
}

interface MessageListProps {
  messages: DisplayMessage[]
  selfMemberId: number
  onRetry?: (id: number) => void
  onReachTop?: () => void
  typing?: boolean
  conversationKey?: string
  showSender?: boolean
  header?: ReactNode
  footer?: ReactNode
}

function sameGroup(a: DisplayMessage | undefined, b: DisplayMessage | undefined): boolean {
  if (!a || !b || a.kind === 'system' || b.kind === 'system' || a.memberId !== b.memberId) return false
  return !a.createdAt || !b.createdAt || Math.abs(Date.parse(b.createdAt) - Date.parse(a.createdAt)) <= 180_000
}

function messageTime(value: string): string {
  const date = new Date(value)
  const today = new Date()
  const yesterday = new Date(today)
  yesterday.setDate(today.getDate() - 1)
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
  if (date.toDateString() === today.toDateString()) return time
  if (date.toDateString() === yesterday.toDateString()) return `昨天 ${time}`
  return `${date.getMonth() + 1}月${date.getDate()}日 ${time}`
}

/** 媒体宽度相对消息行计算，最大 60% / 240px，不受文本气泡宽度限制。 */
function MediaContent({ kind, mediaUrl }: { kind: string; mediaUrl?: string }) {
  if (!mediaUrl) return <span className="text-[13px] text-night-sub">[媒体已失效]</span>
  const className = 'max-w-full rounded-card border border-glass-line'
  if (kind === 'video') {
    return <video src={mediaUrl} controls preload="metadata" playsInline className={className} />
  }
  return (
    <a href={mediaUrl} target="_blank" rel="noopener noreferrer" aria-label="查看图片" className="min-w-0">
      <img src={mediaUrl} alt="图片消息" loading="lazy" className={className} />
    </a>
  )
}

export function MessageList({
  messages, selfMemberId, onRetry, onReachTop, typing,
  conversationKey, showSender = true, header, footer,
}: MessageListProps) {
  const ref = useRef<HTMLDivElement>(null)
  const maxId = messages.reduce((max, m) => Math.max(max, m.id), 0)
  const [seen, setSeen] = useState({ key: conversationKey, maxId, unread: 0, atBottom: true })

  // 在同一次渲染中按会话和 id 更新水位；删除、历史加载不能降低水位或重置未读。
  if (seen.key !== conversationKey) {
    setSeen({ key: conversationKey, maxId, unread: 0, atBottom: true })
  } else if (maxId > seen.maxId) {
    const added = messages.filter((m) => m.id > seen.maxId)
    const atBottom = seen.atBottom || added.some((m) => m.memberId === selfMemberId)
    const unread = atBottom ? 0 : seen.unread + added.filter((m) => m.memberId !== selfMemberId).length
    setSeen({ ...seen, maxId, unread, atBottom })
  }

  useEffect(() => {
    const el = ref.current
    if (el && seen.atBottom) el.scrollTop = el.scrollHeight
  }, [seen.maxId, seen.key, seen.atBottom, typing, footer])

  function scrollToBottom() {
    const el = ref.current
    if (el) el.scrollTop = el.scrollHeight
    setSeen((prev) => ({ ...prev, unread: 0, atBottom: true }))
  }

  function handleScroll() {
    const el = ref.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    setSeen((prev) => {
      const unread = atBottom ? 0 : prev.unread
      return prev.atBottom === atBottom && prev.unread === unread ? prev : { ...prev, atBottom, unread }
    })
    if (el.scrollTop < 30) onReachTop?.()
  }

  const typingSeed = [...messages].reverse().find((m) => m.memberId !== selfMemberId && m.kind !== 'system')?.avatarSeed || 'anonymous'

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div ref={ref} onScroll={handleScroll} data-testid="message-list"
        className="min-h-0 flex-1 overflow-y-auto px-3 py-4 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {header}
        {messages.map((m, i) => {
          const self = m.memberId === selfMemberId
          const previous = messages[i - 1]
          const first = !sameGroup(previous, m)
          const last = !sameGroup(m, messages[i + 1])
          const showTime = m.createdAt && (i === 0 || (previous?.createdAt && Date.parse(m.createdAt) - Date.parse(previous.createdAt) > 300_000))
          const media = m.kind === 'image' || m.kind === 'video'
          const bubble = (body: string) => (
            <Bubble self={self} first={first} last={last} className={!media && m.state === 'sending' ? 'opacity-60' : ''}>{body}</Bubble>
          )
          return (
            <div key={m.id}>
              {showTime && <p className="my-3 text-center text-xs text-night-sub">{messageTime(m.createdAt!)}</p>}
              {m.kind === 'system' ? (
                <div className="my-3 flex justify-center">
                  <p className="whitespace-pre-wrap rounded-full bg-glass px-3 py-1.5 text-xs text-chat-sys">{m.body}</p>
                </div>
              ) : (
                <div data-self={self ? 'true' : 'false'}
                  className={`flex items-end gap-2 ${first ? 'mt-3' : 'mt-[3px]'} ${self ? 'justify-end' : ''}`}>
                  {!self && (last && !(typing && i === messages.length - 1)
                    ? <GradientAvatar seed={m.avatarSeed || 'anonymous'} size={28} />
                    : <span aria-hidden="true" className="w-7 shrink-0" />)}
                  {m.state === 'failed' && (
                    <button type="button" aria-label="发送失败，点击重试" onClick={() => onRetry?.(m.id)}
                      className="flex h-8 w-8 shrink-0 items-center justify-center text-chat-danger">
                      <Icon name="alert" size={20} />
                    </button>
                  )}
                  <div className={`flex min-w-0 flex-col gap-1 ${media ? 'max-w-[min(60%,240px)]' : 'max-w-[75%]'} ${self ? 'items-end' : 'items-start'}`}>
                    {showSender && !self && first && <span className="text-[11px] text-night-sub">{senderLabel(m.nickname, false)}</span>}
                    {media ? (
                      <div className={`flex min-w-0 flex-col gap-1 ${m.state === 'sending' ? 'opacity-60' : ''}`}>
                        <MediaContent kind={m.kind} mediaUrl={m.mediaUrl} />
                        {m.body.trim() !== '' && bubble(m.body)}
                      </div>
                    ) : bubble(m.body)}
                    {m.burnCountdown !== undefined && m.burnCountdown > 0 && (
                      <span className="text-[11px] text-chat-acc">将在 {m.burnCountdown} 秒后焚毁</span>
                    )}
                    {m.state === 'sending' && <span className="sr-only">发送中</span>}
                  </div>
                </div>
              )}
            </div>
          )
        })}
        {typing && (
          <div data-testid="typing" className="mt-3 flex items-end gap-2">
            <GradientAvatar seed={typingSeed} size={28} />
            <div className="rounded-[18px_18px_18px_6px] border border-glass-line bg-bubble-peer px-3.5 py-[9px]">
              <TypingDots dotsOnly />
            </div>
          </div>
        )}
        {footer}
      </div>
      {seen.unread > 0 && (
        <button type="button" aria-label={`${seen.unread} 条新消息`} onClick={scrollToBottom}
          className="absolute right-3 bottom-3 flex h-8 items-center gap-1 rounded-full bg-bubble-self px-3 text-xs text-white">
          <Icon name="arrow-down" size={14} />{seen.unread} 条新消息
        </button>
      )}
    </div>
  )
}
