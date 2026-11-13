import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import * as api from './api/client'
import { useChat } from './useChat'

const ws = vi.hoisted(() => ({ on: vi.fn(() => vi.fn()), sendMessage: vi.fn(), send: vi.fn() }))
vi.mock('./ws', () => ({ getWsClient: () => ws }))
vi.mock('./api/client', async (original) => ({ ...await original<typeof import('./api/client')>(), getConversation: vi.fn(), conversationMessages: vi.fn(), uploadMedia: vi.fn() }))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getConversation).mockResolvedValue({ id: 'c1', kind: 'match', myMemberId: 1, burnAfterRead: false })
  vi.mocked(api.conversationMessages).mockResolvedValue([])
})

it('上传完成后才发送 mediaId，上传失败不发送', async () => {
  let finish!: (value: { mediaId: string; kind: 'image' }) => void
  vi.mocked(api.uploadMedia).mockReturnValue(new Promise((resolve) => { finish = resolve }))
  const file = new File(['x'], 'image.png')
  const { result } = renderHook(() => useChat('c1'))
  await waitFor(() => expect(result.current.detail?.id).toBe('c1'))
  let pending!: Promise<void>
  act(() => { pending = result.current.sendMedia(file) })
  expect(api.uploadMedia).toHaveBeenCalledWith(file)
  expect(ws.sendMessage).not.toHaveBeenCalled()
  await act(async () => { finish({ mediaId: 'm1', kind: 'image' }); await pending })
  expect(ws.sendMessage).toHaveBeenCalledWith('c1', '', { mediaId: 'm1', kind: 'image' })
  ws.sendMessage.mockClear()
  vi.mocked(api.uploadMedia).mockRejectedValue(new api.ApiError('media_rejected', '类型不支持', 400))
  await act(async () => { await expect(result.current.sendMedia(file)).rejects.toMatchObject({ message: '类型不支持' }) })
  expect(ws.sendMessage).not.toHaveBeenCalled()
})

it('上传期间切换会话，不把文件发到旧会话或新会话', async () => {
  let finish!: (value: { mediaId: string; kind: 'image' }) => void
  vi.mocked(api.uploadMedia).mockReturnValue(new Promise((resolve) => { finish = resolve }))
  const { result, rerender } = renderHook(({ id }) => useChat(id), { initialProps: { id: 'c1' } })
  await waitFor(() => expect(result.current.detail?.id).toBe('c1'))
  let pending!: Promise<void>
  act(() => { pending = result.current.sendMedia(new File(['x'], 'image.png')) })
  rerender({ id: 'c2' })
  await act(async () => {
    finish({ mediaId: 'm1', kind: 'image' })
    await expect(pending).rejects.toThrow('会话已切换')
  })
  expect(ws.sendMessage).not.toHaveBeenCalled()
})
