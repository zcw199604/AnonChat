import { useEffect, useRef } from 'react'
import { GradientAvatar } from './GradientAvatar'
import { Bubble } from './Bubble'
import { TypingDots } from './TypingDots'
import { codename } from '../lib/codename'

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
  /** 会话 ID：1v1 场景用它计算匿名代号。 */
  conversationId?: string | null
  /**
   * 是否用匿名代号替代昵称（match / bottle / private 为 true；房间为 false；
   * 故事会话由服务端下发角色名，保持 false）。
   */
  anonMode?: boolean
}

/**
 * 消息列表：自己靠右、纯文本渲染、发送状态、上翻加载、焚毁倒计时、正在输入提示。
 * 1v1 场景不显示昵称，改为显示匿名代号；自己在 1v1 场景显示为「我」。
 */
/** 媒体内容：图片懒加载并支持点击查看原图，视频用原生播放器（支持进度条拖动）。 */
function MediaContent({ kind, mediaUrl }: { kind: string; mediaUrl?: string }) {
  if (!mediaUrl) {
    return <span className="text-[13px] text-night-sub">[媒体已失效]</span>
  }
  if (kind === 'video') {
    return (
      <video
        src={mediaUrl}
        controls
        preload="metadata"
        playsInline
        className="max-w-[240px] rounded-card border border-glass-line"
      />
    )
  }
  return (
    <a href={mediaUrl} target="_blank" rel="noopener noreferrer" aria-label="查看图片">
      <img
        src={mediaUrl}
        alt="图片消息"
        loading="lazy"
        className="max-w-[240px] rounded-card border border-glass-line"
      />
    </a>
  )
}

export function MessageList({
  messages,
  selfMemberId,
  onRetry,
  onReachTop,
  typing,
  conversationId,
  anonMode,
}: MessageListProps) {
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

  function label(m: DisplayMessage, self: boolean): string {
    if (!anonMode) return m.nickname
    if (self) return '我'
    return codename(conversationId ?? '', m.memberId)
  }

  return (
    <div
      ref={ref}
      onScroll={handleScroll}
      data-testid="message-list"
      className="flex-1 space-y-3 overflow-y-auto px-4 py-4"
    >
      {messages.map((m) => {
        const self = m.memberId === selfMemberId
        return (
          <div
            key={m.id}
            data-self={self ? 'true' : 'false'}
            className={`flex items-end gap-2 ${self ? 'ml-auto flex-row-reverse' : ''}`}
          >
            <GradientAvatar seed={m.avatarSeed || 'anonymous'} size={28} />
            <div className={`flex max-w-[85%] flex-col gap-1 ${self ? 'items-end text-right' : ''}`}>
              {!self && (
                <span className="text-[11px] text-night-sub">{label(m, self)}</span>
              )}
              {m.kind === 'system' ? (
                <p className="whitespace-pre-wrap text-xs text-night-sub">{m.body}</p>
              ) : m.kind === 'image' || m.kind === 'video' ? (
                <>
                  <MediaContent kind={m.kind} mediaUrl={m.mediaUrl} />
                  {/* 图片/视频消息的正文通常为空：只有真的带了文字才渲染文本气泡。 */}
                  {m.body.trim() !== '' && <Bubble self={self}>{m.body}</Bubble>}
                </>
              ) : (
                <Bubble self={self}>{m.body}</Bubble>
              )}
              {m.burnCountdown !== undefined && m.burnCountdown > 0 && (
                <span className="text-[11px] text-brand">将在 {m.burnCountdown} 秒后焚毁</span>
              )}
              {m.state === 'sending' && <span className="text-[11px] text-night-sub">发送中</span>}
              {m.state === 'failed' && (
                <button
                  type="button"
                  onClick={() => onRetry?.(m.id)}
                  className="text-[11px] text-danger underline"
                >
                  发送失败，点击重试
                </button>
              )}
            </div>
          </div>
        )
      })}
      {typing && (
        <div data-testid="typing">
          <TypingDots />
        </div>
      )}
    </div>
  )
}
