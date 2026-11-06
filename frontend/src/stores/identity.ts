/** 身份状态（Zustand）。初始化、换身份；换身份后触发重置回调（重连 WS、清空本地会话）。 */
import { create } from 'zustand'
import { postIdentity, getMe, resetIdentity } from '../lib/api/client'

export type VerificationMode = 'off' | 'optional' | 'required'

interface IdentityState {
  nickname: string
  avatarSeed: string
  verified: boolean
  verificationMode: VerificationMode
  initialized: boolean
  init: () => Promise<void>
  reset: () => Promise<void>
  setVerified: (v: boolean) => void
}

const resetHandlers = new Set<() => void | Promise<void>>()

/** 注册换身份后的重置回调。 */
export function onIdentityReset(fn: () => void | Promise<void>): () => void {
  resetHandlers.add(fn)
  return () => resetHandlers.delete(fn)
}

export const useIdentityStore = create<IdentityState>((set) => ({
  nickname: '',
  avatarSeed: '',
  verified: false,
  verificationMode: 'off',
  initialized: false,
  init: async () => {
    const info = await postIdentity()
    let mode: VerificationMode = 'off'
    let verified = info.verified
    try {
      const me = await getMe()
      mode = me.verificationMode
      verified = me.verified
    } catch {
      // 忽略：me 失败不阻塞初始化
    }
    set({
      nickname: info.nickname,
      avatarSeed: info.avatarSeed,
      verified,
      verificationMode: mode,
      initialized: true,
    })
  },
  reset: async () => {
    const info = await resetIdentity()
    set({ nickname: info.nickname, avatarSeed: info.avatarSeed, verified: info.verified })
    for (const h of resetHandlers) {
      await h()
    }
  },
  setVerified: (v) => set({ verified: v }),
}))
