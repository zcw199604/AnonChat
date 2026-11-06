import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MessageList } from './MessageList'
import type { DisplayMessage } from './MessageList'

function msg(partial: Partial<DisplayMessage> & { id: number }): DisplayMessage {
  return {
    memberId: 2,
    nickname: 'peer',
    avatarSeed: 'seed',
    kind: 'text',
    body: 'hello',
    createdAt: '2026-01-01T00:00:00Z',
    ...partial,
  }
}

describe('MessageList', () => {
  it('自己的消息靠右、对方靠左', () => {
    const { container } = render(
      <MessageList
        selfMemberId={1}
        messages={[msg({ id: 1, memberId: 1, body: 'mine' }), msg({ id: 2, memberId: 2, body: 'theirs' })]}
      />,
    )
    const selfEls = container.querySelectorAll('[data-self="true"]')
    expect(selfEls).toHaveLength(1)
    expect(selfEls[0].textContent).toContain('mine')
  })

  it('消息正文以纯文本渲染，HTML 不会被渲染为元素', () => {
    const { container } = render(
      <MessageList selfMemberId={1} messages={[msg({ id: 1, body: '<b>bold</b> & <img src=x>' })]} />,
    )
    expect(container.querySelector('b')).toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('<b>bold</b> & <img src=x>')).toBeInTheDocument()
  })

  it('显示发送中与失败状态，失败可点击重试', () => {
    const onRetry = vi.fn()
    render(
      <MessageList
        selfMemberId={1}
        onRetry={onRetry}
        messages={[
          msg({ id: 1, memberId: 1, body: 'a', state: 'sending' }),
          msg({ id: 2, memberId: 1, body: 'b', state: 'failed' }),
        ]}
      />,
    )
    expect(screen.getByText('发送中')).toBeInTheDocument()
    const retryBtn = screen.getByText('发送失败，点击重试')
    fireEvent.click(retryBtn)
    expect(onRetry).toHaveBeenCalledWith(2)
  })

  it('阅后即焚显示倒计时；消息被移除后不再渲染', () => {
    const list = [msg({ id: 1, burnCountdown: 10 })]
    const { rerender, queryByText } = render(<MessageList selfMemberId={1} messages={list} />)
    expect(queryByText('将在 10 秒后焚毁')).toBeInTheDocument()
    rerender(<MessageList selfMemberId={1} messages={[]} />)
    expect(queryByText('将在 10 秒后焚毁')).toBeNull()
  })

  it('对方正在输入时显示提示', () => {
    render(<MessageList selfMemberId={1} messages={[]} typing />)
    expect(screen.getByTestId('typing')).toBeInTheDocument()
  })
})
