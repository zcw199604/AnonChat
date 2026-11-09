import { useCallback, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { adminReport, adminResolveReport } from '../../../lib/api/client'
import type { AdminReportDetail } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
import { banSubjects, validateBanForm } from '../banForm'
import type { BanFormState } from '../banForm'
import { useAdmin } from '../context'
import { useAsyncData } from '../hooks'
import { formatTime, reportReason, reportStatus } from '../labels'
import {
  Button,
  ConfirmDialog,
  EmptyState,
  FilterTabs,
  LoadingRows,
  PageHeader,
  Panel,
  StatusBadge,
  inputClass,
} from '../ui'

interface SnapshotItem {
  id: number
  memberId: number
  nickname: string
  body: string
  createdAt?: string
  burnedAt?: string
  reported?: boolean
}

const DURATIONS = [
  { value: 24, label: '1 天' },
  { value: 168, label: '7 天' },
  { value: 720, label: '30 天' },
  { value: 0, label: '永久' },
]

/** 举报详情：左侧证据，右侧处理表单；返回列表时保留筛选参数。 */
export function ReportDetailPage() {
  const { id = '' } = useParams()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)

  const status = params.get('status') === 'resolved' ? 'resolved' : 'open'
  const backTo = status === 'resolved' ? '/admin/reports?status=resolved' : '/admin/reports'

  const load = useCallback(() => adminReport(id), [id])
  const { data: detail, loading } = useAsyncData<AdminReportDetail>(`report:${id}`, load, onError)

  const [form, setForm] = useState<BanFormState>({
    identity: true,
    ip: false,
    account: false,
    durationHours: 24,
    reason: '',
  })
  const [errors, setErrors] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [dismissOpen, setDismissOpen] = useState(false)

  async function ban() {
    const result = validateBanForm(form)
    setErrors(result.errors)
    if (!result.ok) return
    setBusy(true)
    try {
      await adminResolveReport(id, {
        action: 'ban',
        ban: {
          subjects: banSubjects(form),
          durationHours: form.durationHours === 0 ? null : form.durationHours,
          reason: form.reason,
        },
      })
      setToast('已封禁并处理举报')
      navigate(backTo)
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  async function dismiss() {
    try {
      await adminResolveReport(id, { action: 'dismiss' })
      setToast('已驳回举报')
      navigate(backTo)
    } catch (e) {
      onError(e)
    }
  }

  if (loading) {
    return (
      <div>
        <PageHeader title="举报详情" />
        <LoadingRows rows={5} />
      </div>
    )
  }

  if (!detail) {
    return (
      <div>
        <PageHeader title="举报详情" />
        <EmptyState>举报不存在或已被处理</EmptyState>
      </div>
    )
  }

  const snapshot: SnapshotItem[] = Array.isArray(detail.snapshot)
    ? (detail.snapshot as SnapshotItem[])
    : []
  const reason = reportReason(detail.reason)
  const st = reportStatus(detail.status)

  return (
    <div>
      <PageHeader
        title="举报详情"
        description="核对证据快照后，选择封禁被举报者或驳回举报。"
        actions={
          <Button variant="secondary" onClick={() => navigate(backTo)}>
            ← 返回列表
          </Button>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="space-y-4">
          <Panel className="p-4">
            <div className="mb-3 flex items-center gap-2">
              <h2 className="text-sm font-semibold text-lobby-ink">举报信息</h2>
              <StatusBadge tone={reason.tone}>{reason.label}</StatusBadge>
              <span className="font-mono text-xs text-lobby-sub">{detail.reason}</span>
              <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
            </div>
            <dl className="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-2">
              <div>
                <dt className="text-xs text-lobby-sub">昵称</dt>
                <dd className="text-lobby-ink">{detail.reported.nickname || '—'}</dd>
              </div>
              <div>
                <dt className="text-xs text-lobby-sub">历史被举报次数</dt>
                <dd className="text-lobby-ink">{detail.reported.reportCount} 次</dd>
              </div>
              <div>
                <dt className="text-xs text-lobby-sub">创建 IP</dt>
                <dd className="font-mono text-lobby-ink">{detail.reported.createdIp || '—'}</dd>
              </div>
              <div>
                <dt className="text-xs text-lobby-sub">最近连接 IP</dt>
                <dd className="font-mono text-lobby-ink">{detail.reported.lastIp || '—'}</dd>
              </div>
              <div>
                <dt className="text-xs text-lobby-sub">实名号码</dt>
                <dd className="font-mono text-lobby-ink">{detail.reported.phoneMasked || '未实名'}</dd>
              </div>
              <div>
                <dt className="text-xs text-lobby-sub">举报时间</dt>
                <dd className="text-lobby-ink">{formatTime(detail.createdAt)}</dd>
              </div>
            </dl>
            {detail.note && (
              <p className="mt-3 rounded-sm bg-lobby-bg p-2 text-xs text-lobby-sub">
                举报备注：{detail.note}
              </p>
            )}
          </Panel>

          <Panel className="p-4">
            <h2 className="mb-3 text-sm font-semibold text-lobby-ink">证据快照</h2>
            {snapshot.length === 0 ? (
              <p className="text-sm text-lobby-sub">没有可展示的消息</p>
            ) : (
              <div className="space-y-1.5">
                {snapshot.map((s) => (
                  <div
                    key={s.id}
                    className={`rounded-sm px-2.5 py-1.5 text-sm whitespace-pre-wrap break-words ${
                      s.reported
                        ? 'border border-danger/40 bg-danger/10 text-lobby-ink'
                        : 'bg-lobby-bg/60 text-lobby-ink'
                    }`}
                  >
                    <span className="mr-2 text-xs text-lobby-sub">{s.nickname}</span>
                    {s.body}
                  </div>
                ))}
              </div>
            )}
          </Panel>
        </div>

        <div>
          {detail.status === 'open' ? (
            <Panel className="space-y-3 p-4">
              <h2 className="text-sm font-semibold text-lobby-ink">处理</h2>

              <div className="space-y-1">
                <span className="block text-xs font-medium text-lobby-sub">封禁对象</span>
                <div className="flex flex-wrap gap-3 text-sm text-lobby-ink">
                  <label className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={form.identity}
                      onChange={(e) => setForm({ ...form, identity: e.target.checked })}
                    />
                    匿名身份
                  </label>
                  <label className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={form.ip}
                      onChange={(e) => setForm({ ...form, ip: e.target.checked })}
                    />
                    IP
                  </label>
                  <label className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={form.account}
                      onChange={(e) => setForm({ ...form, account: e.target.checked })}
                    />
                    实名账号
                  </label>
                </div>
              </div>

              <div className="space-y-1">
                <span className="block text-xs font-medium text-lobby-sub">封禁时长</span>
                <FilterTabs<string>
                  ariaLabel="封禁时长"
                  value={String(form.durationHours ?? 0)}
                  onChange={(v) => setForm({ ...form, durationHours: Number(v) })}
                  options={DURATIONS.map((d) => ({ value: String(d.value), label: d.label }))}
                />
              </div>

              <label className="block space-y-1">
                <span className="block text-xs font-medium text-lobby-sub">封禁原因</span>
                <textarea
                  aria-label="封禁原因"
                  rows={3}
                  className={inputClass}
                  placeholder="必填，会写入审计日志"
                  value={form.reason}
                  onChange={(e) => setForm({ ...form, reason: e.target.value })}
                />
              </label>

              {errors.length > 0 && (
                <div className="text-xs text-danger">{errors.join('；')}</div>
              )}

              <div className="flex flex-col gap-2">
                <Button variant="danger" loading={busy} onClick={() => void ban()}>
                  封禁并处理
                </Button>
                <Button variant="secondary" onClick={() => setDismissOpen(true)}>
                  驳回举报
                </Button>
              </div>
            </Panel>
          ) : (
            <Panel className="p-4 text-sm text-lobby-sub">该举报已处理，无需再次操作。</Panel>
          )}

          {detail.activeBans.length > 0 && (
            <Panel className="mt-4 p-4">
              <h2 className="mb-2 text-sm font-semibold text-lobby-ink">生效中的封禁</h2>
              <ul className="space-y-1 text-xs text-lobby-sub">
                {detail.activeBans.map((b) => (
                  <li key={b.id} className="font-mono">
                    {b.subjectType} · {b.subjectValue}
                  </li>
                ))}
              </ul>
            </Panel>
          )}

        </div>
      </div>

      <ConfirmDialog
        open={dismissOpen}
        title="驳回该举报？"
        description="举报将被标记为已处理，不会对被举报者做任何处置。"
        confirmText="驳回举报"
        onConfirm={dismiss}
        onClose={() => setDismissOpen(false)}
      />
    </div>
  )
}
