import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import App from './App.tsx'
import { useIdentityStore } from '../stores/identity'
import { ApiError } from '../lib/api/client'

describe('App', () => {
  it('在首页渲染品牌标题与匹配按钮', () => {
    render(
      <MemoryRouter>
        <App />
      </MemoryRouter>,
    )
    expect(screen.getByRole('heading', { name: /AnonChat 匿名聊天/ })).toBeInTheDocument()
    expect(screen.getByTestId('match-button')).toBeInTheDocument()
  })
})

it.each([
  ['rate_limited', '身份创建过于频繁，请稍后再试'],
  ['banned', '当前网络已被封禁，请等待封禁解除后重试'],
])('身份初始化 %s 时展示提示页', async (code, text) => {
  const init = vi.spyOn(useIdentityStore.getState(), 'init').mockRejectedValue(new ApiError(code, text, 429))
  const view = render(<MemoryRouter><App /></MemoryRouter>)
  expect(await screen.findByRole('alert')).toHaveTextContent(text)
  view.unmount()
  init.mockRestore()
})
