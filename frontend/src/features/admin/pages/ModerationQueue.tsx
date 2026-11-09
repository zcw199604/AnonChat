import { useState } from 'react'
import type { ReactNode } from 'react'
import {
  EmptyState,
  FilterTabs,
  LoadingRows,
  PageHeader,
  Panel,
  ReasonDialog,
  StatusBadge,
  ConfirmDialog,
  Button,
  LoadMore,
} from '../ui'
import { contentStatus, formatTime } from '../labels'

export interface ModerationItem {
  id: string
  status: string
  body: ReactNode
  meta: ReactNode
  createdAt: string
}

interface ModerationQueueProps {
  title: string
  description: string
  testId: string
  emptyText: string
  items: ModerationItem[]
  loading: boolean
  hasMore: boolean
  loadingMore: boolean
  onLoadMore: () => void
  status: string
  onStatusChange: (status: string) => void
  /** 返回是否成功；失败时对话框保持打开，便于重试。 */
  onApprove: (id: string) => Promise<boolean>
  onReject: (id: string, reason: string) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
  rejectDescription: ReactNode
  removeDescription: ReactNode
}

const STATUS_OPTIONS = [
  { value: 'pending', label: '待审核' },
  { value: 'approved', label: '已通过' },
  { value: 'rejected', label: '已驳回' },
  { value: 'removed', label: '已下架' },
]

/**
 * 广播审核与作品审核共用的队列视图：状态筛选 + 卡片列表 +
 * 每条独立的驳回原因对话框（不再共用顶部输入框）。
 */
export function ModerationQueue({
  title,
  description,
  testId,
  emptyText,
  items,
  loading,
  hasMore,
  loadingMore,
  onLoadMore,
  status,
  onStatusChange,
  onApprove,
  onReject,
  onRemove,
  rejectDescription,
  removeDescription,
}: ModerationQueueProps) {
  /** 正在操作的具体条目 id，用于按钮 loading。 */
  const [busyId, setBusyId] = useState<string | null>(null)
  const [rejectTarget, setRejectTarget] = useState<string | null>(null)
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)

  async function run(id: string, action: () => Promise<boolean>): Promise<boolean> {
    setBusyId(id)
    try {
      return await action()
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div>
      <PageHeader
        title={title}
        description={description}
        actions={
          <FilterTabs
            ariaLabel={`${title}状态`}
            value={status}
            onChange={onStatusChange}
            options={STATUS_OPTIONS}
          />
        }
      />

      {loading ? (
        <LoadingRows rows={3} />
      ) : items.length === 0 ? (
        <EmptyState>{emptyText}</EmptyState>
      ) : (
        <div className="space-y-3">
          {items.map((item) => {
            const st = contentStatus(item.status)
            const busy = busyId === item.id
            return (
              <Panel key={item.id} data-testid={testId} className="p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <div className="text-sm break-words whitespace-pre-wrap text-lobby-ink">
                      {item.body}
                    </div>
                    <div className="flex flex-wrap items-center gap-2 text-xs text-lobby-sub">
                      <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                      <span>{item.meta}</span>
                      <span>{formatTime(item.createdAt)}</span>
                    </div>
                  </div>
                  <div className="flex shrink-0 gap-2">
                    {item.status === 'pending' && (
                      <>
                        <Button
                          variant="primary"
                          size="sm"
                          loading={busy}
                          onClick={() => void run(item.id, () => onApprove(item.id))}
                        >
                          通过
                        </Button>
                        <Button variant="danger" size="sm" disabled={busy} onClick={() => setRejectTarget(item.id)}>
                          驳回
                        </Button>
                      </>
                    )}
                    {item.status === 'approved' && (
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => setRemoveTarget(item.id)}>
                        下架
                      </Button>
                    )}
                  </div>
                </div>
              </Panel>
            )
          })}
        </div>
      )}

      <LoadMore hasMore={hasMore} loading={loadingMore} onClick={onLoadMore} />

      <ReasonDialog
        open={rejectTarget !== null}
        title="驳回该内容？"
        description={rejectDescription}
        confirmText="驳回"
        onConfirm={async (reason) => {
          const id = rejectTarget
          if (!id) return
          if (await run(id, () => onReject(id, reason))) setRejectTarget(null)
          else throw new Error('reject failed')
        }}
        onClose={() => setRejectTarget(null)}
      />

      <ConfirmDialog
        open={removeTarget !== null}
        title="下架该内容？"
        description={removeDescription}
        confirmText="下架"
        danger
        onConfirm={async () => {
          const id = removeTarget
          if (!id) return
          if (await run(id, () => onRemove(id))) setRemoveTarget(null)
        }}
        onClose={() => setRemoveTarget(null)}
      />
    </div>
  )
}
