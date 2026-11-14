import { afterEach, describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { MessageList } from './MessageList'
import { senderLabel } from './senderLabel'
import type { DisplayMessage } from './MessageList'
import { Profiler } from 'react'

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
    const retryBtn = screen.getByRole('button', { name: '发送失败，点击重试' })
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

  it('1v1 场景显示对方真实昵称，自己的消息不显示标签', () => {
    render(
      <MessageList
        selfMemberId={1}
        messages={[
          msg({ id: 1, memberId: 1, nickname: '自己的昵称', body: 'mine' }),
          msg({ id: 2, memberId: 2, nickname: '迷路的水獭', body: 'theirs' }),
        ]}
      />,
    )
    // 对方消息上方显示真实昵称，不再出现「匿名·XXXX」代号。
    expect(screen.getByText('迷路的水獭')).toBeInTheDocument()
    expect(screen.queryByText(/^匿名·/)).toBeNull()
    // 自己的消息不带标签。
    expect(screen.queryByText('自己的昵称')).toBeNull()
  })

  it('对方昵称为空时回退「对方」', () => {
    render(<MessageList selfMemberId={1} messages={[msg({ id: 1, nickname: '' })]} />)
    expect(screen.getByText('对方')).toBeInTheDocument()
  })

  it('senderLabel：自己显示「我」，对方显示昵称，昵称缺失回退「对方」', () => {
    expect(senderLabel('迷路的水獭', false)).toBe('迷路的水獭')
    expect(senderLabel('迷路的水獭', true)).toBe('我')
    expect(senderLabel('', false)).toBe('对方')
    expect(senderLabel('   ', false)).toBe('对方')
  })

  it('对方正在输入时显示提示', () => {
    render(<MessageList selfMemberId={1} messages={[]} typing />)
    expect(screen.getByTestId('typing')).toBeInTheDocument()
  })
})

afterEach(() => vi.useRealTimers())

function row(body: string) {
  return screen.getByText(body).closest('[data-self]')!
}

function scrollAway() {
  const list = screen.getByTestId('message-list')
  Object.defineProperties(list, {
    scrollHeight: { configurable: true, value: 1000 },
    clientHeight: { configurable: true, value: 200 },
  })
  fireEvent.scroll(list, { target: { scrollTop: 100 } })
  return list
}

