/**
 * 资料保存：防抖 500ms 后调用 PATCH /api/identity/profile；
 * 失败时回滚到服务端快照并提示（how.md 2.2）。
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import { patchProfile } from '../../lib/api/client'
import type { MatchPrefs, MeInfo, ProfileInfo } from '../../lib/api/client'
import { useIdentityStore } from '../../stores/identity'

/** 防抖间隔（毫秒）。 */
export const SAVE_DEBOUNCE_MS = 500

export interface ProfilePatchDraft {
  profile?: Partial<ProfileInfo>
  prefs?: Partial<MatchPrefs>
}

export interface ProfileSaver {
  /** 本地乐观值（未提供时用服务端快照）。 */
  draft: ProfilePatchDraft
  profile: ProfileInfo | undefined
  prefs: MatchPrefs | undefined
  /** 记录一次修改并安排防抖保存。 */
  update: (patch: ProfilePatchDraft) => void
  /** 立即提交（点击「匹配」或卸载前调用）。 */
  flush: () => Promise<void>
  /** 是否还有未提交的修改。 */
  pending: () => boolean
}

/**
 * useProfileSaver 管理资料的乐观更新、防抖保存与回滚。
 * onError 由调用方负责 toast。
 */
export function useProfileSaver(me: MeInfo | null, onError: (code?: string) => void): ProfileSaver {
  const refreshMe = useIdentityStore((s) => s.refreshMe)
  const [draft, setDraft] = useState<ProfilePatchDraft>({})
  const timer = useRef<number | null>(null)
  const accumulated = useRef<ProfilePatchDraft>({})

  const send = useCallback(async () => {
    const patch = accumulated.current
    const hasProfile = Boolean(patch.profile && Object.keys(patch.profile).length > 0)
    const hasPrefs = Boolean(patch.prefs && Object.keys(patch.prefs).length > 0)
    if (!hasProfile && !hasPrefs) return
    accumulated.current = {}
    try {
      const body: Record<string, unknown> = {}
      if (patch.profile && Object.keys(patch.profile).length > 0) Object.assign(body, patch.profile)
      if (patch.prefs && Object.keys(patch.prefs).length > 0) body.matchPrefs = patch.prefs
      const updated = await patchProfile(body)
      useIdentityStore.setState({ me: updated })
      setDraft({})
    } catch (e) {
      // 回滚：丢弃本地乐观值，回到服务端快照。
      setDraft({})
      onError((e as { code?: string }).code)
      await refreshMe()
    }
  }, [onError, refreshMe])

  const flush = useCallback(async () => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current)
      timer.current = null
    }
    await send()
  }, [send])

  const update = useCallback(
    (patch: ProfilePatchDraft) => {
      setDraft((prev) => ({
        profile: { ...prev.profile, ...patch.profile },
        prefs: { ...prev.prefs, ...patch.prefs },
      }))
      accumulated.current = {
        profile: { ...accumulated.current.profile, ...patch.profile },
        prefs: { ...accumulated.current.prefs, ...patch.prefs },
      }
      if (timer.current !== null) window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => {
        timer.current = null
        void send()
      }, SAVE_DEBOUNCE_MS)
    },
    [send],
  )

  // 卸载时立即提交未保存的改动（避免切换页面丢失）。
  useEffect(() => {
    return () => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current)
        timer.current = null
        void send()
      }
    }
  }, [send])

  return {
    draft,
    profile: draft.profile ? { ...(me?.profile as ProfileInfo), ...draft.profile } : me?.profile,
    prefs: draft.prefs ? { ...(me?.matchPrefs as MatchPrefs), ...draft.prefs } : me?.matchPrefs,
    update,
    flush,
    pending: () => Boolean(accumulated.current.profile || accumulated.current.prefs),
  }
}
