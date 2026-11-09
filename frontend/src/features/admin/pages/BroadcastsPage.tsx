import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  adminApproveBroadcast,
  adminBroadcasts,
  adminRejectBroadcast,
  adminRemoveBroadcast,
} from '../../../lib/api/client'
import type { AdminBroadcast } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
import { useAdmin } from '../context'
import { usePagedList } from '../hooks'
import { ModerationQueue } from './ModerationQueue'
import type { ModerationItem } from './ModerationQueue'

const STATUSES = ['pending', 'approved', 'rejected', 'removed'] as const
type BroadcastStatus = (typeof STATUSES)[number]

function parseStatus(raw: string | null): BroadcastStatus {
  return (STATUSES as readonly string[]).includes(raw ?? '')
    ? (raw as BroadcastStatus)
    : 'pending'
}

/** 广播审核：通过 / 驳回（逐条填原因）/ 下架。 */
export function BroadcastsPage() {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [params, setParams] = useSearchParams()
  const status = parseStatus(params.get('status'))

  const fetchPage = useCallback(
    async (cursor?: string) => {
      const r = await adminBroadcasts(status)
      // 该接口不分页，cursor 恒为 null。
      void cursor
      return { items: r.broadcasts, nextCursor: null }
    },
    [status],
  )

  const list = usePagedList<AdminBroadcast, string>(status, fetchPage, onError)

  const items: ModerationItem[] = list.items.map((b) => ({
    id: b.id,
    status: b.status,
    body: b.content,
    meta: (
      <>
        提交者编号 <span className="font-mono">{b.publicCode}</span>
      </>
    ),
    createdAt: b.createdAt,
  }))

  async function approve(id: string): Promise<boolean> {
    try {
      await adminApproveBroadcast(id)
      setToast('已通过，展示 24 小时')
      list.setItems((prev) => prev.filter((b) => b.id !== id))
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  async function reject(id: string, reason: string): Promise<boolean> {
    try {
      await adminRejectBroadcast(id, reason)
      setToast('已驳回并退还广播卡')
      list.setItems((prev) => prev.filter((b) => b.id !== id))
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  async function remove(id: string): Promise<boolean> {
    try {
      await adminRemoveBroadcast(id)
      setToast('已下架')
      list.setItems((prev) => prev.filter((b) => b.id !== id))
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  return (
    <ModerationQueue
      title="广播审核"
      description="审核用户提交的广播内容，通过后在首页展示 24 小时；驳回会退还用户的广播卡。"
      testId="admin-broadcast"
      emptyText="该状态下暂无广播"
      items={items}
      loading={list.loading}
      hasMore={list.hasMore}
      loadingMore={list.loadingMore}
      onLoadMore={list.loadMore}
      status={status}
      onStatusChange={(next) =>
        setParams(next === 'pending' ? {} : { status: next }, { replace: true })
      }
      onApprove={approve}
      onReject={reject}
      onRemove={remove}
      rejectDescription="驳回后会退还用户消耗的广播卡，可填写原因告知用户。"
      removeDescription="下架后该广播会立即从首页消失。"
    />
  )
}
