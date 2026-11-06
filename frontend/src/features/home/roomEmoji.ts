import type { RoomInfo } from '../../lib/api/client'

/** slug → emoji 的前端回退映射（U1 使用；U4 起优先后端返回的 emoji 字段）。 */
const SLUG_EMOJI: Record<string, string> = {
  lobby: '💬',
  'late-night': '🌙',
  gaming: '🎮',
  study: '📚',
  emotion: '💗',
  work: '💼',
}

/** 取房间 emoji：优先后端字段，其次按 slug 映射，最后回退 💬。 */
export function roomEmoji(room: Pick<RoomInfo, 'emoji' | 'slug'>): string {
  if (room.emoji) return room.emoji
  return SLUG_EMOJI[room.slug] ?? '💬'
}
