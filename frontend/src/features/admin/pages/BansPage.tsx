import { useCallback, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { adminBans, adminRevokeBan } from '../../../lib/api/client'
import type { AdminBan } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
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
} from '../ui'

type BanFilter = 'active' | 'all'

/** 封禁管理：生效中/全部切换，撤销走确认对话框。 */
export function BansPage() {
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

  const list = usePagedList<AdminBan, string>(filter, fetchPage, onError)
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
    <div>
      <PageHeader
        title="封禁管理"
        description="按身份、IP 或实名账号查看封禁记录，可随时撤销。"
        actions={
          <FilterTabs<BanFilter>
            ariaLabel="封禁筛选"
            value={filter}
            onChange={(v) => setParams(v === 'active' ? {} : { active: 'all' }, { replace: true })}
            options={[
              { value: 'active', label: '生效中' },
              { value: 'all', label: '全部' },
            ]}
          />
        }
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
