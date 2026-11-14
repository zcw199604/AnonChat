import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
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

it('只在传入媒体回调时显示附件，禁用状态禁止选择', () => {
  const view = render(<Composer onSend={vi.fn()} />)
  expect(screen.queryByRole('button', { name: '发送图片或视频' })).not.toBeInTheDocument()
  view.rerender(<Composer onSend={vi.fn()} onSendMedia={vi.fn()} disabled />)
  expect(screen.getByRole('button', { name: '发送图片或视频' })).toBeDisabled()
  expect(screen.getByLabelText('选择图片或视频')).toHaveAttribute('accept', 'image/jpeg,image/png,image/gif,image/webp,video/mp4,video/webm')
})

it('上传中禁用附件；失败显示提示并恢复，可重选同一文件', async () => {
  let reject!: (reason: Error) => void
  const upload = vi.fn().mockReturnValueOnce(new Promise<void>((_, fail) => { reject = fail })).mockResolvedValue(undefined)
  render(<Composer onSend={vi.fn()} onSendMedia={upload} />)
  const file = new File(['png'], 'image.png', { type: 'image/png' })
  const input = screen.getByLabelText('选择图片或视频')
  fireEvent.change(input, { target: { files: [file] } })
  expect(upload).toHaveBeenCalledWith(file)
  expect(screen.getByRole('button', { name: '发送图片或视频' })).toBeDisabled()
  await act(async () => { reject(new Error('文件超过大小上限')) })
  expect(screen.getByRole('alert')).toHaveTextContent('文件超过大小上限')
  expect(screen.getByRole('button', { name: '发送图片或视频' })).toBeEnabled()
  fireEvent.change(input, { target: { files: [file] } })
  await waitFor(() => expect(upload).toHaveBeenCalledTimes(2))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('900 字以内不显示计数，超过 90% 才显示，并按 maxChars 计算阈值', () => {
  const view = render(<Composer onSend={vi.fn()} />)
  const input = screen.getByLabelText('消息输入框')
  expect(screen.queryByText('0 / 1000')).toBeNull()
  fireEvent.change(input, { target: { value: 'a'.repeat(900) } })
  expect(screen.queryByText('900 / 1000')).toBeNull()
  fireEvent.change(input, { target: { value: 'a'.repeat(901) } })
  expect(screen.getByText('901 / 1000')).toBeInTheDocument()
  view.rerender(<Composer onSend={vi.fn()} maxChars={10} />)
  fireEvent.change(input, { target: { value: 'a'.repeat(9) } })
  expect(screen.queryByText('9 / 10')).toBeNull()
  fireEvent.change(input, { target: { value: 'a'.repeat(10) } })
  expect(screen.getByText('10 / 10')).toBeInTheDocument()
})

it('无内容或只有空白时发送键禁用；有效内容启用，发送后清空并收起表情', () => {
  const onSend = vi.fn()
  render(<Composer onSend={onSend} />)
  const input = screen.getByLabelText('消息输入框')
  const send = screen.getByRole('button', { name: '发送' })
  expect(send).toBeDisabled()
  fireEvent.change(input, { target: { value: '  ' } })
  expect(send).toBeDisabled()
  fireEvent.change(input, { target: { value: ' hi ' } })
  expect(send).toBeEnabled()
  fireEvent.click(screen.getByRole('button', { name: '表情' }))
  fireEvent.click(send)
  expect(onSend).toHaveBeenCalledWith('hi')
  expect(input).toHaveValue('')
  expect(send).toBeDisabled()
  expect(screen.queryByTestId('emoji-panel')).toBeNull()
})

it('输入框从一行增高到五行，超出后滚动，发送后恢复一行', () => {
  render(<Composer onSend={vi.fn()} />)
  const input = screen.getByLabelText('消息输入框') as HTMLTextAreaElement
  expect(input.rows).toBe(1)
  expect(input.placeholder).toBe('说点什么…')
  Object.defineProperty(input, 'scrollHeight', { configurable: true, value: 72 })
  fireEvent.change(input, { target: { value: 'a\nb\nc' } })
  expect(input.style.height).toBe('72px')
  Object.defineProperty(input, 'scrollHeight', { configurable: true, value: 200 })
  fireEvent.change(input, { target: { value: 'a\nb\nc\nd\ne\nf' } })
  expect(input.style.height).toBe('120px')
  expect(input.style.overflowY).toBe('auto')
  Object.defineProperty(input, 'scrollHeight', { configurable: true, value: 24 })
  fireEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(input.style.height).toBe('24px')
})
