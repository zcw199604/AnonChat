import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { VipLock } from './VipLock'

describe('VipLock：显示由 me.vip.active 决定', () => {
  it('非 VIP 时显示遮罩与解锁按钮，点击触发 onUnlock', () => {
    const onUnlock = vi.fn()
    render(
      <VipLock active onUnlock={onUnlock}>
        <div>匹配偏好</div>
      </VipLock>,
    )
    expect(screen.getByTestId('vip-lock')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /激活码解锁 VIP 特权/ }))
    expect(onUnlock).toHaveBeenCalled()
  })

  it('VIP 生效时不显示遮罩', () => {
    render(
      <VipLock active={false} onUnlock={vi.fn()}>
        <div>匹配偏好</div>
      </VipLock>,
    )
    expect(screen.queryByTestId('vip-lock')).toBeNull()
    expect(screen.getByText('匹配偏好')).toBeInTheDocument()
  })
})
