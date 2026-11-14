import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import * as api from '../../lib/api/client'
import MatchPage from './MatchPage'

/**
 * 1v1 随机匹配按产品决定显示对方的真实昵称与头像：
 * match.found 的 peer 带出 nickname / avatarSeed，页面头部直接展示，
 * 不再使用「匿名·XXXX」代号。
 */

const handlers = vi.hoisted(() => new Map<string, (d: unknown) => void>())

const ws = vi.hoisted(() => ({
  on: vi.fn((evt: string, cb: (d: unknown) => void) => {
    handlers.set(evt, cb)
    return () => handlers.delete(evt)
  }),
  send: vi.fn(),
  subscribeConversation: vi.fn(),
  sendMessage: vi.fn(),
}))

vi.mock('../../lib/ws', () => ({ getWsClient: () => ws }))
vi.mock('../../lib/api/client', async (original) => ({
  ...(await original<typeof import('../../lib/api/client')>()),
  getConversation: vi.fn(),
  conversationMessages: vi.fn(),
  blockConversation: vi.fn(),
  createReport: vi.fn(),
}))

function renderMatch() {
  return render(
    <MemoryRouter initialEntries={['/match']}>
      <MatchPage />
    </MemoryRouter>,
  )
}

/** 模拟服务端下发 match.found。 */
function found(peer: Record<string, unknown>, conversationId = 'c1') {
  act(() => {
    handlers.get('match.found')?.({ conversationId, peer })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  handlers.clear()
  vi.mocked(api.getConversation).mockResolvedValue({
    id: 'c1',
    kind: 'match',
    myMemberId: 1,
    burnAfterRead: false,
  })
  vi.mocked(api.conversationMessages).mockResolvedValue([])
})

describe('MatchPage 1v1 头部', () => {
  it('显示对方真实昵称与头像种子', async () => {
    renderMatch()
    found({ memberId: 2, nickname: '迷路的水獭', avatarSeed: 'seed-2', gender: 'female', age: 24, province: '浙江' })

    expect(await screen.findByText('迷路的水獭')).toBeInTheDocument()
    // 头部头像使用对方下发的 avatarSeed。
    const header = screen.getByText('迷路的水獭').closest('header')
    const avatar = header?.querySelector('[data-seed="seed-2"]')
    expect(avatar).not.toBeNull()
    // 不再出现「匿名·XXXX」代号。
    expect(screen.queryByText(/^匿名·/)).toBeNull()
  })

  it('对方没有昵称时回退「对方」', async () => {
    renderMatch()
    found({ memberId: 2 })

    expect(await screen.findByText('对方')).toBeInTheDocument()
    expect(screen.queryByText(/^匿名·/)).toBeNull()
  })
})
