import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import * as api from '../lib/api/client'
import { WsClient } from '../lib/ws/client'
import { createFakeWebSocket } from '../lib/ws/fake'
import MatchPage from './match/MatchPage'
import BottleConversationPage from './bottles/BottleConversationPage'
import PrivateJoinPage from './private/PrivateJoinPage'
import RoomPage from './rooms/RoomPage'
import StoryPlayPage from './stories/StoryPlayPage'

const current = vi.hoisted(() => ({ ws: null as WsClient | null }))
vi.mock('../lib/ws', () => ({ getWsClient: () => current.ws! }))
vi.mock('../lib/api/client', async original => ({ ...await original<typeof import('../lib/api/client')>(),
  getConversation: vi.fn(), conversationMessages: vi.fn(), uploadMedia: vi.fn(), getPrivateLink: vi.fn(), joinPrivateLink: vi.fn(), getStory: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getConversation).mockResolvedValue({ id: 'c1', kind: 'match', myMemberId: 1, burnAfterRead: false })
  vi.mocked(api.conversationMessages).mockResolvedValue([])
  vi.mocked(api.uploadMedia).mockResolvedValue({ mediaId: 'm1', kind: 'image' })
  vi.mocked(api.getPrivateLink).mockResolvedValue({ status: 'active', needPassword: false, burnAfterRead: false })
  vi.mocked(api.joinPrivateLink).mockResolvedValue({ conversationId: 'c1' })
  vi.mocked(api.getStory).mockResolvedValue({ id: 's1', title: '故事', opening: '开场', tags: [], roleA: '甲', roleB: '乙', hostRole: 'a', status: 'active', valid: null, hostConsent: false, guestConsent: false, conversationId: 'c1', myRole: 'host', expiresAt: '', createdAt: '' })
})

it.each([
  ['/match', '/match', <MatchPage />],
  ['/bottles/c1', '/bottles/:conversationId', <BottleConversationPage />],
  ['/p/token', '/p/:token', <PrivateJoinPage />],
])('%s 的附件实际上传后经 WS 发送', async (path, route, page) => {
  const fake = createFakeWebSocket()
  const ws = new WsClient({ url: 'ws://test/ws', WebSocketImpl: fake.Impl, resume: { getConversationMessages: async () => [], getRoomMessages: async () => [] } })
  current.ws = ws
  ws.connect()
  fake.latest().open()
  const view = render(<MemoryRouter initialEntries={[path]}><Routes><Route path={route} element={page} /></Routes></MemoryRouter>)
  if (path === '/match') {
    act(() => fake.latest().message({ t: 'match.found', d: { conversationId: 'c1', peer: { memberId: 2, nickname: '对方', avatarSeed: 'a' } } }))
  }
  await screen.findByRole('button', { name: '发送图片或视频' })
  const file = new File(['x'], 'image.png', { type: 'image/png' })
  fireEvent.change(screen.getByLabelText('选择图片或视频'), { target: { files: [file] } })
  await waitFor(() => expect(fake.latest().sent.some(f => f.t === 'msg.send')).toBe(true))
  expect(api.uploadMedia).toHaveBeenCalledWith(file)
  const frame = fake.latest().sent.find(f => f.t === 'msg.send')!
  expect(frame.d).toMatchObject({ conversationId: 'c1', body: '', kind: 'image', mediaId: 'm1' })
  act(() => fake.latest().message({ t: 'ack', id: frame.id, d: { messageId: 1 } }))
  view.unmount()
  ws.close()
})

it.each([
  ['/rooms/r1', '/rooms/:roomId', <RoomPage />],
  ['/stories/s1/play', '/stories/:storyId/play', <StoryPlayPage />],
])('%s 聊天输入框不开放附件', async (path, route, page) => {
  const fake = createFakeWebSocket()
  const ws = new WsClient({ url: 'ws://test/ws', WebSocketImpl: fake.Impl, resume: { getConversationMessages: async () => [], getRoomMessages: async () => [] } })
  current.ws = ws
  ws.connect()
  fake.latest().open()
  const view = render(<MemoryRouter initialEntries={[path]}><Routes><Route path={route} element={page} /></Routes></MemoryRouter>)
  if (path.startsWith('/rooms')) {
    const frame = fake.latest().sent.find(f => f.t === 'room.join')!
    await act(async () => { fake.latest().message({ t: 'ack', id: frame.id, d: { roomId: 'r1', conversationId: 'c1', memberId: 1, online: 1 } }) })
  }
  await screen.findByLabelText('消息输入框')
  expect(screen.queryByRole('button', { name: '发送图片或视频' })).not.toBeInTheDocument()
  view.unmount()
  ws.close()
})
