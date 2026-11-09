import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

const adminLogin = vi.fn()

vi.mock('../../lib/api/client', () => ({
  adminLogin: (u: string, p: string) => adminLogin(u, p),
}))

import { LoginPage } from './LoginPage'

describe('LoginPage', () => {
  beforeEach(() => {
    adminLogin.mockReset().mockResolvedValue(undefined)
  })

  it('用户名或密码为空时登录按钮禁用', () => {
    render(<LoginPage onSuccess={vi.fn()} onError={vi.fn()} />)
    expect(screen.getByRole('button', { name: '登录' })).toBeDisabled()
  })

  it('回车提交调用 adminLogin 并回调成功', async () => {
    const user = userEvent.setup()
    const onSuccess = vi.fn()
    render(<LoginPage onSuccess={onSuccess} onError={vi.fn()} />)

    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'secret{Enter}')

    await vi.waitFor(() => expect(adminLogin).toHaveBeenCalledWith('admin', 'secret'))
    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalled())
  })

  it('登录失败时把错误交给 onError', async () => {
    const user = userEvent.setup()
    const onError = vi.fn()
    adminLogin.mockRejectedValueOnce(new Error('bad'))
    render(<LoginPage onSuccess={vi.fn()} onError={onError} />)

    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'wrong{Enter}')

    await vi.waitFor(() => expect(onError).toHaveBeenCalled())
  })
})
