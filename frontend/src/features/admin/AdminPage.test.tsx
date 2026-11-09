import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

const adminStats = vi.fn()
const adminBroadcasts = vi.fn()
const adminRejectBroadcast = vi.fn()
const adminApproveBroadcast = vi.fn()
const adminRemoveBroadcast = vi.fn()
const adminLogin = vi.fn()

vi.mock('../../lib/api/client', () => ({
  ApiError: class ApiError extends Error {
    code: string
    status: number
    constructor(code: string, message: string, status: number) {
      super(message)
      this.code = code
      this.status = status
    }
  },
  adminStats: () => adminStats(),
  adminLogout: () => Promise.resolve(),
  adminBroadcasts: (status: string) => adminBroadcasts(status),
  adminApproveBroadcast: (id: string) => adminApproveBroadcast(id),
  adminRejectBroadcast: (id: string, reason: string) => adminRejectBroadcast(id, reason),
  adminRemoveBroadcast: (id: string) => adminRemoveBroadcast(id),
  adminLogin: (u: string, p: string) => adminLogin(u, p),
}))

import AdminPage from './AdminPage'

function renderAdmin(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/admin/*" element={<AdminPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

const broadcasts = [
  {
    id: 'b1',
    content: '这是一条测试广播',
    status: 'pending',
    publicCode: 'ABCD3FGH9K',
    createdAt: '2024-03-05T10:00:00Z',
  },
]

describe('AdminPage 与 AdminLayout', () => {
  beforeEach(() => {
    adminStats.mockReset().mockResolvedValue({
      online: 3,
      activeConversations: 1,
      messages24h: 42,
      openReports: 2,
    })
    adminBroadcasts.mockReset().mockResolvedValue({ broadcasts })
    adminRejectBroadcast.mockReset().mockResolvedValue(undefined)
    adminApproveBroadcast.mockReset().mockResolvedValue(undefined)
    adminRemoveBroadcast.mockReset().mockResolvedValue(undefined)
    adminLogin.mockReset().mockResolvedValue(undefined)
  })

  it('侧边栏渲染分组链接，当前路由的链接带 aria-current', async () => {
    renderAdmin('/admin/broadcasts')

    const nav = await screen.findByRole('navigation', { name: '后台导航' })
    const link = within(nav).getByRole('link', { name: /广播审核/ })
    expect(link).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: /举报处理/ })).not.toHaveAttribute('aria-current')
    // 分组标题
    expect(screen.getByText('内容审核')).toBeInTheDocument()
    expect(screen.getByText('系统')).toBeInTheDocument()
  })

  it('待处理举报角标显示 openReports', async () => {
    renderAdmin('/admin')
    const nav = await screen.findByRole('navigation', { name: '后台导航' })
    await vi.waitFor(() =>
      expect(within(nav).getByRole('link', { name: /举报处理/ })).toHaveTextContent('2'),
    )
  })

  it('广播驳回：填写原因并确认后以 (id, reason) 调用接口', async () => {
    const { default: userEvent } = await import('@testing-library/user-event')
    const user = userEvent.setup()
    renderAdmin('/admin/broadcasts')

    const card = await screen.findByTestId('admin-broadcast')
    expect(card).toHaveTextContent('这是一条测试广播')

    await user.click(screen.getByRole('button', { name: '驳回' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('驳回原因'), '内容不合规')
    await user.click(within(dialog).getByRole('button', { name: '驳回' }))

    await vi.waitFor(() => expect(adminRejectBroadcast).toHaveBeenCalledWith('b1', '内容不合规'))
  })

  it('广播驳回失败：对话框保持打开且原因不丢失', async () => {
    adminRejectBroadcast.mockRejectedValue(Object.assign(new Error('boom'), { code: 'internal' }))
    const { default: userEvent } = await import('@testing-library/user-event')
    const user = userEvent.setup()
    renderAdmin('/admin/broadcasts')

    await screen.findByTestId('admin-broadcast')
    await user.click(screen.getByRole('button', { name: '驳回' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('驳回原因'), '内容不合规')
    await user.click(within(dialog).getByRole('button', { name: '驳回' }))

    await vi.waitFor(() => expect(adminRejectBroadcast).toHaveBeenCalled())
    await vi.waitFor(() =>
      expect(within(dialog).getByRole('button', { name: '驳回' })).not.toBeDisabled(),
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('驳回原因')).toHaveValue('内容不合规')
    expect(screen.getByTestId('admin-broadcast')).toBeInTheDocument()
  })
})
