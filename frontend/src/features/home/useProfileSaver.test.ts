import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

const patchProfile = vi.fn()
const refreshMe = vi.fn()

vi.mock('../../lib/api/client', () => ({
  patchProfile: (...args: unknown[]) => patchProfile(...args),
}))
vi.mock('../../stores/identity', () => {
  const state = {
    refreshMe: () => refreshMe(),
    getState: () => state,
    setState: vi.fn(),
    subscribe: vi.fn(),
  }
  const useIdentityStore = (selector?: (s: unknown) => unknown) => (selector ? selector(state) : state)
  useIdentityStore.setState = vi.fn()
  useIdentityStore.getState = () => state
  return { useIdentityStore }
})

import { useProfileSaver, SAVE_DEBOUNCE_MS } from './useProfileSaver'
import type { MeInfo } from '../../lib/api/client'

const me: MeInfo = {
  nickname: '测试',
  avatarSeed: 'seed',
  verified: false,
  verificationMode: 'off',
  publicCode: 'ABCDEFGHIJ',
  profile: {
    gender: 'secret',
    age: null,
    province: null,
    provincePublic: false,
    tags: [],
    cleanMode: false,
  },
  matchPrefs: { gender: 'any', sameProvince: false },
  vip: { active: true, until: null },
  broadcastCards: 0,
}

describe('useProfileSaver：防抖保存与失败回滚', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    patchProfile.mockReset()
    refreshMe.mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('500ms 内的多次修改合并成一次请求', async () => {
    patchProfile.mockResolvedValue(me)
    const onError = vi.fn()
    const { result } = renderHook(() => useProfileSaver(me, onError))

    act(() => {
      result.current.update({ profile: { gender: 'female' } })
      result.current.update({ profile: { age: 24 } })
    })
    // 防抖未到期前不发请求。
    expect(patchProfile).not.toHaveBeenCalled()
    await act(async () => {
      vi.advanceTimersByTime(SAVE_DEBOUNCE_MS)
      await Promise.resolve()
    })
    expect(patchProfile).toHaveBeenCalledTimes(1)
    expect(patchProfile).toHaveBeenCalledWith({ gender: 'female', age: 24 })
    expect(onError).not.toHaveBeenCalled()
  })

  it('保存失败时回滚并回调错误码', async () => {
    patchProfile.mockRejectedValue({ code: 'vip_required' })
    const onError = vi.fn()
    const { result } = renderHook(() => useProfileSaver(me, onError))

    act(() => {
      result.current.update({ prefs: { gender: 'female' } })
    })
    await act(async () => {
      vi.advanceTimersByTime(SAVE_DEBOUNCE_MS)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(onError).toHaveBeenCalledWith('vip_required')
    expect(result.current.profile?.gender).toBe('secret')
    expect(refreshMe).toHaveBeenCalled()
  })

  it('flush 立即提交未保存的改动', async () => {
    patchProfile.mockResolvedValue(me)
    const { result } = renderHook(() => useProfileSaver(me, vi.fn()))
    act(() => {
      result.current.update({ profile: { cleanMode: true } })
    })
    await act(async () => {
      await result.current.flush()
    })
    expect(patchProfile).toHaveBeenCalledWith({ cleanMode: true })
  })
})
