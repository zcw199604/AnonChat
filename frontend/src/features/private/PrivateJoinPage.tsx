import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useChat } from '../../lib/useChat'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { getPrivateLink, joinPrivateLink, destroyPrivateLink } from '../../lib/api/client'
import type { PrivateLinkInfo } from '../../lib/api/client'
import { privateGate, PRIVATE_GATE_TEXT } from './gate'
import type { PrivateGate } from './gate'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'
import { GlassPanel } from '../../components/GlassPanel'
import { Card } from '../../components/Card'
import { codename } from '../../lib/codename'

/** 私密链接会话：进入会话后切换为暗色玻璃（how.md 1.3）。 */
export default function PrivateJoinPage() {
  const { token } = useParams()
  const nav = useNavigate()
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)
  const setInPrivateConversation = useUiStore((s) => s.setInPrivateConversation)

  const [info, setInfo] = useState<PrivateLinkInfo | null>(null)
  const [gate, setGate] = useState<PrivateGate | null>(null)
  const [password, setPassword] = useState('')
  const [conversationId, setConversationId] = useState<string | null>(null)
  const [destroyed, setDestroyed] = useState(false)
  const [confirmDestroy, setConfirmDestroy] = useState(false)

  const chat = useChat(conversationId)

  // 进入会话后主题切暗色，离开时恢复。
  useEffect(() => {
    setInPrivateConversation(Boolean(conversationId))
    return () => setInPrivateConversation(false)
  }, [conversationId, setInPrivateConversation])

  const join = useCallback(async () => {
    if (!token) return
    try {
      const r = await joinPrivateLink(token, { password: password || undefined })
      setConversationId(r.conversationId)
      ws.subscribeConversation(r.conversationId)
    } catch (e) {
      const code = (e as { code?: string }).code
      if (code === 'bad_password') {
        setGate('password')
        setToast(errorMessage('bad_password'))
      } else if (code === 'link_full') setGate('full')
      else if (code === 'link_expired') setGate('expired')
      else if (code === 'conversation_ended') setGate('destroyed')
      else setToast(errorMessage(code))
    }
  }, [token, password, ws, setToast])

  useEffect(() => {
    if (!token) return
    getPrivateLink(token)
      .then((i) => {
        setInfo(i)
        const g = privateGate(i.status, i.needPassword)
        if (g === 'expired' || g === 'destroyed' || g === 'full') {
          setGate(g)
          return
        }
        // 其他人走 join；创建者/已加入者 join 是幂等的（会直接返回会话），无需密码。
        void join()
      })
      .catch((e) => setToast(errorMessage((e as { code?: string }).code)))
    // 只在 token 变化时查询一次
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  useEffect(() => {
    if (!conversationId) return
    const off = ws.on('conv.ended', (d) => {
      const x = d as { conversationId: string; reason: string }
      if (x.conversationId === conversationId && x.reason === 'destroyed') setDestroyed(true)
    })
    return off
  }, [ws, conversationId])

  async function doDestroy() {
    setConfirmDestroy(false)
    if (!token) return
    try {
      await destroyPrivateLink(token)
      setDestroyed(true)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  if (destroyed) {
    return (
      <main className="flex min-h-screen items-center justify-center px-4 text-center">
        <div className="space-y-4">
          <p className="text-night-ink">会话已被销毁</p>
          <button
            type="button"
            onClick={() => nav('/')}
            className="rounded-full bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out"
          >
            返回大厅
          </button>
        </div>
      </main>
    )
  }

  if (gate === 'destroyed' || gate === 'expired' || gate === 'full') {
    return (
      <main className="mx-auto max-w-xl px-4 py-16 text-center">
        <p className="text-base text-lobby-ink">{PRIVATE_GATE_TEXT[gate]}</p>
      </main>
    )
  }

  if (conversationId) {
    const name = codename(conversationId, chat.peer?.memberId ?? 0)
    return (
      <main className="mx-auto flex h-screen max-w-[760px] flex-col p-0 md:p-4">
        <GlassPanel className="h-full rounded-none md:rounded-panel">
          <header className="flex items-center justify-between gap-2 border-b border-glass-line px-4 py-3">
            <span className="text-sm font-medium text-night-ink">{name}</span>
            <button
              type="button"
              className="rounded-sm px-2 py-1 text-xs text-danger transition duration-150 ease-out hover:bg-glass"
              onClick={() => setConfirmDestroy(true)}
            >
              销毁会话
            </button>
          </header>
          {info?.burnAfterRead && (
            <p className="mx-4 mt-3 rounded-sm bg-brand/15 px-3 py-2 text-[11px] text-night-ink">
              消息在对方阅读 10 秒后从双方界面消失；对方仍可截屏；服务器最多保留 7 天用于安全审计。
            </p>
          )}
          <MessageList
            messages={chat.messages}
            selfMemberId={chat.myMemberId}
            conversationId={conversationId}
            anonMode
            typing={chat.typing}
          />
          <Composer onSend={chat.send} onTyping={chat.sendTyping} onSendMedia={chat.sendMedia} />
        </GlassPanel>
        {confirmDestroy && (
          <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
            role="dialog"
            aria-label="销毁确认"
          >
            <div className="w-full max-w-sm rounded-panel bg-lobby-card p-5 shadow-soft">
              <p className="mb-4 text-sm text-lobby-ink">销毁后双方都无法再查看，确定销毁吗？</p>
              <div className="flex justify-end gap-2">
                <button
                  type="button"
                  className="rounded-sm px-3 py-1.5 text-sm text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg"
                  onClick={() => setConfirmDestroy(false)}
                >
                  取消
                </button>
                <button
                  type="button"
                  className="rounded-sm bg-danger px-3 py-1.5 text-sm text-white transition duration-150 ease-out"
                  onClick={doDestroy}
                >
                  确认销毁
                </button>
              </div>
            </div>
          </div>
        )}
      </main>
    )
  }

  // 需要密码或正在加入
  return (
    <main className="mx-auto max-w-xl px-4 py-16">
      <h1 className="mb-6 text-2xl font-bold tracking-tight text-lobby-ink">私密聊天</h1>
      <Card className="space-y-4 p-6">
        {gate === 'password' && (
          <>
            <p className="text-sm text-lobby-sub">该链接需要访问密码。</p>
            <input
              aria-label="访问密码"
              type="password"
              className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <button
              type="button"
              className="rounded-sm bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out"
              onClick={join}
            >
              加入
            </button>
          </>
        )}
        {gate === null && <p className="text-sm text-lobby-sub">加载中…</p>}
      </Card>
    </main>
  )
}