describe('MessageList 分组与时间', () => {
  it('同一成员三分钟内成组，昵称仅在组首、头像仅在组尾，自己无头像', () => {
    render(<MessageList selfMemberId={1} messages={[
      msg({ id: 1, body: 'first', createdAt: '2026-01-01T10:00:00' }),
      msg({ id: 2, body: 'last', createdAt: '2026-01-01T10:03:00' }),
      msg({ id: 3, body: 'new group', createdAt: '2026-01-01T10:06:01' }),
      msg({ id: 4, body: 'mine', memberId: 1 }),
    ]} />)
    expect(within(row('first') as HTMLElement).getByText('peer')).toBeInTheDocument()
    expect(row('first').querySelector('[data-seed]')).toBeNull()
    expect(within(row('last') as HTMLElement).queryByText('peer')).toBeNull()
    expect(row('last').querySelector('[data-seed="seed"]')).not.toBeNull()
    expect(within(row('new group') as HTMLElement).getByText('peer')).toBeInTheDocument()
    expect(row('mine').querySelector('[data-seed]')).toBeNull()
  })

  it('缺失时间也成组，system 打断组且不显示头像昵称', () => {
    render(<MessageList selfMemberId={1} messages={[
      msg({ id: 1, body: 'before', createdAt: undefined }),
      msg({ id: 2, body: 'same', createdAt: '2026-01-01T10:00:00' }),
      msg({ id: 3, body: '系统提示', kind: 'system' }),
      msg({ id: 4, body: 'after' }),
    ]} />)
    expect(row('before').querySelector('[data-seed]')).toBeNull()
    expect(within(row('same') as HTMLElement).queryByText('peer')).toBeNull()
    const capsule = screen.getByText('系统提示')
    expect(capsule).toHaveClass('text-chat-sys', 'rounded-full')
    expect(capsule.parentElement?.querySelector('[data-seed]')).toBeNull()
    expect(within(row('after') as HTMLElement).getByText('peer')).toBeInTheDocument()
  })

  it('showSender=false 隐藏昵称；header/footer 位于消息和 typing 两端', () => {
    render(<MessageList selfMemberId={1} messages={[msg({ id: 1, body: '正文' })]}
      showSender={false} header={<p>名片</p>} footer={<p>结束栏</p>} typing />)
    expect(screen.queryByText('peer')).toBeNull()
    const list = screen.getByTestId('message-list')
    expect(list.firstElementChild).toHaveTextContent('名片')
    expect(list.lastElementChild).toHaveTextContent('结束栏')
    expect(screen.getByTestId('typing').compareDocumentPosition(screen.getByText('结束栏')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('首条和超过五分钟的间隔插入时间；区分今天、昨天、更早且缺失时间不插入', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 0, 3, 12))
    render(<MessageList selfMemberId={1} messages={[
      msg({ id: 1, body: 'old', createdAt: '2026-01-01T09:05:00' }),
      msg({ id: 2, body: 'yesterday', createdAt: '2026-01-02T10:00:00' }),
      msg({ id: 3, body: 'today', createdAt: '2026-01-03T10:00:00' }),
      msg({ id: 4, body: 'five', createdAt: '2026-01-03T10:05:00' }),
      msg({ id: 5, body: 'six', createdAt: '2026-01-03T10:11:00' }),
      msg({ id: 6, body: 'no date', createdAt: undefined }),
    ]} />)
    for (const text of ['1月1日 09:05', '昨天 10:00', '10:00', '10:11']) {
      expect(screen.getByText(text, { exact: true })).toBeInTheDocument()
    }
    expect(screen.queryByText('10:05', { exact: true })).toBeNull()
  })

  it('输入气泡使用最近对方头像，无对方消息时使用 anonymous', () => {
    const view = render(<MessageList selfMemberId={1} messages={[]} typing />)
    expect(screen.getByTestId('typing').querySelector('[data-seed="anonymous"]')).not.toBeNull()
    view.rerender(<MessageList selfMemberId={1} messages={[
      msg({ id: 1, avatarSeed: 'peer-last' }), msg({ id: 2, memberId: 1, avatarSeed: 'self' }),
    ]} typing />)
    expect(screen.getByTestId('typing').querySelector('[data-seed="peer-last"]')).not.toBeNull()
    expect(screen.getByTestId('typing').querySelectorAll('.bg-chat-acc')).toHaveLength(3)
  })
  it('typing 紧跟最后一条对方消息时，该消息让出头像位置，停止输入后恢复', () => {
    const messages = [msg({ id: 1, body: '最后一条', avatarSeed: 'peer-last' })]
    const view = render(<MessageList selfMemberId={1} messages={messages} typing />)
    expect(row('最后一条').querySelector('[data-seed]')).toBeNull()
    expect(row('最后一条').querySelector('[aria-hidden="true"].w-7')).not.toBeNull()
    expect(screen.getByTestId('typing').querySelector('[data-seed="peer-last"]')).not.toBeNull()
    view.rerender(<MessageList selfMemberId={1} messages={messages} />)
    expect(row('最后一条').querySelector('[data-seed="peer-last"]')).not.toBeNull()
    view.rerender(<MessageList selfMemberId={1} messages={[...messages, msg({ id: 2, memberId: 1, body: '自己' })]} typing />)
    expect(row('最后一条').querySelector('[data-seed="peer-last"]')).not.toBeNull()
  })
})

describe('MessageList 新消息按 id 计数', () => {
  it('滚动未改变 atBottom/unread 时不提交列表重渲染', () => {
    const onRender = vi.fn()
    render(<Profiler id="messages" onRender={onRender}>
      <MessageList selfMemberId={1} messages={[msg({ id: 1 })]} />
    </Profiler>)
    const commits = onRender.mock.calls.length
    const list = screen.getByTestId('message-list')
    for (let i = 0; i < 5; i++) fireEvent.scroll(list)
    expect(onRender).toHaveBeenCalledTimes(commits)
  })

  it('新消息包含自己发送的内容时不计未读，强制滚到底并清空已有未读', () => {
    const view = render(<MessageList selfMemberId={1} messages={[msg({ id: 1 })]} />)
    const list = scrollAway()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 1 }), msg({ id: 2 })]} />)
    expect(screen.getByRole('button', { name: '1 条新消息' })).toBeInTheDocument()
    view.rerender(<MessageList selfMemberId={1} messages={[
      msg({ id: 1 }), msg({ id: 2 }), msg({ id: 3, memberId: 1 }), msg({ id: 4 }),
    ]} />)
    expect(list.scrollTop).toBe(1000)
    expect(screen.queryByRole('button', { name: /条新消息/ })).toBeNull()
  })

  it('底部时 typing 或 footer 增长继续滚到底，上翻时保持当前位置', () => {
    const messages = [msg({ id: 1 })]
    const view = render(<MessageList selfMemberId={1} messages={messages} />)
    const list = screen.getByTestId('message-list')
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1000 })
    view.rerender(<MessageList selfMemberId={1} messages={messages} typing />)
    expect(list.scrollTop).toBe(1000)
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1200 })
    view.rerender(<MessageList selfMemberId={1} messages={messages} typing footer={<p>结束</p>} />)
    expect(list.scrollTop).toBe(1200)
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1400 })
    view.rerender(<MessageList selfMemberId={1} messages={messages} typing footer={<p>结束原因变化</p>} />)
    expect(list.scrollTop).toBe(1400)
    scrollAway()
    view.rerender(<MessageList selfMemberId={1} messages={messages} footer={<p>再次变化</p>} />)
    expect(list.scrollTop).toBe(100)
  })

  it('500 条裁剪后仍计数；首条变化、加载历史、删除不清零；点击后滚到底并清零', () => {
    const messages = Array.from({ length: 500 }, (_, i) => msg({ id: i + 1, body: `message-${i + 1}` }))
    const view = render(<MessageList selfMemberId={1} messages={messages} conversationKey="a" />)
    const list = scrollAway()
    const cropped = [...messages.slice(1), msg({ id: 501 })]
    view.rerender(<MessageList selfMemberId={1} messages={cropped} conversationKey="a" />)
    expect(screen.getByRole('button', { name: '1 条新消息' })).toBeInTheDocument()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 0 }), ...cropped]} conversationKey="a" />)
    expect(screen.getByRole('button', { name: '1 条新消息' })).toBeInTheDocument()
    const deleted = cropped.slice(10, -1)
    view.rerender(<MessageList selfMemberId={1} messages={deleted} conversationKey="a" />)
    expect(screen.getByRole('button', { name: '1 条新消息' })).toBeInTheDocument()
    // 删除最大 id 后重载旧消息不能重复计数，水位只能增加。
    view.rerender(<MessageList selfMemberId={1} messages={[...deleted, msg({ id: 501 }), msg({ id: 502 })]} conversationKey="a" />)
    fireEvent.click(screen.getByRole('button', { name: '2 条新消息' }))
    expect(list.scrollTop).toBe(1000)
    expect(screen.queryByRole('button', { name: /条新消息/ })).toBeNull()
  })

  it('加载更早历史不计数；批量新消息计数，用户滚到底清零，会话切换重置水位', () => {
    const view = render(<MessageList selfMemberId={1} messages={[msg({ id: 10 })]} conversationKey="a" />)
    const list = scrollAway()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 9 }), msg({ id: 10 })]} conversationKey="a" />)
    expect(screen.queryByRole('button', { name: /条新消息/ })).toBeNull()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 10 }), msg({ id: 11 }), msg({ id: 12 })]} conversationKey="a" />)
    expect(screen.getByRole('button', { name: '2 条新消息' })).toBeInTheDocument()
    fireEvent.scroll(list, { target: { scrollTop: 800 } })
    expect(screen.queryByRole('button', { name: /条新消息/ })).toBeNull()
    scrollAway()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 13 })]} conversationKey="a" />)
    expect(screen.getByRole('button', { name: '1 条新消息' })).toBeInTheDocument()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 1 })]} conversationKey="b" />)
    expect(screen.queryByRole('button', { name: /条新消息/ })).toBeNull()
    scrollAway()
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 1 }), msg({ id: 2 })]} conversationKey="b" />)
    expect(screen.getByRole('button', { name: '1 条新消息' })).toBeInTheDocument()
  })

  it('在底部时按最大 id 自动滚动，长度不增长也滚动', () => {
    const view = render(<MessageList selfMemberId={1} messages={[msg({ id: 1 })]} />)
    const list = screen.getByTestId('message-list')
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1000 })
    view.rerender(<MessageList selfMemberId={1} messages={[msg({ id: 2 })]} />)
    expect(list.scrollTop).toBe(1000)
    expect(screen.queryByRole('button', { name: /条新消息/ })).toBeNull()
  })
})
