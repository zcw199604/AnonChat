/** 身份状态（Zustand）。初始化、换身份、资料与权益快照；换身份后触发重置回调。 */
import { create } from 'zustand'
import { postIdentity, getMe, resetIdentity } from '../lib/api/client'
import type { MeInfo } from '../lib/api/client'

export type VerificationMode = 'off' | 'optional' | 'required'

interface IdentityState {
  nickname: string
  avatarSeed: string
  verified: boolean
  verificationMode: VerificationMode
  /** 匿名编号（10 位 Crockford Base32）。 */
  publicCode: string
  /** 服务端返回的 me 快照（资料、匹配偏好、VIP、广播卡）。 */
  me: MeInfo | null
  initialized: boolean
  init: () => Promise<void>
  reset: () => Promise<void>
  setVerified: (v: boolean) => void
  /** 重新拉取 me（资料修改、兑换激活码后调用）。 */
  refreshMe: () => Promise<void>
}

const resetHandlers = new Set<() => void | Promise<void>>()

/** 注册换身份后的重置回调。 */
export function onIdentityReset(fn: () => void | Promise<void>): () => void {
  resetHandlers.add(fn)
  return () => resetHandlers.delete(fn)
}

export const useIdentityStore = create<IdentityState>((set, get) => ({
  nickname: '',
  avatarSeed: '',
  verified: false,
  verificationMode: 'off',
  publicCode: '',
  me: null,
  initialized: false,
  init: async () => {
    const info = await postIdentity()
    let mode: VerificationMode = 'off'
    let verified = info.verified
    let me: MeInfo | null = null
    try {
      me = await getMe()
      mode = me.verificationMode
      verified = me.verified
    } catch {
      // 忽略：me 失败不阻塞初始化
    }
    set({
      nickname: me?.nickname ?? info.nickname,
      avatarSeed: me?.avatarSeed ?? info.avatarSeed,
      verified,
      verificationMode: mode,
      publicCode: me?.publicCode ?? '',
      me,
      initialized: true,
    })
  },
  reset: async () => {
    const info = await resetIdentity()
    set({
      nickname: info.nickname,
      avatarSeed: info.avatarSeed,
      verified: info.verified,
      publicCode: '',
      me: null,
    })
    for (const h of resetHandlers) {
      await h()
    }
    // 换身份后权益随新身份重置，重新拉取一次 me。
    void get().refreshMe()
  },
  setVerified: (v) => set({ verified: v }),
  refreshMe: async () => {
    try {
      const me = await getMe()
      set({
        me,
        nickname: me.nickname,
        avatarSeed: me.avatarSeed,
        verified: me.verified,
        verificationMode: me.verificationMode,
        publicCode: me.publicCode ?? '',
      })
    } catch {
      // 忽略：刷新失败保留旧快照
    }
  },
}))
