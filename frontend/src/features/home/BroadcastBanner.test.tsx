import { describe, it, expect, vi, beforeAll, afterAll, beforeEach } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

const currentBroadcasts = vi.fn()

vi.mock('../../lib/api/client', () => ({
  currentBroadcasts: () => currentBroadcasts(),
}))

import { useIdentityStore } from '../../stores/identity'
import type { Broadcast } from '../../lib/api/client'
import { BroadcastBanner } from './BroadcastBanner'

const originalState = useIdentityStore.getState()

function broadcast(content: string): Broadcast {
  return {
    id: content,
    content,
    status: 'approved',
    rejectReason: null,
    showUntil: null,
    createdAt: '2026-01-01T00:00:00Z',
  }
}

function renderBanner() {
  return render(
    <MemoryRouter>
      <BroadcastBanner />
    </MemoryRouter>,
  )
}

// jsdom 没有 ResizeObserver：Marquee 测量布局时会用到（文件级桩，避免异步 state 更新落在测试之后）。
beforeAll(() => {
  Object.defineProperty(globalThis, 'ResizeObserver', {
    configurable: true,
    writable: true,
    value: class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  })
})

afterAll(() => {
  Reflect.deleteProperty(globalThis, 'ResizeObserver')
})

beforeEach(() => {
  currentBroadcasts.mockReset()
  // initialized 为 true 时组件才会去拉取广播。
  useIdentityStore.setState({ initialized: true })
})

afterEach(() => {
  // 先卸载再还原 store，避免已挂载组件在 act 之外收到状态更新。
  cleanup()
  useIdentityStore.setState(originalState)
})

describe('BroadcastBanner：首页广播窄横幅', () => {
  it('有广播时滚动展示内容，并给出「发布广播」入口', async () => {
    currentBroadcasts.mockResolvedValue([broadcast('今晚八点开黑')])
    renderBanner()

    expect(screen.getByTestId('broadcast-banner')).toBeInTheDocument()
    expect(await screen.findByText('今晚八点开黑')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '发布广播' })).toHaveAttribute('href', '/vip')
    expect(screen.queryByText('还没有广播')).toBeNull()
  })

  it('没有广播时显示占位文案与「了解广播卡」入口', async () => {
    currentBroadcasts.mockResolvedValue([])
    renderBanner()

    expect(await screen.findByText('还没有广播')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '了解广播卡' })).toHaveAttribute('href', '/vip')
    expect(screen.queryByTestId('marquee')).toBeNull()
  })

  it('接口失败时不报错，按无广播展示', async () => {
    currentBroadcasts.mockRejectedValue(new Error('boom'))
    renderBanner()

    await waitFor(() => expect(currentBroadcasts).toHaveBeenCalled())
    expect(screen.getByTestId('broadcast-banner')).toBeInTheDocument()
    expect(screen.getByText('还没有广播')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '了解广播卡' })).toHaveAttribute('href', '/vip')
  })
})
