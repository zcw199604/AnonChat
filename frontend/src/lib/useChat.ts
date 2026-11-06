import { useEffect, useMemo, useRef, useState } from 'react'
import { getWsClient } from './ws'
import { conversationMessages, getConversation } from './api/client'
import type { ConversationDetail, ConversationPeer } from './api/client'
import { useConversationsStore } from '../stores/conversations'
import type { DisplayMessage } from '../components/MessageList'

/** 订阅一个会话：加载历史与会话详情（取 myMemberId/peer）、接收实时消息、处理焚毁与已读上报。 */
export function useChat(conversationId: string | null) {
  const ws = getWsClient()
  const map = useConversationsStore((s) => s.messages)
  const upsert = useConversationsStore((s) => s.upsertMessages)
  const add = useConversationsStore((s) => s.addMessage)
  const remove = useConversationsStore((s) => s.removeMessages)
  const lastRead = useRef(0)
  const [detail, setDetail] = useState<ConversationDetail | null>(null)

  // 会话详情（myMemberId / peer）。
  useEffect(() => {
    if (!conversationId) return
    let cancelled = false
    getConversation(conversationId)
      .then((d) => {
        if (!cancelled) setDetail(d)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [conversationId])

  useEffect(() => {
    if (!conversationId) return
    let cancelled = false
    conversationMessages(conversationId, { limit: 50 })
      .then((msgs) => {
        if (!cancelled) upsert(conversationId, msgs)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [conversationId, upsert])

  useEffect(() => {
    if (!conversationId) return
    const offNew = ws.on('msg.new', (d) => {
      const x = d as { conversationId: string; message: unknown }
      if (x.conversationId === conversationId) add(conversationId, x.message as never)
    })
    const offBurn = ws.on('msg.burned', (d) => {
      const x = d as { conversationId: string; messageIds: number[] }
      if (x.conversationId === conversationId) remove(conversationId, x.messageIds)
    })
    return () => {
      offNew()
      offBurn()
    }
  }, [conversationId, ws, add, remove])

  const stored = useMemo(
    () => (conversationId ? (map[conversationId] ?? []) : []),
    [map, conversationId],
  )

  // 已读上报：仅上报最新消息 id，节流 1 秒。
  useEffect(() => {
    if (!conversationId || stored.length === 0) return
    const maxId = stored[stored.length - 1].id
    if (maxId <= lastRead.current) return
    const t = setTimeout(() => {
      lastRead.current = maxId
      ws.send('msg.read', { conversationId, messageId: maxId })
    }, 1000)
    return () => clearTimeout(t)
  }, [stored, conversationId, ws])

  const messages: DisplayMessage[] = useMemo(
    () =>
      stored.map((m) => ({
        id: m.id,
        memberId: m.memberId,
        nickname: m.nickname,
        avatarSeed: m.avatarSeed,
        kind: m.kind,
        body: m.body,
        createdAt: m.createdAt,
        burnAfterRead: m.burnAfterRead,
      })),
    [stored],
  )

  const currentDetail = detail && detail.id === conversationId ? detail : null
  const myMemberId = currentDetail?.myMemberId ?? 0
  const peer: ConversationPeer | undefined = currentDetail?.peer

  function send(body: string) {
    if (!conversationId) return
    ws.sendMessage(conversationId, body)
  }

  return { messages, myMemberId, peer, detail: currentDetail, send }
}
