import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card } from '../../components/Card'
import { Skeleton } from '../../components/Skeleton'
import { GradientAvatar } from '../../components/GradientAvatar'
import { RangeSlider } from '../../components/RangeSlider'
import { Segmented } from '../../components/Segmented'
import { TagPicker } from '../../components/TagPicker'
import { Toggle } from '../../components/Toggle'
import { VipLock } from '../../components/VipLock'
import { ResetIdentityConfirm } from '../../components/ResetIdentityConfirm'
import { useIdentityStore } from '../../stores/identity'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { setNickname, shuffleNickname } from '../../lib/api/client'
import type { Gender, MatchPrefs, ProfileInfo } from '../../lib/api/client'
import { useProfileSaver } from './useProfileSaver'
import { MAX_AGE, MIN_AGE, PROVINCES } from '../../lib/profileConsts'

/** 折叠状态保存在 localStorage。 */
const COLLAPSE_KEY = 'anonchat.profileCollapsed'

interface ProfileCardProps {
  onMatch: () => void
  nickname: string
}

/** 资料卡 + 匹配大卡（how.md 2.2）：接通 me、PATCH profile、昵称接口。 */
export function ProfileCard({ onMatch, nickname: propNickname }: ProfileCardProps) {
  const nav = useNavigate()
  const me = useIdentityStore((s) => s.me)
  const publicCode = useIdentityStore((s) => s.publicCode)
  const setToast = useUiStore((s) => s.setToast)

  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(COLLAPSE_KEY) === '1'
    } catch {
      return false
    }
  })
  // nicknameEdit 为 null 时使用服务端昵称，避免用 effect 同步 prop。
  const [nicknameEdit, setNicknameEdit] = useState<string | null>(null)
  const [editingNickname, setEditingNickname] = useState(false)
  const saver = useProfileSaver(me, (code) => setToast(errorMessage(code)))

  const profile = saver.profile
  const prefs = saver.prefs
  const vipActive = Boolean(me?.vip.active)
  const nickname = nicknameEdit ?? propNickname

  async function handleMatch() {
    await saver.flush()
    onMatch()
  }

  function updateProfile(patch: Partial<ProfileInfo>) {
    saver.update({ profile: patch })
  }

  function updatePrefs(patch: Partial<MatchPrefs>) {
    saver.update({ prefs: patch })
  }

  function toggleCollapsed() {
    const next = !collapsed
    setCollapsed(next)
    try {
      localStorage.setItem(COLLAPSE_KEY, next ? '1' : '0')
    } catch {
      // 忽略
    }
  }

  async function copyCode() {
    try {
      await navigator.clipboard.writeText(publicCode)
      setToast('已复制匿名编号，仅用于联系客服')
    } catch {
      setToast('复制失败，请手动复制')
    }
  }

  async function share() {
    const url = location.origin
    if (navigator.share) {
      try {
        await navigator.share({ url })
        return
      } catch {
        // 用户取消或失败 → 退化为复制
      }
    }
    try {
      await navigator.clipboard.writeText(url)
      setToast('站点链接已复制')
    } catch {
      setToast('复制失败，请手动复制')
    }
  }

  async function doShuffle() {
    try {
      const updated = await shuffleNickname()
      useIdentityStore.setState({ me: updated })
      setNicknameEdit(null)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function submitNickname() {
    try {
      const updated = await setNickname(nickname)
      useIdentityStore.setState({ me: updated })
      setNicknameEdit(null)
      setEditingNickname(false)
      setToast('昵称已更新')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  const ready = Boolean(me && profile && prefs)

  if (!ready || !me || !profile || !prefs) {
    // 资料尚未加载（或加载失败）时仍然渲染匹配按钮，保证主入口可用。
    return (
      <Card className="flex h-full flex-col gap-5 p-5">
        <div className="space-y-2">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-3 w-56" />
        </div>
        <div className="mt-auto flex flex-col items-center gap-3">
          <div
            aria-hidden
            className="flex h-40 w-40 items-center justify-center rounded-full"
            style={{
              background:
                'linear-gradient(135deg, color-mix(in srgb, var(--color-brand) 14%, transparent), color-mix(in srgb, var(--color-accent) 14%, transparent))',
            }}
          >
            <button
              type="button"
              data-testid="match-button"
              onClick={onMatch}
              className="h-[152px] w-[152px] rounded-full text-lg font-semibold text-white transition duration-150 ease-out hover:opacity-95"
              style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
            >
              匹配
            </button>
          </div>
          <p className="text-xs text-lobby-sub">点击开始匹配，遇见有趣的人</p>
        </div>
      </Card>
    )
  }

  const maskedCode = `${publicCode.slice(0, 2)}****${publicCode.slice(-4)}`

  return (
    <Card className="flex h-full flex-col gap-5 p-5">
      {/* 头像行：匿名编号、复制、分享、随机昵称、折叠 */}
      <div className="flex items-center gap-3">
        <GradientAvatar seed={me.avatarSeed || 'anonymous'} size={48} label={nickname} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm text-lobby-ink">{maskedCode || '—'}</span>
            <button
              type="button"
              aria-label="复制匿名编号"
              onClick={copyCode}
              className="rounded-sm px-1 text-xs text-lobby-sub transition duration-150 ease-out hover:text-brand"
            >
              ⧉
            </button>
            <button
              type="button"
              aria-label="分享本站"
              onClick={share}
              className="rounded-sm px-1 text-xs text-lobby-sub transition duration-150 ease-out hover:text-brand"
            >
              ⤴
            </button>
          </div>
          {vipActive && me.vip.until && (
            <span className="mt-1 inline-block rounded-full bg-brand/15 px-2 py-0.5 text-[11px] text-brand">
              VIP 至 {me.vip.until.slice(0, 10)}
            </span>
          )}
        </div>
        <ResetIdentityConfirm className="rounded-sm px-1 text-sm text-lobby-sub transition duration-150 ease-out hover:text-brand" label="🔄" ariaLabel="换个身份" />
        <button
          type="button"
          aria-label="折叠资料"
          onClick={toggleCollapsed}
          className="rounded-sm px-2 py-1 text-sm text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg"
        >
          {collapsed ? '▼' : '▲'}
        </button>
      </div>

      {!collapsed && (
        <>
          {/* 昵称 */}
          <div className="flex items-center gap-2">
            <span className="w-12 text-xs text-lobby-sub">昵称</span>
            {editingNickname ? (
              <>
                <input
                  aria-label="昵称"
                  className="flex-1 rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1 text-sm text-lobby-ink"
                  maxLength={12}
                  value={nickname}
                  onChange={(e) => setNicknameEdit(e.target.value)}
                />
                <button
                  type="button"
                  className="rounded-sm bg-brand px-2 py-1 text-xs text-white"
                  onClick={submitNickname}
                >
                  保存
                </button>
              </>
            ) : (
              <>
                <span className="flex-1 text-sm text-lobby-ink">{nickname}</span>
                <button
                  type="button"
                  aria-label="随机昵称"
                  onClick={doShuffle}
                  className="rounded-sm px-1 text-sm text-lobby-sub transition duration-150 ease-out hover:text-brand"
                >
                  🔀
                </button>
                <button
                  type="button"
                  aria-label="编辑昵称"
                  onClick={() => {
                    setNicknameEdit(propNickname)
                    setEditingNickname(true)
                  }}
                  className="rounded-sm px-1 text-sm text-lobby-sub transition duration-150 ease-out hover:text-brand"
                >
                  ✎
                </button>
              </>
            )}
          </div>

          {/* 性别 */}
          <div className="flex items-center gap-2">
            <span className="w-12 shrink-0 text-xs text-lobby-sub">性别</span>
            <Segmented<Gender>
              ariaLabel="性别"
              value={profile.gender}
              options={[
                { value: 'male', label: '男' },
                { value: 'female', label: '女' },
                { value: 'secret', label: '保密' },
              ]}
              onChange={(g) => updateProfile({ gender: g })}
            />
          </div>

          {/* 年龄 */}
          <div className="flex items-center gap-2">
            <span className="w-12 shrink-0 text-xs text-lobby-sub">年龄</span>
            <div className="flex-1">
              <RangeSlider
                ariaLabel="年龄"
                min={MIN_AGE}
                max={MAX_AGE}
                value={profile.age ?? null}
                onChange={(age) => updateProfile({ age })}
              />
            </div>
          </div>

          {/* 城市与公开 */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="w-12 shrink-0 text-xs text-lobby-sub">城市</span>
            <select
              aria-label="省份"
              className="rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1 text-sm text-lobby-ink"
              value={profile.province ?? ''}
              onChange={(e) => updateProfile({ province: e.target.value || null, provincePublic: false })}
            >
              <option value="">未设置</option>
              {PROVINCES.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
            <label className="flex items-center gap-1.5 text-xs text-lobby-sub">
              公开
              <Toggle
                label="公开省份"
                checked={profile.provincePublic}
                onChange={(v) => updateProfile({ provincePublic: v })}
                disabled={!profile.province}
              />
            </label>
            <label className="ml-auto flex items-center gap-1.5 text-xs text-lobby-sub">
              清流模式
              <Toggle
                label="清流模式"
                checked={profile.cleanMode}
                onChange={(v) => updateProfile({ cleanMode: v })}
              />
            </label>
          </div>

          {/* 标签 */}
          <div className="flex items-start gap-2">
            <span className="w-12 shrink-0 pt-1 text-xs text-lobby-sub">标签</span>
            <TagPicker selected={profile.tags} onChange={(tags) => updateProfile({ tags })} />
          </div>

          {/* 匹配偏好（VIP 权益） */}
          <VipLock active={!vipActive} onUnlock={() => nav('/vip')}>
            <div className="space-y-3 rounded-sm border border-lobby-line p-3">
              <div className="flex items-center gap-2">
                <span className="w-12 shrink-0 text-xs text-lobby-sub">匹配</span>
                <Segmented<'any' | 'male' | 'female'>
                  ariaLabel="匹配性别"
                  value={prefs.gender}
                  options={[
                    { value: 'male', label: '男' },
                    { value: 'female', label: '女' },
                    { value: 'any', label: '不限' },
                  ]}
                  onChange={(g) => updatePrefs({ gender: g })}
                  disabled={!vipActive}
                />
              </div>
              <div className="flex items-center gap-2">
                <span className="w-12 shrink-0 text-xs text-lobby-sub">同省</span>
                <span className="text-xs text-lobby-ink">同省匹配</span>
                <Toggle
                  label="同省匹配"
                  checked={prefs.sameProvince}
                  onChange={(v) => updatePrefs({ sameProvince: v })}
                  disabled={!vipActive}
                />
              </div>
            </div>
          </VipLock>
        </>
      )}

      {/* 匹配按钮 */}
      <div className="mt-auto flex flex-col items-center gap-3">
        <div
          aria-hidden
          className="flex h-40 w-40 items-center justify-center rounded-full"
          style={{
            background:
              'linear-gradient(135deg, color-mix(in srgb, var(--color-brand) 14%, transparent), color-mix(in srgb, var(--color-accent) 14%, transparent))',
          }}
        >
          <button
            type="button"
            data-testid="match-button"
            onClick={() => void handleMatch()}
            className="h-[152px] w-[152px] rounded-full text-lg font-semibold text-white transition duration-150 ease-out hover:opacity-95"
            style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
          >
            匹配
          </button>
        </div>
        <p className="text-xs text-lobby-sub">点击开始匹配，遇见有趣的人</p>
      </div>
    </Card>
  )
}
