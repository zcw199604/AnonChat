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

  it('图片消息渲染懒加载图片，点击在新标签页打开', () => {
    const { container } = render(
      <MessageList
        selfMemberId={1}
        messages={[msg({ id: 1, kind: 'image', body: '', mediaUrl: '/api/media/abc' })]}
      />,
    )
    const img = screen.getByAltText('图片消息') as HTMLImageElement
    expect(img).toBeInTheDocument()
    expect(img.getAttribute('src')).toBe('/api/media/abc')
    expect(img.getAttribute('loading')).toBe('lazy')
    const link = img.closest('a') as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe('/api/media/abc')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    // 媒体消息没有正文时不渲染空的文本气泡（Bubble 带 animate-fade-up）。
    expect(container.querySelector('.animate-fade-up')).toBeNull()
  })

  it('视频消息渲染带 controls 的播放器', () => {
    const { container } = render(
      <MessageList
        selfMemberId={1}
        messages={[msg({ id: 1, kind: 'video', body: '', mediaUrl: '/api/media/v1' })]}
      />,
    )
    const video = container.querySelector('video') as HTMLVideoElement
    expect(video).not.toBeNull()
    expect(video.getAttribute('src')).toBe('/api/media/v1')
    expect(video.hasAttribute('controls')).toBe(true)
    expect(video.getAttribute('preload')).toBe('metadata')
    expect(video.hasAttribute('playsinline')).toBe(true)
  })

  it('缺少 mediaUrl 时显示媒体已失效', () => {
    render(
      <MessageList
        selfMemberId={1}
        messages={[
          msg({ id: 1, kind: 'image', body: '' }),
          msg({ id: 2, kind: 'video', body: '' }),
        ]}
      />,
    )
    expect(screen.getAllByText('[媒体已失效]')).toHaveLength(2)
  })

  it('媒体消息带文字时仍然渲染文本气泡', () => {
    render(
      <MessageList
        selfMemberId={1}
        messages={[msg({ id: 1, kind: 'image', body: '看我拍的', mediaUrl: '/api/media/x' })]}
      />,
    )
    expect(screen.getByText('看我拍的')).toBeInTheDocument()
    expect(screen.getByAltText('图片消息')).toBeInTheDocument()
  })

  it('对方正在输入时显示提示', () => {
    render(<MessageList selfMemberId={1} messages={[]} typing />)
    expect(screen.getByTestId('typing')).toBeInTheDocument()
  })
})
