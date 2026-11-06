/** 随机匹配页状态机（纯函数，便于单测）。 */

export type MatchStatus = 'idle' | 'waiting' | 'chatting' | 'ended'

/** 对方的公开资料芯片（how.md 3.4：match.found 去掉了 nickname 和 avatarSeed）。 */
export interface MatchPeer {
  memberId: number
  gender?: 'male' | 'female' | null
  age?: number | null
  province?: string | null
  tags?: string[]
  cleanMode?: boolean
}

/** 服务端最终生效的匹配条件（match.waiting.filters）。 */
export interface MatchFilters {
  cleanMode: boolean
  gender: 'any' | 'male' | 'female'
  sameProvince: boolean
}

export interface MatchState {
  status: MatchStatus
  conversationId?: string
  peer?: MatchPeer
  endReason?: string
  filters?: MatchFilters
}

export type MatchAction =
  | { type: 'start' }
  | { type: 'cancel' }
  | { type: 'found'; conversationId: string; peer: MatchPeer }
  | { type: 'next' }
  | { type: 'leave' }
  | { type: 'ended'; reason: string }
  | { type: 'resume'; conversationId: string }
  | { type: 'filters'; filters: MatchFilters }
  | { type: 'reset' }

export const initialMatchState: MatchState = { status: 'idle' }

export function matchReducer(state: MatchState, action: MatchAction): MatchState {
  switch (action.type) {
    case 'start':
      return { status: 'waiting', filters: state.filters }
    case 'cancel':
      return { status: 'idle', filters: state.filters }
    case 'found':
      return {
        status: 'chatting',
        conversationId: action.conversationId,
        peer: action.peer,
        filters: state.filters,
      }
    case 'next':
      return { status: 'waiting', filters: state.filters }
    case 'leave':
      return { status: 'idle', filters: state.filters }
    case 'ended':
      return { ...state, status: 'ended', endReason: action.reason }
    case 'resume':
      return { status: 'chatting', conversationId: action.conversationId }
    case 'filters':
      return { ...state, filters: action.filters }
    case 'reset':
      return { status: 'idle' }
    default:
      return state
  }
}

const REASON_TEXT: Record<string, string> = {
  peer_left: '对方已离开',
  peer_skipped: '对方跳过了你',
  blocked: '会话已结束',
  expired: '会话已过期',
  destroyed: '会话已销毁',
}

/** 匹配结束原因文案。 */
export function endReasonText(reason?: string): string {
  if (reason && REASON_TEXT[reason]) return REASON_TEXT[reason]
  return '会话已结束'
}

/** 生效条件的芯片文案（how.md 2.3）。 */
export function filterChips(filters?: MatchFilters): string[] {
  if (!filters) return []
  const out: string[] = []
  if (filters.cleanMode) out.push('清流')
  if (filters.gender === 'male') out.push('只匹配男生')
  if (filters.gender === 'female') out.push('只匹配女生')
  if (filters.sameProvince) out.push('同省')
  return out
}

/** 对方的公开资料芯片文案。 */
export function peerChips(peer?: MatchPeer): string[] {
  if (!peer) return []
  const out: string[] = []
  if (peer.gender === 'female') out.push('♀')
  if (peer.gender === 'male') out.push('♂')
  if (typeof peer.age === 'number') out.push(`${peer.age}`)
  if (peer.province) out.push(peer.province)
  for (const tag of peer.tags ?? []) out.push(`#${tag}`)
  return out
}
