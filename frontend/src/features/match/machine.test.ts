import { describe, it, expect } from 'vitest'
import { matchReducer, initialMatchState, endReasonText } from './machine'

describe('matchReducer', () => {
  it('开始匹配进入等待中', () => {
    expect(matchReducer(initialMatchState, { type: 'start' }).status).toBe('waiting')
  })
  it('取消回到空闲', () => {
    expect(matchReducer({ status: 'waiting' }, { type: 'cancel' }).status).toBe('idle')
  })
  it('匹配成功进入聊天中并记录对方的公开资料（不再含昵称）', () => {
    const s = matchReducer(initialMatchState, {
      type: 'found',
      conversationId: 'c1',
      peer: { memberId: 2, gender: 'female', age: 24, province: '浙江', tags: ['音乐'], cleanMode: true },
    })
    expect(s.status).toBe('chatting')
    expect(s.conversationId).toBe('c1')
    expect(s.peer?.memberId).toBe(2)
    expect(s.peer?.province).toBe('浙江')
    expect(s.peer && 'nickname' in s.peer).toBe(false)
  })
  it('下一个回到等待中', () => {
    expect(matchReducer({ status: 'chatting' }, { type: 'next' }).status).toBe('waiting')
  })
  it('离开回到空闲', () => {
    expect(matchReducer({ status: 'chatting' }, { type: 'leave' }).status).toBe('idle')
  })
  it('结束进入已结束并记录原因', () => {
    const s = matchReducer({ status: 'chatting' }, { type: 'ended', reason: 'peer_skipped' })
    expect(s.status).toBe('ended')
    expect(s.endReason).toBe('peer_skipped')
  })
})

describe('endReasonText', () => {
  it('映射各结束原因', () => {
    expect(endReasonText('peer_left')).toBe('对方已离开')
    expect(endReasonText('peer_skipped')).toBe('对方跳过了你')
    expect(endReasonText('blocked')).toBe('会话已结束')
    expect(endReasonText('expired')).toBe('会话已过期')
  })
  it('未知原因使用通用文案', () => {
    expect(endReasonText('whatever')).toBe('会话已结束')
    expect(endReasonText(undefined)).toBe('会话已结束')
  })
})
