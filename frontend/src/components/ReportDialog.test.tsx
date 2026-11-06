import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ReportDialog } from './ReportDialog'

describe('ReportDialog', () => {
  it('勾选消息并选择原因后提交，messageIds 与 reason 正确', () => {
    const onSubmit = vi.fn()
    render(
      <ReportDialog
        open
        peerMessages={[
          { id: 11, body: 'first' },
          { id: 22, body: 'second' },
        ]}
        onClose={vi.fn()}
        onSubmit={onSubmit}
      />,
    )
    fireEvent.click(screen.getByText('first').closest('label')!.querySelector('input')!)
    fireEvent.change(screen.getByLabelText('举报原因'), { target: { value: 'harassment' } })
    fireEvent.change(screen.getByLabelText('举报备注'), { target: { value: '烦人' } })
    fireEvent.click(screen.getByText('提交举报'))
    expect(onSubmit).toHaveBeenCalledWith({ messageIds: [11], reason: 'harassment', note: '烦人' })
  })

  it('未选择消息或原因为空时提交按钮禁用', () => {
    render(<ReportDialog open peerMessages={[{ id: 1, body: 'x' }]} onClose={vi.fn()} onSubmit={vi.fn()} />)
    expect(screen.getByText('提交举报')).toBeDisabled()
  })

  it('备注最多 500 字', () => {
    render(<ReportDialog open peerMessages={[{ id: 1, body: 'x' }]} onClose={vi.fn()} onSubmit={vi.fn()} />)
    const note = screen.getByLabelText('举报备注') as HTMLTextAreaElement
    expect(note.maxLength).toBe(500)
  })
})
