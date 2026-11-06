import { useEffect, useRef } from 'react'
import { Avatar } from './Avatar'

export type MessageState = 'sending' | 'sent' | 'failed'

export interface DisplayMessage {
  id: number
  memberId: number
  nickname: string
  avatarSeed: string
  kind: string
  body: string
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
}

/** 消息列表：自己靠右、纯文本渲染、发送状态、上翻加载、焚毁倒计时、正在输入提示。 */
export function MessageList({ messages, selfMemberId, onRetry, onReachTop, typing }: MessageListProps) {
  const ref = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)
  const prevCount = useRef(0)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (messages.length > prevCount.current && atBottom.current) {
      el.scrollTop = el.scrollHeight
    }
    prevCount.current = messages.length
  }, [messages.length])

  function handleScroll() {
    const el = ref.current
    if (!el) return
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    if (el.scrollTop < 30) onReachTop?.()
  }

  return (
    <div
      ref={ref}
      onScroll={handleScroll}
      data-testid="message-list"
      className="flex-1 overflow-y-auto px-4 py-3 space-y-2"
    >
      {messages.map((m) => {
        const self = m.memberId === selfMemberId
        return (
          <div
            key={m.id}
            data-self={self ? 'true' : 'false'}
            className={`flex gap-2 items-end ${self ? 'flex-row-reverse ml-auto' : ''}`}
          >
            <Avatar seed={m.avatarSeed} size={28} />
            <div className={`max-w-[75%] ${self ? 'items-end text-right' : ''}`}>
              {!self && <div className="text-xs text-slate-500 mb-0.5">{m.nickname}</div>}
              {m.kind === 'system' ? (
                <p className="text-xs text-slate-400 whitespace-pre-wrap">{m.body}</p>
              ) : (
                <p className="whitespace-pre-wrap break-words rounded-2xl px-3 py-2 bg-slate-800 text-slate-100">
                  {m.body}
                </p>
              )}
              {m.burnCountdown !== undefined && m.burnCountdown > 0 && (
                <span className="text-xs text-amber-400">将在 {m.burnCountdown} 秒后焚毁</span>
              )}
              {m.state === 'sending' && <span className="text-xs text-slate-400">发送中</span>}
              {m.state === 'failed' && (
                <button
                  type="button"
                  onClick={() => onRetry?.(m.id)}
                  className="text-xs text-red-400 underline"
                >
                  发送失败，点击重试
                </button>
              )}
            </div>
          </div>
        )
      })}
      {typing && (
        <div className="text-xs text-slate-400" data-testid="typing">
          对方正在输入…
        </div>
      )}
    </div>
  )
}
