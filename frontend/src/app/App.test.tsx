import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import App from './App.tsx'

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
