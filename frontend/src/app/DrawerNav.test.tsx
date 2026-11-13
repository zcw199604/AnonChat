import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { DrawerNav } from './DrawerNav'

/**
 * 抽屉导航：产品只保留「匿名匹配」，因此条目只剩「首页」与「随机匹配」，
 * 另加一个「换个身份」按钮；其余页面不再出现在菜单里。
 */
describe('DrawerNav', () => {
  it('只有「首页」「随机匹配」两个导航项', () => {
    render(
      <MemoryRouter>
        <DrawerNav onNavigate={() => undefined} />
      </MemoryRouter>,
    )

    const links = screen.getAllByRole('link')
    expect(links).toHaveLength(2)
    expect(links[0]).toHaveAttribute('href', '/')
    expect(links[0]).toHaveTextContent('首页')
    expect(links[1]).toHaveAttribute('href', '/match')
    expect(links[1]).toHaveTextContent('随机匹配')

    for (const label of ['话题房间', '匿名信', '私密链接', '故事书（公测）', 'VIP 与激活码']) {
      expect(screen.queryByText(label)).toBeNull()
    }
    // 「换个身份」保留。
    expect(screen.getByRole('button', { name: /换个身份/ })).toBeInTheDocument()
  })
})
