import { describe, it, expect } from 'vitest'
import { routeTheme } from './useRouteTheme'

describe('routeTheme：路由与明暗主题的判定（how.md 1.3）', () => {
  it('大厅类路由跟随用户偏好', () => {
    expect(routeTheme('/', 'light')).toBe('light')
    expect(routeTheme('/', 'dark')).toBe('dark')
    expect(routeTheme('/rooms', 'light')).toBe('light')
    expect(routeTheme('/bottles', 'dark')).toBe('dark')
    expect(routeTheme('/p/new', 'light')).toBe('light')
    expect(routeTheme('/vip', 'light')).toBe('light')
    expect(routeTheme('/stories', 'light')).toBe('light')
    expect(routeTheme('/stories/new', 'dark')).toBe('dark')
    expect(routeTheme('/works/abc', 'light')).toBe('light')
  })

  it('聊天类路由固定暗色，忽略用户偏好', () => {
    for (const path of [
      '/match',
      '/rooms/room-1',
      '/bottles/conv-1',
      '/stories/story-1/play',
    ]) {
      expect(routeTheme(path, 'light')).toBe('dark')
      expect(routeTheme(path, 'dark')).toBe('dark')
    }
  })

  it('/p/:token 只有在进入会话后才切暗色', () => {
    expect(routeTheme('/p/token-abc', 'light', false)).toBe('light')
    expect(routeTheme('/p/token-abc', 'light', true)).toBe('dark')
  })

  it('/admin 固定使用浅色大厅 token', () => {
    expect(routeTheme('/admin', 'dark')).toBe('light')
    expect(routeTheme('/admin/reports', 'dark')).toBe('light')
  })

  it('未知路由回退到用户偏好', () => {
    expect(routeTheme('/unknown', 'dark')).toBe('dark')
  })
})
