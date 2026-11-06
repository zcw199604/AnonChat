import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import App from './App.tsx'

describe('App', () => {
  it('在首页渲染占位标题', () => {
    render(
      <MemoryRouter>
        <App />
      </MemoryRouter>,
    )
    expect(screen.getByRole('heading', { name: 'AnonChat' })).toBeInTheDocument()
    expect(screen.getByText('匿名聊天，打开就能聊。')).toBeInTheDocument()
  })
})
