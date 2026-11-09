import { beforeEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { RateLimitsPage } from './RateLimitsPage'
import { adminRateLimitRules, adminUpdateRateLimitRules, ApiError } from '../../../lib/api/client'
import type { AdminRateLimitSettings } from '../../../lib/api/client'
vi.mock('../../../lib/api/client', async (original) => ({ ...await original<typeof import('../../../lib/api/client')>(), adminRateLimitRules: vi.fn(), adminUpdateRateLimitRules: vi.fn() }))
const settings: AdminRateLimitSettings = { version: 1, multiplier: 2, rules: [
  { key: 'identity_create', name: '身份创建', group: 'identity', kind: 'frequency', algorithm: 'fixed_window', banSubject: 'ip', limit: 5, windowSeconds: 86400, banSeconds: 86400, enabled: true, minWindowSeconds: 1, maxWindowSeconds: 86400, updatedAt: null },
  { key: 'ws_conn_per_ip', name: '网络连接', group: 'ws', kind: 'concurrent', algorithm: 'fixed_window', banSubject: '', limit: 3, windowSeconds: 0, banSeconds: 0, enabled: true, minWindowSeconds: 0, maxWindowSeconds: 0, updatedAt: null },
] }
beforeEach(() => { vi.mocked(adminRateLimitRules).mockResolvedValue(settings); vi.mocked(adminUpdateRateLimitRules).mockReset() })
it('加载分组规则、禁用不支持封禁的控件，修改后统一保存', async () => {
  vi.mocked(adminUpdateRateLimitRules).mockImplementation(async (rules) => ({ ...settings, version: 2, rules: settings.rules.map((r) => ({ ...r, ...rules.find((x) => x.key === r.key) })) }))
  render(<RateLimitsPage />)
  expect(await screen.findByText('身份创建')).toBeInTheDocument()
  expect(screen.getByText('当前运行时乘数 ×2（测试环境）')).toBeInTheDocument()
  expect(screen.getByLabelText('网络连接超限封禁')).toBeDisabled()
  expect(screen.queryByLabelText('网络连接时间窗口')).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('身份创建次数'), { target: { value: '6' } })
  fireEvent.change(screen.getByLabelText('身份创建超限封禁'), { target: { value: '-1' } })
  expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
  fireEvent.click(screen.getByText('保存'))
  await waitFor(() => expect(adminUpdateRateLimitRules).toHaveBeenCalledWith(expect.arrayContaining([expect.objectContaining({ key: 'identity_create', limit: 6, banSeconds: -1 })])))
  await waitFor(() => expect(screen.queryByText('有未保存的改动')).not.toBeInTheDocument())
})
it('保存失败展示后端具体原因并保留改动', async () => {
  vi.mocked(adminUpdateRateLimitRules).mockRejectedValue(new ApiError('bad_request', '身份创建：窗口超出范围', 400))
  render(<RateLimitsPage />)
  fireEvent.change(await screen.findByLabelText('身份创建次数'), { target: { value: '6' } })
  fireEvent.click(screen.getByText('保存'))
  expect(await screen.findByRole('alert')).toHaveTextContent('身份创建：窗口超出范围')
  expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
})
