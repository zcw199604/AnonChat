/** 主题类型与大厅明暗偏好的持久化（how.md 1.3）。 */
export type Theme = 'light' | 'dark'

/** 大厅明暗偏好的 localStorage 键。 */
export const THEME_STORAGE_KEY = 'anonchat.theme'

/** 读取大厅明暗偏好（默认浅色）。 */
export function readLobbyTheme(): Theme {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    return v === 'dark' ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

/** 保存大厅明暗偏好。 */
export function saveLobbyTheme(t: Theme): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, t)
  } catch {
    // 隐私模式下写入失败时忽略。
  }
}
