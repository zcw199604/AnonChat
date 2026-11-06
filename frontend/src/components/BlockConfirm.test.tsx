import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BlockConfirm } from './BlockConfirm'

describe('BlockConfirm', () => {
  it('确认调用 onConfirm，取消调用 onCancel', () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(<BlockConfirm open name="小明" onConfirm={onConfirm} onCancel={onCancel} />)
    fireEvent.click(screen.getByText('确认屏蔽'))
    expect(onConfirm).toHaveBeenCalled()
    fireEvent.click(screen.getByText('取消'))
    expect(onCancel).toHaveBeenCalled()
  })

  it('关闭时不渲染', () => {
    const { container } = render(<BlockConfirm open={false} onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(container).toBeEmptyDOMElement()
  })
})
