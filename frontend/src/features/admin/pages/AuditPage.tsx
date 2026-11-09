import { useCallback } from 'react'
import { adminAuditLogs } from '../../../lib/api/client'
import type { AdminAuditLog } from '../../../lib/api/client'
import { useAdmin } from '../context'
import { usePagedList } from '../hooks'
import { auditAction, formatTime } from '../labels'
import {
  EmptyState,
  LoadMore,
  LoadingRows,
  PageHeader,
  Panel,
} from '../ui'

/** 审计日志：中文动作 + 原始 action，对象过长截断并保留 title。 */
export function AuditPage() {
  const { onError } = useAdmin()

  const fetchPage = useCallback(async (cursor?: number) => {
    // 接口的 cursor 是字符串，但真实语义是日志 id 的游标。
    const r = await adminAuditLogs({ cursor: cursor == null ? undefined : String(cursor), limit: 20 })
    return { items: r.logs, nextCursor: r.nextCursor ?? null }
  }, [])

  const list = usePagedList<AdminAuditLog, number>('audit', fetchPage, onError)

  return (
    <div>
      <PageHeader title="审计日志" description="记录管理员的关键操作，便于追溯。" />

      {list.loading ? (
        <LoadingRows rows={6} />
      ) : list.items.length === 0 ? (
        <EmptyState>暂无审计日志</EmptyState>
      ) : (
        <Panel className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-left text-sm">
              <thead className="border-b border-lobby-line text-xs text-lobby-sub">
                <tr>
                  <th className="px-4 py-2 font-medium">时间</th>
                  <th className="px-4 py-2 font-medium">动作</th>
                  <th className="px-4 py-2 font-medium">对象</th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((log) => (
                  <tr key={log.id} className="border-b border-lobby-line/60 last:border-0">
                    <td className="px-4 py-2.5 text-xs whitespace-nowrap text-lobby-sub">
                      {formatTime(log.createdAt)}
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="text-lobby-ink">{auditAction(log.action)}</div>
                      <div className="font-mono text-[11px] text-lobby-sub">{log.action}</div>
                    </td>
                    <td className="max-w-[280px] px-4 py-2.5">
                      <div className="truncate font-mono text-xs text-lobby-sub" title={log.target ?? ''}>
                        {log.target || '—'}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>
      )}

      <LoadMore hasMore={list.hasMore} loading={list.loadingMore} onClick={list.loadMore} />
    </div>
  )
}
