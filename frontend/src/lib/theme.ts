/** 主题类型与大厅明暗偏好的持久化（how.md 1.3）。 */
export type Theme = 'light' | 'dark'

/** 大厅明暗偏好的 localStorage 键。 */
export const THEME_STORAGE_KEY = 'anonchat.theme'

/** 读取明暗偏好；未设置时跟随系统，不支持系统主题时使用浅色。 */
export function readLobbyTheme(): Theme {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    // 无法读取存储时仍可跟随系统主题。
  }
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches
    ? 'dark'
    : 'light'
}

/** 保存大厅明暗偏好。 */
export function saveLobbyTheme(t: Theme): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, t)
  } catch {
    // 隐私模式下写入失败时忽略。
  }
}
