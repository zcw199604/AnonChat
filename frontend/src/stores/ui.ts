/** 全局 UI 状态：提示、实名弹窗、被踢出。 */
import { create } from 'zustand'

interface UiState {
  toast: string
  verificationOpen: boolean
  kicked: string | null
  connected: boolean
  setToast: (msg: string) => void
  clearToast: () => void
  openVerification: () => void
  closeVerification: () => void
  setKicked: (reason: string) => void
  setConnected: (v: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  toast: '',
  verificationOpen: false,
  kicked: null,
  connected: false,
  setToast: (msg) => set({ toast: msg }),
  clearToast: () => set({ toast: '' }),
  openVerification: () => set({ verificationOpen: true }),
  closeVerification: () => set({ verificationOpen: false }),
  setKicked: (reason) => set({ kicked: reason }),
  setConnected: (v) => set({ connected: v }),
}))
