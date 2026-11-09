import { useCallback, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { adminBans, adminOnlineUsers, adminRevokeBan } from '../../../lib/api/client'
import type { AdminBan, AdminOnlineUser } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
import { BanDialog } from '../BanDialog'
import type { BanTarget } from '../BanDialog'
import { useAdmin } from '../context'
import { usePagedList } from '../hooks'
import { banSubject, formatTime } from '../labels'
import {
  Button,
  ConfirmDialog,
  EmptyState,
  FilterTabs,
  LoadMore,
  LoadingRows,
  PageHeader,
  Panel,
  StatusBadge,
  inputClass,
} from '../ui'

/** 顶部视图：在线用户 / 封禁记录。 */
type BanView = 'online' | 'records'
type BanFilter = 'active' | 'all'

/** 封禁弹窗的重挂载键：目标变化时重置表单初值。 */
function banTargetKey(target: BanTarget | null): string {
  if (!target) return 'none'
  return target.kind === 'ip' ? `ip:${target.ip ?? ''}` : `user:${target.identityId}`
}

/** 在线用户列表的自动刷新间隔。 */
const ONLINE_REFRESH_MS = 15_000
/** 搜索框输入防抖间隔。 */
const SEARCH_DEBOUNCE_MS = 300

/** 封禁管理：在线用户（可一键封禁）与封禁记录（可撤销）两个视图。 */
export function BansPage() {
  const [params, setParams] = useSearchParams()
  const view: BanView = params.get('view') === 'records' ? 'records' : 'online'

  const [target, setTarget] = useState<BanTarget | null>(null)
  /** 封禁成功后自增，触发当前视图的列表刷新。 */
  const [nonce, setNonce] = useState(0)

  return (
    <div>
      <PageHeader
        title="封禁管理"
        description="查看当前在线用户，按匿名身份、实名账号或 IP 一键封禁；也可查看封禁记录并随时撤销。"
        actions={
          <>
            {view === 'records' && (
              <Button variant="secondary" onClick={() => setTarget({ kind: 'ip' })}>
                手动封禁 IP
              </Button>
            )}
            <FilterTabs<BanView>
              ariaLabel="封禁视图"
              value={view}
              onChange={(v) => setParams(v === 'records' ? { view: 'records' } : {}, { replace: true })}
              options={[
                { value: 'online', label: '在线用户' },
                { value: 'records', label: '封禁记录' },
              ]}
            />
          </>
        }
      />

      {view === 'online' ? (
        <OnlineUsersView
          refreshKey={nonce}
          onBanUser={(u) => setTarget({ kind: 'user', ...u })}
          onBanIp={(ip) => setTarget({ kind: 'ip', ip })}
        />
      ) : (
        <BanRecordsView refreshKey={nonce} />
      )}

      <BanDialog
        key={banTargetKey(target)}
        open={target !== null}
        target={target}
        onClose={() => setTarget(null)}
        onDone={() => {
          setTarget(null)
          setNonce((n) => n + 1)
        }}
      />
    </div>
  )
}

/** 在线用户视图：搜索 + 刷新 + 15 秒自动刷新，行内可直接封禁用户或 IP。 */
function OnlineUsersView({
  refreshKey,
  onBanUser,
  onBanIp,
}: {
  refreshKey: number
  onBanUser: (u: AdminOnlineUser) => void
  onBanIp: (ip: string) => void
}) {
  const { onError } = useAdmin()
  const [input, setInput] = useState('')
  const [query, setQuery] = useState('')
  /** users 为 null 表示尚未加载完成（只在首屏显示骨架）。 */
  const [users, setUsers] = useState<AdminOnlineUser[] | null>(null)
  const [total, setTotal] = useState(0)
  const [tick, setTick] = useState(0)

  // 输入防抖：停止输入一段时间后再查询。
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(input.trim()), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [input])

  // 拉取在线列表：首屏、搜索词变化、手动刷新、封禁成功后都会重新加载。
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const r = await adminOnlineUsers(query || undefined)
        if (cancelled) return
        setUsers(r.users)
        setTotal(r.total)
      } catch (e) {
        if (!cancelled) onError(e)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [query, tick, refreshKey, onError])

  // 每 15 秒自动刷新一次（卸载时清理定时器）。
  useEffect(() => {
    const timer = window.setInterval(() => setTick((n) => n + 1), ONLINE_REFRESH_MS)
    return () => window.clearInterval(timer)
  }, [])

  return (
    <div className="space-y-3">
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(input.trim())
        }}
      >
        <input
          aria-label="搜索在线用户"
          className={`${inputClass} max-w-xs`}
          placeholder="搜索昵称 / 编号 / IP"
          value={input}
          onChange={(e) => setInput(e.target.value)}
        />
        <Button type="submit" variant="secondary">
          搜索
        </Button>
        <Button variant="ghost" onClick={() => setTick((n) => n + 1)}>
          刷新
        </Button>
        <span className="text-xs text-lobby-sub">当前在线 {total} 人</span>
      </form>

      {users === null ? (
        <LoadingRows rows={4} />
      ) : users.length === 0 ? (
        <EmptyState>当前没有在线用户</EmptyState>
      ) : (
        <Panel className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] text-left text-sm">
              <thead className="border-b border-lobby-line text-xs text-lobby-sub">
                <tr>
                  <th className="px-4 py-2 font-medium">用户</th>
                  <th className="px-4 py-2 font-medium">登录 IP</th>
                  <th className="px-4 py-2 font-medium">连接数</th>
                  <th className="px-4 py-2 font-medium">上线时间</th>
                  <th className="px-4 py-2 font-medium">UA</th>
                  <th className="px-4 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.identityId} className="border-b border-lobby-line/60 last:border-0">
                    <td className="px-4 py-2.5">
                      <div className="text-lobby-ink">{u.nickname || '未命名'}</div>
                      <div className="font-mono text-xs text-lobby-sub">{u.publicCode}</div>
                    </td>
                    <td className="px-4 py-2.5">
                      {u.ips.length === 0 ? (
                        <span className="text-xs text-lobby-sub">—</span>
                      ) : (
                        <div className="space-y-1">
                          {u.ips.map((ip) => (
                            <div key={ip} className="flex items-center gap-2">
                              <span className="font-mono text-xs text-lobby-ink select-all">{ip}</span>
                              <Button size="sm" variant="ghost" onClick={() => onBanIp(ip)}>
                                封禁 IP
                              </Button>
                            </div>
                          ))}
                        </div>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-lobby-sub">{u.connCount}</td>
                    <td className="px-4 py-2.5 text-xs text-lobby-sub">
                      {formatTime(u.connectedAt)}
                    </td>
                    <td
                      className="max-w-[180px] truncate px-4 py-2.5 text-xs text-lobby-sub"
                      title={u.userAgent}
                    >
                      {u.userAgent || '—'}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <Button variant="danger" size="sm" onClick={() => onBanUser(u)}>
                        封禁用户
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>
      )}
    </div>
  )
}

