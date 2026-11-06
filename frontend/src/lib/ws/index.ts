import { WsClient } from './client'
import { conversationMessages, roomMessages } from '../api/client'

let instance: WsClient | null = null

/** 全局唯一的 WS 客户端单例。 */
export function getWsClient(): WsClient {
  if (!instance) {
    instance = new WsClient({
      resume: {
        getConversationMessages: (conversationId, after) =>
          conversationMessages(conversationId, { after, limit: 50 }),
        getRoomMessages: (roomId) => roomMessages(roomId, { limit: 50 }),
      },
    })
  }
  return instance
}

/** 测试用：重置单例。 */
export function resetWsClient(): void {
  instance?.close()
  instance = null
}
