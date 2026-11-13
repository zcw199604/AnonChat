import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TopBar } from './TopBar'

/**
 * 顶栏：☰ 抽屉入口已隐藏（功能精简），品牌名左对齐，右侧保留状态点与两个按钮。
 */
describe('TopBar', () => {
  it('不存在「菜单」按钮（抽屉已隐藏）', () => {
    render(
      <MemoryRouter>
        <TopBar />
      </MemoryRouter>,
    )

    expect(screen.queryByRole('button', { name: '菜单' })).toBeNull()
    expect(screen.queryByRole('navigation')).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('仍渲染品牌名与右侧按钮', () => {
    render(
      <MemoryRouter>
        <TopBar />
      </MemoryRouter>,
    )

    expect(screen.getByRole('heading', { level: 1, name: /AnonChat/ })).toBeInTheDocument()
    expect(screen.getByText('ANONYMOUS CHAT')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '添加到主屏幕' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '切换明暗主题' })).toBeInTheDocument()
  })
})
