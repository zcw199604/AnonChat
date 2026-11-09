import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ConfirmDialog } from './ui'

describe('ConfirmDialog', () => {
  it('确认时调用 onConfirm', async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined)
    render(
      <ConfirmDialog open title="确定删除？" confirmText="删除" onConfirm={onConfirm} onClose={vi.fn()} />,
    )
    expect(screen.getByRole('dialog')).toHaveAttribute('aria-modal', 'true')
    fireEvent.click(screen.getByRole('button', { name: '删除' }))
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1))
  })

  it('取消按钮与 ESC 都会关闭', () => {
    const onClose = vi.fn()
    render(
      <ConfirmDialog open title="确定删除？" confirmText="删除" onConfirm={vi.fn()} onClose={onClose} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(onClose).toHaveBeenCalledTimes(1)

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('关闭时不渲染内容', () => {
    const { container } = render(
      <ConfirmDialog
        open={false}
        title="确定删除？"
        confirmText="删除"
        onConfirm={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(container).toBeEmptyDOMElement()
  })
})
