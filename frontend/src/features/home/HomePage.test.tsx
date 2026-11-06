import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import HomePage from './HomePage'

/**
 * 首页布局（how.md 2.2，审查意见 V1、V2）。
 * 视觉高度无法在 jsdom 中测量，这里用结构/类名断言固定住两件必须成立的事：
 * 1. 在线人数卡不再被拉伸（不占满整行高度），右列剩余高度交给匿名动态卡；
 * 2. 浮动胶囊只在视口 ≥1440px 时出现。
 */

// 视口宽度感知的 matchMedia 桩：让 min-width 断言按 innerWidth 求值。
function stubMatchMedia() {
  window.matchMedia = ((query: string) => {
    const m = /min-width:\s*(\d+)px/.exec(query)
    const matches = m ? window.innerWidth >= Number(m[1]) : false
    return {
      matches,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    } as unknown as MediaQueryList
  }) as typeof window.matchMedia
}

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width })
}

beforeEach(() => {
  stubMatchMedia()
})

afterEach(() => {
  setViewport(1024)
})

function renderHome() {
  return render(
    <MemoryRouter>
      <HomePage />
    </MemoryRouter>,
  )
}

describe('首页右列布局', () => {
  it('在线人数卡按内容自适应高度，匿名动态卡填满右列剩余高度', () => {
    setViewport(1280)
    renderHome()

    const online = screen.getByTestId('online-card')
    // 不再用 h-full 拉满整行（此前被资料大卡撑出约 330px 空白）。
    expect(online.className).not.toContain('h-full')

    const side = screen.getByTestId('home-side-column')
    // 桌面端右列是纵向 flex，匿名动态占据剩余高度。
    expect(side.className).toContain('md:flex')
    expect(side.className).toContain('md:flex-col')
    const feed = screen.getByTestId('feed-card-wrapper')
    expect(feed.className).toContain('md:flex-1')
  })
})

describe('浮动胶囊的显示阈值', () => {
  it('1280px 宽时不显示浮动胶囊（避免遮挡匿名动态卡）', () => {
    setViewport(1280)
    renderHome()
    expect(screen.queryByTestId('floating-entries')).toBeNull()
  })

  it('1440px 宽时显示浮动胶囊', () => {
    setViewport(1440)
    renderHome()
    expect(screen.getByTestId('floating-entries')).toBeInTheDocument()
  })
})