/** 封禁记录视图：生效中/全部切换，撤销走确认对话框。 */
function BanRecordsView({ refreshKey }: { refreshKey: number }) {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [params, setParams] = useSearchParams()
  const filter: BanFilter = params.get('active') === 'all' ? 'all' : 'active'

  const fetchPage = useCallback(
    async (cursor?: string) => {
      const r = await adminBans({
        active: filter === 'active' ? true : undefined,
        cursor,
        limit: 20,
      })
      return { items: r.bans, nextCursor: r.nextCursor ?? null }
    },
    [filter],
  )

  const list = usePagedList<AdminBan, string>(`${filter}:${refreshKey}`, fetchPage, onError)
  const [target, setTarget] = useState<AdminBan | null>(null)

  async function revoke() {
    if (!target) return
    try {
      await adminRevokeBan(target.id)
      setToast('已撤销封禁')
      list.setItems((prev) => prev.filter((b) => b.id !== target.id))
      setTarget(null)
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3">
      <FilterTabs<BanFilter>
        ariaLabel="封禁筛选"
        value={filter}
        onChange={(v) => setParams(v === 'active' ? { view: 'records' } : { view: 'records', active: 'all' }, { replace: true })}
        options={[
          { value: 'active', label: '生效中' },
          { value: 'all', label: '全部' },
        ]}
      />

      {list.loading ? (
        <LoadingRows rows={4} />
      ) : list.items.length === 0 ? (
        <EmptyState>{filter === 'active' ? '当前没有生效中的封禁' : '暂无封禁记录'}</EmptyState>
      ) : (
        <Panel className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-left text-sm">
              <thead className="border-b border-lobby-line text-xs text-lobby-sub">
                <tr>
                  <th className="px-4 py-2 font-medium">主体类型</th>
                  <th className="px-4 py-2 font-medium">主体值</th>
                  <th className="px-4 py-2 font-medium">原因</th>
                  <th className="px-4 py-2 font-medium">状态</th>
                  <th className="px-4 py-2 font-medium">到期时间</th>
                  <th className="px-4 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((b) => (
                  <tr key={b.id} className="border-b border-lobby-line/60 last:border-0">
                    <td className="px-4 py-2.5 text-lobby-ink">{banSubject(b.subjectType)}</td>
                    <td className="px-4 py-2.5">
                      <span className="font-mono text-xs text-lobby-ink select-all">{b.subjectValue}</span>
                    </td>
                    <td className="max-w-[220px] truncate px-4 py-2.5 text-lobby-sub" title={b.reason}>
                      {b.reason}
                    </td>
                    <td className="px-4 py-2.5">
                      <StatusBadge tone={b.active ? 'danger' : 'neutral'}>
                        {b.active ? '生效中' : '已失效'}
                      </StatusBadge>
                    </td>
                    <td className="px-4 py-2.5 text-xs text-lobby-sub">
                      {b.expiresAt ? formatTime(b.expiresAt) : '永久'}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      {b.active && (
                        <Button variant="danger" size="sm" onClick={() => setTarget(b)}>
                          撤销
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>
      )}

      <LoadMore hasMore={list.hasMore} loading={list.loadingMore} onClick={list.loadMore} />

      <ConfirmDialog
        open={target !== null}
        title="撤销该封禁？"
        description={
          target ? (
            <span className="font-mono text-xs">
              {banSubject(target.subjectType)} · {target.subjectValue}
            </span>
          ) : undefined
        }
        confirmText="撤销封禁"
        danger
        onConfirm={revoke}
        onClose={() => setTarget(null)}
      />
    </div>
  )
}
