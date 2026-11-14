import { useCallback, useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import { useUiStore } from '../stores/ui'
import { readLobbyTheme, saveLobbyTheme } from '../lib/theme'
import type { Theme } from '../lib/theme'

export { readLobbyTheme, saveLobbyTheme, THEME_STORAGE_KEY } from '../lib/theme'
export type { Theme } from '../lib/theme'

/** 聊天类路由匹配（/p/:token 需进入会话后才算）。 */
const CHAT_PATTERNS: RegExp[] = [
  /^\/match(\/|$)/,
  /^\/rooms\/[^/]+/,
  /^\/bottles\/[^/]+/,
  /^\/stories\/[^/]+\/play$/,
]

/** /p/:token 只有在进入会话后才隐藏大厅顶栏。 */
const PRIVATE_PATTERN = /^\/p\/[^/]+$/

/** 是否为聊天类路由（决定是否显示大厅顶栏）。 */
export function isChatRoute(pathname: string, inPrivateConversation = false): boolean {
  if (CHAT_PATTERNS.some((re) => re.test(pathname))) return true
  return PRIVATE_PATTERN.test(pathname) && inPrivateConversation
}

/**
 * 判定当前路由应使用的主题（纯函数，便于单测）。
 */
export function routeTheme(
  pathname: string,
  lobbyPref: Theme,
): Theme {
  if (pathname.startsWith('/admin')) return 'light'
  return lobbyPref
}

/** 根据路由与用户偏好算出主题，并写到 <html data-theme>。 */
export function useRouteTheme(): {
  theme: Theme
  lobbyPref: Theme
  toggleLobbyTheme: () => void
} {
  const { pathname } = useLocation()
  const lobbyPref = useUiStore((s) => s.lobbyTheme)
  const setLobbyTheme = useUiStore((s) => s.setLobbyTheme)

  const theme = routeTheme(pathname, lobbyPref)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
  }, [theme])

  const toggleLobbyTheme = useCallback(() => {
    const next: Theme = lobbyPref === 'dark' ? 'light' : 'dark'
    setLobbyTheme(next)
    saveLobbyTheme(next)
  }, [lobbyPref, setLobbyTheme])

  return { theme, lobbyPref, toggleLobbyTheme }
}

/** 应用启动时把 localStorage 中的偏好写回 UI store。 */
export function syncLobbyThemeFromStorage(): void {
  useUiStore.getState().setLobbyTheme(readLobbyTheme())
}
