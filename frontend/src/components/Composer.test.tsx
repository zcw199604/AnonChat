import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Composer } from './Composer'

describe('Composer', () => {
  afterEach(() => vi.useRealTimers())

  it('Enter 发送、Shift+Enter 换行', () => {
    const onSend = vi.fn()
    render(<Composer onSend={onSend} />)
    const ta = screen.getByLabelText('消息输入框')
    fireEvent.change(ta, { target: { value: 'hi' } })
    fireEvent.keyDown(ta, { key: 'Enter', shiftKey: false })
    expect(onSend).toHaveBeenCalledWith('hi')

    onSend.mockClear()
    fireEvent.change(ta, { target: { value: 'line1' } })
    fireEvent.keyDown(ta, { key: 'Enter', shiftKey: true })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('超过 1000 字禁止发送', () => {
    const onSend = vi.fn()
    render(<Composer onSend={onSend} />)
    const ta = screen.getByLabelText('消息输入框')
    fireEvent.change(ta, { target: { value: 'a'.repeat(1001) } })
    const btn = screen.getByRole('button', { name: '发送' })
    expect(btn).toBeDisabled()
    fireEvent.click(btn)
    expect(onSend).not.toHaveBeenCalled()
    expect(screen.getByText('1001 / 1000')).toBeInTheDocument()
  })

  it('点击表情插入到输入框', () => {
    render(<Composer onSend={vi.fn()} />)
    fireEvent.click(screen.getByLabelText('表情'))
    const panel = screen.getByTestId('emoji-panel')
    const firstEmoji = panel.querySelector('button')!
    fireEvent.click(firstEmoji)
    expect((screen.getByLabelText('消息输入框') as HTMLTextAreaElement).value.length).toBeGreaterThan(0)
  })

  it('输入中每 3 秒最多触发一次 typing', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(0))
    const onTyping = vi.fn()
    render(<Composer onSend={vi.fn()} onTyping={onTyping} />)
    const ta = screen.getByLabelText('消息输入框')
    fireEvent.change(ta, { target: { value: 'a' } })
    fireEvent.change(ta, { target: { value: 'ab' } })
    expect(onTyping).toHaveBeenCalledTimes(1)
    vi.setSystemTime(new Date(3001))
    fireEvent.change(ta, { target: { value: 'abc' } })
    expect(onTyping).toHaveBeenCalledTimes(2)
  })
})
