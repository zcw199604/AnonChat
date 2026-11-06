/** 房间在线人数状态（Zustand）。 */
import { create } from 'zustand'

interface PresenceState {
  online: Record<string, number>
  setOnline: (roomId: string, online: number) => void
  clearAll: () => void
}

export const usePresenceStore = create<PresenceState>((set) => ({
  online: {},
  setOnline: (roomId, online) => set((s) => ({ online: { ...s.online, [roomId]: online } })),
  clearAll: () => set({ online: {} }),
}))
