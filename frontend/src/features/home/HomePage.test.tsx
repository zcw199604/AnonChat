import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import HomePage from './HomePage'

/**
 * 首页布局（how.md 2.2）：已移除在线人数卡、快捷入口 section、≥1440px 浮动胶囊、
 * 广播改为资料卡上方的窄横幅，品牌行已移除（品牌名改由 TopBar 渲染），
 * 资料卡也不再包含 VIP 解锁的匹配偏好、VIP 徽标与昵称编辑。
 * 产品只保留「匿名匹配」，因此首页主体是广播横幅+资料匹配卡。
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
  setViewport(1280)
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

describe('首页不再显示在线人数与快捷入口', () => {
  it('不存在在线人数卡、快捷入口 section 与浮动胶囊', () => {
    renderHome()

    expect(screen.queryByTestId('online-card')).toBeNull()
    expect(screen.queryByTestId('floating-entries')).toBeNull()
    expect(screen.queryByText('快捷入口')).toBeNull()
  })

  it('1440px 视口下也不出现浮动胶囊', () => {
    setViewport(1440)
    renderHome()

    expect(screen.queryByTestId('floating-entries')).toBeNull()
    expect(screen.queryByText('快捷入口')).toBeNull()
  })
})

describe('首页不显示话题房间与匿名动态', () => {
  it('不存在话题房间与匿名动态区块', () => {
    renderHome()

    expect(screen.queryByTestId('feed-card-wrapper')).toBeNull()
    expect(screen.queryByText('话题房间')).toBeNull()
    expect(screen.queryByText('匿名动态')).toBeNull()
  })

  it('1440px 视口下也不出现话题房间与匿名动态', () => {
    setViewport(1440)
    renderHome()

    expect(screen.queryByText('话题房间')).toBeNull()
    expect(screen.queryByText('匿名动态')).toBeNull()
  })
})

describe('首页只保留匿名匹配', () => {
  it('不出现旧广播卡，改为渲染窄横幅（品牌行改由 TopBar 渲染）', () => {
    renderHome()

    // 广播保留，但以窄横幅形式出现在资料卡上方。
    expect(screen.getByTestId('broadcast-banner')).toBeInTheDocument()
    expect(screen.queryByText('AnonChat 匿名聊天')).toBeNull()
    expect(screen.queryByText('ANONYMOUS CHAT')).toBeNull()
    // 连接状态圆点已移到 TopBar。
    expect(screen.queryByTestId('ws-connected')).toBeNull()
  })

  it('不出现昵称编辑入口与 VIP 徽标', () => {
    renderHome()

    expect(screen.queryByLabelText('编辑昵称')).toBeNull()
    expect(screen.queryByLabelText('随机昵称')).toBeNull()
    expect(screen.queryByText(/VIP 至 \d{4}-\d{2}-\d{2}/)).toBeNull()
    expect(screen.queryByTestId('vip-lock')).toBeNull()
  })
})
