import { useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { adminReports } from '../../../lib/api/client'
import type { AdminReportSummary } from '../../../lib/api/client'
import { useAdmin } from '../context'
import { usePagedList } from '../hooks'
import { formatTime, reportReason, reportStatus } from '../labels'
import {
  EmptyState,
  FilterTabs,
  LoadMore,
  LoadingRows,
  PageHeader,
  Panel,
  StatusBadge,
} from '../ui'

type ReportFilter = 'open' | 'resolved'

/** 举报列表：URL 保存状态筛选，整行可点击进入详情。 */
export function ReportsPage() {
  const { onError } = useAdmin()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const status: ReportFilter = params.get('status') === 'resolved' ? 'resolved' : 'open'

  const fetchPage = useCallback(
    async (cursor?: string) => {
      const r = await adminReports({ status, cursor, limit: 20 })
      return { items: r.reports, nextCursor: r.nextCursor ?? null }
    },
    [status],
  )

  const list = usePagedList<AdminReportSummary, string>(status, fetchPage, onError)

  function changeStatus(next: ReportFilter) {
    setParams(next === 'open' ? {} : { status: next }, { replace: true })
  }

  return (
    <div>
      <PageHeader
        title="举报处理"
        description="查看用户举报的证据快照，并对被举报者执行封禁或驳回。"
        actions={
          <FilterTabs<ReportFilter>
            ariaLabel="举报状态"
            value={status}
            onChange={changeStatus}
            options={[
              { value: 'open', label: '待处理' },
              { value: 'resolved', label: '已处理' },
            ]}
          />
        }
      />

      {list.loading ? (
        <LoadingRows rows={4} />
      ) : list.items.length === 0 ? (
        <EmptyState>{status === 'open' ? '暂无待处理举报' : '暂无已处理举报'}</EmptyState>
      ) : (
        <Panel className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-left text-sm">
              <thead className="border-b border-lobby-line text-xs text-lobby-sub">
                <tr>
                  <th className="px-4 py-2 font-medium">原因</th>
                  <th className="px-4 py-2 font-medium">状态</th>
                  <th className="px-4 py-2 font-medium">时间</th>
                  <th className="px-4 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((r) => {
                  const reason = reportReason(r.reason)
                  const st = reportStatus(r.status)
                  const go = () =>
                    navigate(
                      `/admin/reports/${r.id}${status === 'resolved' ? '?status=resolved' : ''}`,
                    )
                  return (
                    <tr
                      key={r.id}
                      onClick={go}
                      className="cursor-pointer border-b border-lobby-line/60 last:border-0 hover:bg-lobby-bg"
                    >
                      <td className="px-4 py-2.5">
                        <span className="inline-flex items-center gap-2">
                          <StatusBadge tone={reason.tone}>{reason.label}</StatusBadge>
                          <span className="font-mono text-xs text-lobby-sub">{r.reason}</span>
                        </span>
                      </td>
                      <td className="px-4 py-2.5">
                        <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-lobby-sub">{formatTime(r.createdAt)}</td>
                      <td className="px-4 py-2.5 text-right">
                        <button
                          type="button"
                          className="text-xs font-medium text-brand"
                          onClick={(e) => {
                            e.stopPropagation()
                            go()
                          }}
                        >
                          查看
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </Panel>
      )}

      <LoadMore hasMore={list.hasMore} loading={list.loadingMore} onClick={list.loadMore} />
    </div>
  )
}
