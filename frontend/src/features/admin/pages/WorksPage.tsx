import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  adminApproveWork,
  adminRejectWork,
  adminRemoveWork,
  adminWorks,
} from '../../../lib/api/client'
import type { AdminWork } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
import { useAdmin } from '../context'
import { usePagedList } from '../hooks'
import { ModerationQueue } from './ModerationQueue'
import type { ModerationItem } from './ModerationQueue'

const STATUSES = ['pending', 'approved', 'rejected', 'removed'] as const
type WorkStatus = (typeof STATUSES)[number]

function parseStatus(raw: string | null): WorkStatus {
  return (STATUSES as readonly string[]).includes(raw ?? '') ? (raw as WorkStatus) : 'pending'
}

/** 作品审核：通过 / 驳回（逐条填原因）/ 下架。 */
export function WorksPage() {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [params, setParams] = useSearchParams()
  const status = parseStatus(params.get('status'))

  const fetchPage = useCallback(
    async (cursor?: string) => {
      const r = await adminWorks(status)
      // 该接口不分页，cursor 恒为 null。
      void cursor
      return { items: r.works, nextCursor: null }
    },
    [status],
  )

  const list = usePagedList<AdminWork, string>(status, fetchPage, onError)

  const items: ModerationItem[] = list.items.map((w) => ({
    id: w.id,
    status: w.status,
    body: `《${w.title}》`,
    meta: (
      <>
        {w.likeCount} 赞
        {w.rejectReason ? ` · 驳回原因：${w.rejectReason}` : ''}
      </>
    ),
    createdAt: w.createdAt,
  }))

  async function approve(id: string): Promise<boolean> {
    try {
      await adminApproveWork(id)
      setToast('作品已通过')
      list.setItems((prev) => prev.filter((w) => w.id !== id))
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  async function reject(id: string, reason: string): Promise<boolean> {
    try {
      await adminRejectWork(id, reason)
      setToast('作品已驳回')
      list.setItems((prev) => prev.filter((w) => w.id !== id))
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  async function remove(id: string): Promise<boolean> {
    try {
      await adminRemoveWork(id)
      setToast('作品已下架')
      list.setItems((prev) => prev.filter((w) => w.id !== id))
      return true
    } catch (e) {
      onError(e)
      return false
    }
  }

  return (
    <ModerationQueue
      title="作品审核"
      description="双方同意收录的共创作品需要审核，通过后进入书架被其他用户阅读。"
      testId="admin-work"
      emptyText="该状态下暂无作品"
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
      rejectDescription="驳回后该作品不会出现在书架，可填写原因告知作者。"
      removeDescription="下架后该作品会立即从书架移除。"
    />
  )
}
