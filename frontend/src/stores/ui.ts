/** 全局 UI 状态：提示、实名弹窗、被踢出、主题、抽屉。 */
import { create } from 'zustand'
import type { Theme } from '../lib/theme'

interface UiState {
  toast: string
  verificationOpen: boolean
  kicked: string | null
  connected: boolean
  /** 大厅明暗偏好（how.md 1.3，持久化在 localStorage）。 */
  lobbyTheme: Theme
  /** /p/:token 是否已进入会话（决定是否切换暗色）。 */
  inPrivateConversation: boolean
  /** 汉堡抽屉是否打开。 */
  drawerOpen: boolean
  setToast: (msg: string) => void
  clearToast: () => void
  openVerification: () => void
  closeVerification: () => void
  setKicked: (reason: string) => void
  setConnected: (v: boolean) => void
  setLobbyTheme: (t: Theme) => void
  setInPrivateConversation: (v: boolean) => void
  setDrawerOpen: (v: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  toast: '',
  verificationOpen: false,
  kicked: null,
  connected: false,
  lobbyTheme: 'light',
  inPrivateConversation: false,
  drawerOpen: false,
  setToast: (msg) => set({ toast: msg }),
  clearToast: () => set({ toast: '' }),
  openVerification: () => set({ verificationOpen: true }),
  closeVerification: () => set({ verificationOpen: false }),
  setKicked: (reason) => set({ kicked: reason }),
  setConnected: (v) => set({ connected: v }),
  setLobbyTheme: (t) => set({ lobbyTheme: t }),
  setInPrivateConversation: (v) => set({ inPrivateConversation: v }),
  setDrawerOpen: (v) => set({ drawerOpen: v }),
}))
