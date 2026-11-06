/** 会话消息状态（Zustand）：有序去重、每会话上限 500、未读、发件箱、焚毁移除。 */
import { create } from 'zustand'

export interface StoredMessage {
  id: number
  memberId: number
  nickname: string
  avatarSeed: string
  kind: string
  body: string
  createdAt: string
  burnAfterRead?: boolean
}

const MAX_MESSAGES = 500

interface ConversationsState {
  messages: Record<string, StoredMessage[]>
  unread: Record<string, number>
  upsertMessages: (conversationId: string, incoming: StoredMessage[]) => void
  addMessage: (conversationId: string, msg: StoredMessage) => void
  setUnread: (conversationId: string, n: number) => void
  removeMessages: (conversationId: string, ids: number[]) => void
  clearAll: () => void
}

/** 合并并按 id 去重、升序；超出上限丢弃最旧。 */
function mergeMessages(list: StoredMessage[], incoming: StoredMessage[]): StoredMessage[] {
  const map = new Map<number, StoredMessage>()
  for (const m of list) map.set(m.id, m)
  for (const m of incoming) map.set(m.id, m)
  const merged = [...map.values()].sort((a, b) => a.id - b.id)
  return merged.length > MAX_MESSAGES ? merged.slice(merged.length - MAX_MESSAGES) : merged
}

export const useConversationsStore = create<ConversationsState>((set) => ({
  messages: {},
  unread: {},
  upsertMessages: (conversationId, incoming) =>
    set((s) => ({
      messages: {
        ...s.messages,
        [conversationId]: mergeMessages(s.messages[conversationId] ?? [], incoming),
      },
    })),
  addMessage: (conversationId, msg) =>
    set((s) => ({
      messages: {
        ...s.messages,
        [conversationId]: mergeMessages(s.messages[conversationId] ?? [], [msg]),
      },
    })),
  setUnread: (conversationId, n) =>
    set((s) => ({ unread: { ...s.unread, [conversationId]: n } })),
  removeMessages: (conversationId, ids) =>
    set((s) => {
      const drop = new Set(ids)
      const list = (s.messages[conversationId] ?? []).filter((m) => !drop.has(m.id))
      return { messages: { ...s.messages, [conversationId]: list } }
    }),
  clearAll: () => set({ messages: {}, unread: {} }),
}))
