import { useCallback, useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
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

export default function PrivateJoinPage() {
  const { token } = useParams()
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)

  const [info, setInfo] = useState<PrivateLinkInfo | null>(null)
  const [gate, setGate] = useState<PrivateGate | null>(null)
  const [password, setPassword] = useState('')
  const [conversationId, setConversationId] = useState<string | null>(null)
  const [destroyed, setDestroyed] = useState(false)
  const [confirmDestroy, setConfirmDestroy] = useState(false)

  const chat = useChat(conversationId)

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
      }
      else if (code === 'link_full') setGate('full')
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
    return <main className="mx-auto max-w-xl px-4 py-10 text-center text-slate-300">会话已被销毁</main>
  }

  if (gate === 'destroyed' || gate === 'expired' || gate === 'full') {
    return (
      <main className="mx-auto max-w-xl px-4 py-10 text-center">
        <p className="text-lg text-slate-200">{PRIVATE_GATE_TEXT[gate]}</p>
      </main>
    )
  }

  if (conversationId) {
    return (
      <main className="mx-auto max-w-2xl px-4 py-4 flex flex-col h-[calc(100vh-56px)]">
        {info?.burnAfterRead && (
          <p className="text-xs text-amber-400 bg-amber-950/40 rounded px-3 py-2 mb-2">
            消息在对方阅读 10 秒后从双方界面消失；对方仍可截屏；服务器最多保留 7 天用于安全审计。
          </p>
        )}
        <div className="flex justify-end pb-2 border-b border-slate-800">
          <button type="button" className="px-3 py-1 rounded bg-red-800 text-sm" onClick={() => setConfirmDestroy(true)}>
            销毁会话
          </button>
        </div>
        <MessageList messages={chat.messages} selfMemberId={chat.myMemberId} />
        <Composer onSend={chat.send} />
        {confirmDestroy && (
          <div className="fixed inset-0 bg-black/60 flex items-center justify-center p-4 z-50" role="dialog" aria-label="销毁确认">
            <div className="bg-slate-900 rounded-lg p-4 w-full max-w-sm">
              <p className="mb-4 text-slate-200">销毁后双方都无法再查看，确定销毁吗？</p>
              <div className="flex justify-end gap-2">
                <button type="button" className="px-3 py-1 rounded bg-slate-700" onClick={() => setConfirmDestroy(false)}>
                  取消
                </button>
                <button type="button" className="px-3 py-1 rounded bg-red-600" onClick={doDestroy}>
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
    <main className="mx-auto max-w-xl px-4 py-10 space-y-4">
      <h1 className="text-2xl font-bold">私密聊天</h1>
      {gate === 'password' && (
        <>
          <p className="text-slate-400">该链接需要访问密码。</p>
          <input aria-label="访问密码" type="password" className="w-full bg-slate-800 rounded px-3 py-2" value={password} onChange={(e) => setPassword(e.target.value)} />
          <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white" onClick={join}>
            加入
          </button>
        </>
      )}
      {gate === null && <p className="text-slate-400">加载中…</p>}
    </main>
  )
}
