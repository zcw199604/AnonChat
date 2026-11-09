import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

const adminOnlineUsers = vi.fn()
const adminBans = vi.fn()
const adminCreateBan = vi.fn()
const adminRevokeBan = vi.fn()

vi.mock('../../lib/api/client', () => ({
  ApiError: class ApiError extends Error {
    code = 'internal'
    status = 500
  },
  adminOnlineUsers: (q?: string) => adminOnlineUsers(q),
  adminBans: (params?: unknown) => adminBans(params),
  adminCreateBan: (body: unknown) => adminCreateBan(body),
  adminRevokeBan: (id: string) => adminRevokeBan(id),
}))

import { BansPage } from './pages/BansPage'

const NOW = '2024-03-05T10:00:00Z'

const onlineUsers = [
  {
    identityId: 'id-1',
    publicCode: 'ABCD3FGH9K',
    nickname: '在线甲',
    accountId: 'acc-1',
    ips: ['1.1.1.1', '2.2.2.2'],
    connCount: 2,
    connectedAt: NOW,
    userAgent: 'Mozilla/5.0 测试',
  },
]

function renderBans(initialPath = '/admin/bans') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/admin/bans" element={<BansPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('BansPage 在线用户视图', () => {
  beforeEach(() => {
    adminOnlineUsers.mockReset().mockResolvedValue({ users: onlineUsers, total: 1 })
    adminBans.mockReset().mockResolvedValue({ bans: [], nextCursor: null })
    adminCreateBan.mockReset().mockResolvedValue({ bans: [] })
    adminRevokeBan.mockReset().mockResolvedValue(undefined)
  })

  it('渲染在线用户的昵称、编号与全部 IP', async () => {
    renderBans()

    expect(await screen.findByText('在线甲')).toBeInTheDocument()
    expect(screen.getByText('ABCD3FGH9K')).toBeInTheDocument()
    expect(screen.getByText('1.1.1.1')).toBeInTheDocument()
    expect(screen.getByText('2.2.2.2')).toBeInTheDocument()
    expect(screen.getByText('当前在线 1 人')).toBeInTheDocument()
    expect(adminOnlineUsers).toHaveBeenCalled()
  })

  it('空列表显示「当前没有在线用户」', async () => {
    adminOnlineUsers.mockResolvedValue({ users: [], total: 0 })
    renderBans()

    expect(await screen.findByText('当前没有在线用户')).toBeInTheDocument()
  })

  it('点「封禁 IP」后提交，以正确的 subjects 调用 adminCreateBan', async () => {
    const user = userEvent.setup()
    renderBans('/admin/bans?view=online')

    const banButtons = await screen.findAllByRole('button', { name: '封禁 IP' })
    // 第一个「封禁 IP」对应 1.1.1.1。
    await user.click(banButtons[0])

    const ipInput = await screen.findByLabelText('IP 地址')
    expect(ipInput).toHaveValue('1.1.1.1')

    await user.type(screen.getByLabelText('封禁原因'), '滥发广告')
    await user.click(screen.getByRole('button', { name: '确认封禁' }))

    await waitFor(() => {
      expect(adminCreateBan).toHaveBeenCalledTimes(1)
    })
    expect(adminCreateBan).toHaveBeenCalledWith({
      subjects: [{ type: 'ip', value: '1.1.1.1' }],
      durationHours: 24,
      reason: '滥发广告',
    })
  })

  it('封禁用户时按勾选组合 identity/account/ip 主体', async () => {
    const user = userEvent.setup()
    renderBans()

    await user.click(await screen.findByRole('button', { name: '封禁用户' }))

    // 默认勾选匿名身份；再勾上实名账号与第二个 IP。
    await user.click(screen.getByLabelText('实名账号'))
    await user.click(screen.getByLabelText('2.2.2.2'))
    await user.type(screen.getByLabelText('封禁原因'), '骚扰他人')
    await user.click(screen.getByRole('button', { name: '确认封禁' }))

    await waitFor(() => {
      expect(adminCreateBan).toHaveBeenCalledTimes(1)
    })
    expect(adminCreateBan).toHaveBeenCalledWith({
      subjects: [
        { type: 'identity', value: 'id-1' },
        { type: 'account', value: 'acc-1' },
        { type: 'ip', value: '2.2.2.2' },
      ],
      durationHours: 24,
      reason: '骚扰他人',
    })
  })

  it('未填原因时就地报错且不提交', async () => {
    const user = userEvent.setup()
    renderBans()

    await user.click(await screen.findByRole('button', { name: '封禁用户' }))
    await user.click(screen.getByRole('button', { name: '确认封禁' }))

    expect(await screen.findByText('请填写封禁原因')).toBeInTheDocument()
    expect(adminCreateBan).not.toHaveBeenCalled()
  })
})

describe('BansPage 封禁记录视图', () => {
  beforeEach(() => {
    adminOnlineUsers.mockReset().mockResolvedValue({ users: onlineUsers, total: 1 })
    adminBans.mockReset().mockResolvedValue({
      bans: [
        {
          id: 'b1',
          subjectType: 'ip',
          subjectValue: '9.9.9.9',
          reason: '测试',
          active: true,
          expiresAt: null,
        },
      ],
      nextCursor: null,
    })
    adminCreateBan.mockReset().mockResolvedValue({ bans: [] })
    adminRevokeBan.mockReset().mockResolvedValue(undefined)
  })

  it('view=records 时渲染封禁记录，并可用「手动封禁 IP」提交', async () => {
    const user = userEvent.setup()
    renderBans('/admin/bans?view=records')

    expect(await screen.findByText('9.9.9.9')).toBeInTheDocument()
    expect(adminOnlineUsers).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: '手动封禁 IP' }))
    // 空输入 + 无原因：先提示至少一个主体。
    await user.click(screen.getByRole('button', { name: '确认封禁' }))
    expect(await screen.findByText(/请至少选择一个封禁对象/)).toBeInTheDocument()

    await user.type(screen.getByLabelText('IP 地址'), '203.0.113.7')
    await user.type(screen.getByLabelText('封禁原因'), '手动处置')
    await user.click(screen.getByRole('button', { name: '确认封禁' }))

    await waitFor(() => {
      expect(adminCreateBan).toHaveBeenCalledWith({
        subjects: [{ type: 'ip', value: '203.0.113.7' }],
        durationHours: 24,
        reason: '手动处置',
      })
    })
  })

  it('非法 IP 就地报错', async () => {
    const user = userEvent.setup()
    renderBans('/admin/bans?view=records')

    await user.click(await screen.findByRole('button', { name: '手动封禁 IP' }))
    await user.type(screen.getByLabelText('IP 地址'), '999.1.1.1')
    await user.type(screen.getByLabelText('封禁原因'), '非法测试')
    await user.click(screen.getByRole('button', { name: '确认封禁' }))

    expect(await screen.findByText('IP 格式不正确')).toBeInTheDocument()
    expect(adminCreateBan).not.toHaveBeenCalled()
  })
})
