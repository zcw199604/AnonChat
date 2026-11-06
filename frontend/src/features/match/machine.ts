/** 随机匹配页状态机（纯函数，便于单测）。 */

export type MatchStatus = 'idle' | 'waiting' | 'chatting' | 'ended'

export interface MatchPeer {
  memberId: number
  nickname: string
  avatarSeed: string
}

export interface MatchState {
  status: MatchStatus
  conversationId?: string
  peer?: MatchPeer
  endReason?: string
}

export type MatchAction =
  | { type: 'start' }
  | { type: 'cancel' }
  | { type: 'found'; conversationId: string; peer: MatchPeer }
  | { type: 'next' }
  | { type: 'leave' }
  | { type: 'ended'; reason: string }
  | { type: 'resume'; conversationId: string }
  | { type: 'reset' }

export const initialMatchState: MatchState = { status: 'idle' }

export function matchReducer(state: MatchState, action: MatchAction): MatchState {
  switch (action.type) {
    case 'start':
      return { status: 'waiting' }
    case 'cancel':
      return { status: 'idle' }
    case 'found':
      return {
        status: 'chatting',
        conversationId: action.conversationId,
        peer: action.peer,
      }
    case 'next':
      return { status: 'waiting' }
    case 'leave':
      return { status: 'idle' }
    case 'ended':
      return { ...state, status: 'ended', endReason: action.reason }
    case 'resume':
      return { status: 'chatting', conversationId: action.conversationId }
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
