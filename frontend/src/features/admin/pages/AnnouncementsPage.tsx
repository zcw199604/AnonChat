import { useCallback, useState } from 'react'
import {
  adminAnnouncements,
  adminCreateAnnouncement,
  adminDeleteAnnouncement,
} from '../../../lib/api/client'
import type { AdminAnnouncement } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
import { useAdmin } from '../context'
import { useAsyncData } from '../hooks'
import { formatTime } from '../labels'
import {
  Button,
  ConfirmDialog,
  EmptyState,
  LoadingRows,
  PageHeader,
  Panel,
  inputClass,
} from '../ui'

const MAX_LENGTH = 120

/** 公告：发布 + 列表 + 删除确认。 */
export function AnnouncementsPage() {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [content, setContent] = useState('')
  const [publishing, setPublishing] = useState(false)
  const [target, setTarget] = useState<AdminAnnouncement | null>(null)

  const load = useCallback(async () => (await adminAnnouncements()).announcements, [])
  const { data, loading, reload } = useAsyncData<AdminAnnouncement[]>('announcements', load, onError)
  const list = data ?? []

  async function publish() {
    const value = content.trim()
    if (value === '') return
    setPublishing(true)
    try {
      await adminCreateAnnouncement({ content: value })
      setContent('')
      setToast('公告已发布')
      reload()
    } catch (e) {
      onError(e)
    } finally {
      setPublishing(false)
    }
  }

  async function remove() {
    if (!target) return
    try {
      await adminDeleteAnnouncement(target.id)
      setToast('公告已删除')
      setTarget(null)
      reload()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div>
      <PageHeader title="公告" description="公告会在首页跑马灯滚动展示，最多 120 字。" />

      <Panel className="space-y-2 p-4">
        <h2 className="text-sm font-semibold text-lobby-ink">发布公告</h2>
        <textarea
          aria-label="公告内容"
          rows={3}
          maxLength={MAX_LENGTH}
          className={inputClass}
          placeholder="公告内容（1–120 字），会在首页跑马灯滚动"
          value={content}
          onChange={(e) => setContent(e.target.value)}
        />
        <div className="flex items-center justify-between">
          <span className="text-xs text-lobby-sub">
            {content.length}/{MAX_LENGTH}
          </span>
          <Button
            variant="primary"
            loading={publishing}
            disabled={content.trim() === ''}
            onClick={() => void publish()}
          >
            发布公告
          </Button>
        </div>
      </Panel>

      <h2 className="mt-6 mb-2 text-sm font-medium text-lobby-sub">已发布公告</h2>
      {loading ? (
        <LoadingRows rows={2} />
      ) : list.length === 0 ? (
        <EmptyState>暂无公告</EmptyState>
      ) : (
        <div className="space-y-2">
          {list.map((a) => (
            <Panel key={a.id} className="flex items-start justify-between gap-3 p-4">
              <div className="min-w-0">
                <p className="text-sm break-words text-lobby-ink">{a.content}</p>
                <p className="mt-1 text-xs text-lobby-sub">{formatTime(a.createdAt)}</p>
              </div>
              <Button variant="danger" size="sm" onClick={() => setTarget(a)}>
                删除
              </Button>
            </Panel>
          ))}
        </div>
      )}

      <ConfirmDialog
        open={target !== null}
        title="删除该公告？"
        description={target?.content}
        confirmText="删除公告"
        danger
        onConfirm={remove}
        onClose={() => setTarget(null)}
      />
    </div>
  )
}
