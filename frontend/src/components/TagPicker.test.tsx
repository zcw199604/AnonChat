import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TAGS } from '../lib/profileConsts'
import { TagPicker } from './TagPicker'

/** 标签选择：默认渲染全部；传入 collapsedCount 时折叠为前 N 个 + 「更多 +N」。 */
describe('TagPicker', () => {
  it('不传 collapsedCount 时渲染全部标签，且没有「更多」按钮', () => {
    render(<TagPicker selected={[]} onChange={() => undefined} />)

    for (const tag of TAGS) {
      expect(screen.getByRole('button', { name: tag })).toBeInTheDocument()
    }
    expect(screen.queryByRole('button', { name: /更多/ })).toBeNull()
    expect(screen.queryByRole('button', { name: '收起' })).toBeNull()
    expect(screen.getByText(`0/5`)).toBeInTheDocument()
  })

  it('collapsedCount=5 且无已选时只渲染 5 个标签与「更多 +13」，可展开与收起', async () => {
    const user = userEvent.setup()
    render(<TagPicker collapsedCount={5} selected={[]} onChange={() => undefined} />)

    expect(screen.getAllByRole('button', { pressed: false })).toHaveLength(5)
    for (const tag of TAGS.slice(0, 5)) {
      expect(screen.getByRole('button', { name: tag })).toBeInTheDocument()
    }
    expect(screen.queryByRole('button', { name: TAGS[5] })).toBeNull()

    const more = screen.getByRole('button', { name: '更多 +13' })
    expect(more).toHaveAttribute('aria-expanded', 'false')

    await user.click(more)
    for (const tag of TAGS) {
      expect(screen.getByRole('button', { name: tag })).toBeInTheDocument()
    }
    expect(screen.queryByRole('button', { name: /更多/ })).toBeNull()
    const collapse = screen.getByRole('button', { name: '收起' })
    expect(collapse).toHaveAttribute('aria-expanded', 'true')

    await user.click(collapse)
    expect(screen.queryByRole('button', { name: TAGS[5] })).toBeNull()
    expect(screen.getByRole('button', { name: '更多 +13' })).toBeInTheDocument()
  })

  it('已选标签位于列表后部时折叠状态下仍可见且为激活态', () => {
    render(<TagPicker collapsedCount={5} selected={['二次元']} onChange={() => undefined} />)

    const picked = screen.getByRole('button', { name: '二次元' })
    expect(picked).toHaveAttribute('aria-pressed', 'true')
    // 已选优先 + 未选补足到 5 个 → 隐藏 13 个。
    expect(screen.getAllByRole('button', { pressed: true })).toHaveLength(1)
    expect(screen.getByRole('button', { name: '更多 +13' })).toBeInTheDocument()
    expect(screen.getByText('1/5')).toBeInTheDocument()
  })
})
