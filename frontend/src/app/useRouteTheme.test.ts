import { afterEach, describe, it, expect, vi } from 'vitest'
import { renderHook, act, cleanup } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { isChatRoute, readLobbyTheme, routeTheme, THEME_STORAGE_KEY, useRouteTheme } from './useRouteTheme'
import { useUiStore } from '../stores/ui'

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.unstubAllGlobals()
  useUiStore.setState({ lobbyTheme: 'light', inPrivateConversation: false })
  delete document.documentElement.dataset.theme
})

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

  it('聊天类路由跟随白天和夜晚偏好，仍隐藏顶栏', () => {
    for (const path of [
      '/match',
      '/rooms/room-1',
      '/bottles/conv-1',
      '/stories/story-1/play',
    ]) {
      expect(routeTheme(path, 'light')).toBe('light')
      expect(routeTheme(path, 'dark')).toBe('dark')
      expect(isChatRoute(path)).toBe(true)
    }
  })

  it.each(['light', 'dark'] as const)('/p/:token 进入和退出会话都保持 %s 偏好，仅改变顶栏显隐', (pref) => {
    useUiStore.setState({ lobbyTheme: pref })
    const { result } = renderHook(() => useRouteTheme(), {
      wrapper: ({ children }) => createElement(MemoryRouter, { initialEntries: ['/p/token-abc'] }, children),
    })
    for (const inConversation of [false, true, false]) {
      act(() => useUiStore.getState().setInPrivateConversation(inConversation))
      expect(result.current.theme).toBe(pref)
      expect(document.documentElement.dataset.theme).toBe(pref)
      expect(isChatRoute('/p/token-abc', inConversation)).toBe(inConversation)
    }
  })

  it('/admin 固定使用浅色大厅 token', () => {
    expect(routeTheme('/admin', 'dark')).toBe('light')
    expect(routeTheme('/admin/reports', 'dark')).toBe('light')
    expect(routeTheme('/admin', 'light')).toBe('light')
    expect(isChatRoute('/')).toBe(false)
  })

  it('未知路由回退到用户偏好', () => {
    expect(routeTheme('/unknown', 'dark')).toBe('dark')
  })
})

describe('readLobbyTheme：已存偏好优先，否则跟随系统', () => {
  it.each([true, false])('无存储值时跟随系统 dark=%s', (matches) => {
    const matchMedia = vi.fn().mockReturnValue({ matches })
    vi.stubGlobal('matchMedia', matchMedia)
    expect(readLobbyTheme()).toBe(matches ? 'dark' : 'light')
    expect(matchMedia).toHaveBeenCalledWith('(prefers-color-scheme: dark)')
  })

  it.each(['light', 'dark'] as const)('显式 %s 偏好优先于系统', (pref) => {
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: pref === 'light' }))
    localStorage.setItem(THEME_STORAGE_KEY, pref)
    expect(readLobbyTheme()).toBe(pref)
  })

  it('无效存储值也跟随系统', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'invalid')
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: true }))
    expect(readLobbyTheme()).toBe('dark')
  })

  it('matchMedia 不可用时回退浅色', () => {
    vi.stubGlobal('matchMedia', undefined)
    expect(readLobbyTheme()).toBe('light')
  })
})
