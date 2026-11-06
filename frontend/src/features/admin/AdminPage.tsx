import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api/client'
import {
  adminLogin,
  adminLogout,
  adminStats,
  adminReports,
  adminReport,
  adminResolveReport,
  adminBans,
  adminRevokeBan,
  adminAuditLogs,
  adminAnnouncements,
  adminCreateAnnouncement,
  adminDeleteAnnouncement,
  adminBroadcasts,
  adminApproveBroadcast,
  adminRejectBroadcast,
  adminRemoveBroadcast,
  adminActivationBatches,
  adminCreateActivationBatch,
  adminRevokeActivationBatch,
  adminWorks,
  adminApproveWork,
  adminRejectWork,
  adminRemoveWork,
  adminLookupIdentity,
} from '../../lib/api/client'
import type {
  AdminAnnouncement,
  AdminBroadcast,
  ActivationBatch,
  AdminWork,
  AdminIdentityLookup,
} from '../../lib/api/client'
import type { AdminStats, AdminReportSummary, AdminReportDetail, AdminBan, AdminAuditLog } from '../../lib/api/client'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { validateBanForm, banSubjects } from './banForm'
import { BotPanel } from './BotPanel'

type Tab =
  | 'overview'
  | 'reports'
  | 'bans'
  | 'audit'
  | 'announcements'
  | 'broadcasts'
  | 'codes'
  | 'works'
  | 'bot'
  | 'lookup'

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

export default function AdminPage() {
  const setToast = useUiStore((s) => s.setToast)
  const [authed, setAuthed] = useState<boolean | null>(null)
  const [tab, setTab] = useState<Tab>('overview')

  const handleErr = useCallback(
    (e: unknown) => {
      if (e instanceof ApiError && e.status === 401) {
        setAuthed(false)
        return
      }
      setToast(errorMessage((e as { code?: string }).code))
    },
    [setToast],
  )

  const checkAuth = useCallback(async () => {
    try {
      await adminStats()
      setAuthed(true)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setAuthed(false)
      else setAuthed(false)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时探测登录状态
    void checkAuth()
  }, [checkAuth])

  if (authed === null) {
    return <main className="p-8 text-lobby-sub">加载中…</main>
  }

  if (!authed) {
    return (
      <LoginForm
        onSuccess={() => {
          setAuthed(true)
        }}
        onError={handleErr}
      />
    )
  }

  return (
    <main className="mx-auto max-w-5xl px-4 py-6">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">管理后台</h1>
        <button
          type="button"
          className="text-sm px-3 py-1 rounded-sm bg-lobby-bg"
          onClick={() => {
            void adminLogout().finally(() => setAuthed(false))
          }}
        >
          退出登录
        </button>
      </div>
      <div className="mb-4 flex flex-wrap gap-2 text-sm">
        {([
          ['overview', '概览'],
          ['reports', '举报'],
          ['bans', '封禁'],
          ['announcements', '公告'],
          ['broadcasts', '广播审核'],
          ['codes', '激活码'],
          ['works', '作品审核'],
          ['bot', 'Bot'],
          ['lookup', '按编号查身份'],
          ['audit', '审计'],
        ] as Array<[Tab, string]>).map(([k, label]) => (
          <button
            key={k}
            type="button"
            className={`px-3 py-1 rounded-sm ${tab === k ? 'bg-brand text-white' : 'bg-lobby-bg'}`}
            onClick={() => setTab(k)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'overview' && <Overview onError={handleErr} />}
      {tab === 'reports' && <ReportsView onError={handleErr} />}
      {tab === 'bans' && <BansView onError={handleErr} />}
      {tab === 'audit' && <AuditView onError={handleErr} />}
      {tab === 'announcements' && <AnnouncementsView onError={handleErr} />}
      {tab === 'broadcasts' && <BroadcastsView onError={handleErr} />}
      {tab === 'codes' && <CodesView onError={handleErr} />}
      {tab === 'works' && <WorksView onError={handleErr} />}
      {tab === 'bot' && <BotPanel onError={handleErr} />}
      {tab === 'lookup' && <LookupView onError={handleErr} />}
    </main>
  )
}

function LoginForm({ onSuccess, onError }: { onSuccess: () => void; onError: (e: unknown) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true)
    try {
      await adminLogin(username, password)
      onSuccess()
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="mx-auto max-w-sm px-4 py-16 space-y-3">
      <h1 className="text-2xl font-bold">管理员登录</h1>
      <input aria-label="用户名" className="w-full bg-lobby-bg rounded-sm px-3 py-2" placeholder="用户名" value={username} onChange={(e) => setUsername(e.target.value)} />
      <input aria-label="密码" type="password" className="w-full bg-lobby-bg rounded-sm px-3 py-2" placeholder="密码" value={password} onChange={(e) => setPassword(e.target.value)} />
      <button type="button" className="px-4 py-2 rounded-sm bg-brand text-white disabled:opacity-40" disabled={busy} onClick={submit}>
        登录
      </button>
    </main>
  )
}

function Overview({ onError }: { onError: (e: unknown) => void }) {
  const [stats, setStats] = useState<AdminStats | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = () =>
      adminStats()
        .then((s) => {
          if (!cancelled) setStats(s)
        })
        .catch((e) => onError(e))
    load()
    const t = setInterval(load, 30000)
    return () => {
      cancelled = true
      clearInterval(t)
    }
  }, [onError])

  if (!stats) return <p className="text-lobby-sub">加载中…</p>
  const cards = [
    { label: '在线人数', value: stats.online },
    { label: '进行中会话', value: stats.activeConversations },
    { label: '24 小时消息', value: stats.messages24h },
    { label: '待处理举报', value: stats.openReports },
  ]
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {cards.map((c) => (
        <div key={c.label} className="rounded-card bg-lobby-card border border-lobby-line p-4">
          <div className="text-3xl font-bold">{c.value}</div>
          <div className="text-sm text-lobby-sub">{c.label}</div>
        </div>
      ))}
    </div>
  )
}

function ReportsView({ onError }: { onError: (e: unknown) => void }) {
  const [status, setStatus] = useState('open')
  const [reports, setReports] = useState<AdminReportSummary[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [detailID, setDetailID] = useState<string | null>(null)

  const load = async (reset: boolean) => {
    try {
      const r = await adminReports({ status, cursor: reset ? undefined : cursor ?? undefined, limit: 20 })
      setReports(reset ? r.reports : [...reports, ...r.reports])
      setCursor(r.nextCursor ?? null)
    } catch (e) {
      onError(e)
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入/切换筛选时加载数据，状态更新在异步请求之后
    void load(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status])

  if (detailID) {
    return <ReportDetail id={detailID} onBack={() => setDetailID(null)} onError={onError} />
  }

  return (
    <div className="space-y-3">
      <select aria-label="举报状态" className="bg-lobby-bg rounded-sm px-2 py-1 text-sm" value={status} onChange={(e) => setStatus(e.target.value)}>
        <option value="open">待处理</option>
        <option value="resolved">已处理</option>
      </select>
      <div className="space-y-2">
        {reports.map((r) => (
          <button key={r.id} type="button" className="w-full text-left rounded-sm bg-lobby-card border border-lobby-line p-3 hover:bg-lobby-bg" onClick={() => setDetailID(r.id)}>
            <div className="flex justify-between text-sm">
              <span>{r.reason}</span>
              <span className="text-lobby-sub">{new Date(r.createdAt).toLocaleString()}</span>
            </div>
            <div className="text-xs text-lobby-sub">状态: {r.status}</div>
          </button>
        ))}
        {reports.length === 0 && <p className="text-lobby-sub">暂无举报</p>}
      </div>
      {cursor && (
        <button type="button" className="px-3 py-1 rounded-sm bg-lobby-bg text-sm" onClick={() => void load(false)}>
          加载更多
        </button>
      )}
    </div>
  )
}

function ReportDetail({ id, onBack, onError }: { id: string; onBack: () => void; onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [detail, setDetail] = useState<AdminReportDetail | null>(null)
  const [form, setForm] = useState({ identity: true, ip: false, account: false, durationHours: 24 as number | null, reason: '' })
  const [errors, setErrors] = useState<string[]>([])

  useEffect(() => {
    adminReport(id)
      .then(setDetail)
      .catch((e) => onError(e))
  }, [id, onError])

  if (!detail) return <p className="text-lobby-sub">加载中…</p>

  const snapshot: SnapshotItem[] = Array.isArray(detail.snapshot) ? (detail.snapshot as SnapshotItem[]) : []

  async function dismiss() {
    try {
      await adminResolveReport(id, { action: 'dismiss' })
      setToast('已驳回举报')
      onBack()
    } catch (e) {
      onError(e)
    }
  }

  async function ban() {
    const res = validateBanForm(form)
    setErrors(res.errors)
    if (!res.ok) return
    try {
      await adminResolveReport(id, {
        action: 'ban',
        ban: { subjects: banSubjects(form), durationHours: form.durationHours === 0 ? null : form.durationHours, reason: form.reason },
      })
      setToast('已封禁并处理举报')
      onBack()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-4">
      <button type="button" className="text-sm text-brand" onClick={onBack}>
        ← 返回列表
      </button>
      <div className="rounded-sm bg-lobby-card border border-lobby-line p-3 text-sm space-y-1">
        <div>原因：{detail.reason}</div>
        <div>状态：{detail.status}</div>
        <div>被举报者：{detail.reported.nickname}</div>
        <div>创建 IP：{detail.reported.createdIp || '—'}</div>
        <div>最近连接 IP：{detail.reported.lastIp || '—'}</div>
        <div>实名号码：{detail.reported.phoneMasked || '未实名'}</div>
        <div>历史被举报：{detail.reported.reportCount} 次</div>
      </div>

      <div className="rounded-sm bg-lobby-card border border-lobby-line p-3">
        <h3 className="font-semibold mb-2 text-sm">证据快照</h3>
        <div className="space-y-1 text-sm">
          {snapshot.map((s) => (
            <div key={s.id} className={`whitespace-pre-wrap break-words rounded-sm px-2 py-1 ${s.reported ? 'bg-danger/10 border border-danger/40' : 'bg-lobby-bg/50'}`}>
              <span className="text-lobby-sub mr-2">{s.nickname}</span>
              {s.body}
            </div>
          ))}
        </div>
      </div>

      {detail.status === 'open' && (
        <div className="rounded-sm bg-lobby-card border border-lobby-line p-3 space-y-3">
          <h3 className="font-semibold text-sm">处理</h3>
          <div className="flex gap-2">
            <button type="button" className="px-3 py-1 rounded-sm bg-lobby-line text-sm" onClick={dismiss}>
              驳回
            </button>
          </div>
          <div className="space-y-2 text-sm">
            <div className="flex gap-3">
              <label className="flex items-center gap-1">
                <input type="checkbox" checked={form.identity} onChange={(e) => setForm({ ...form, identity: e.target.checked })} /> 身份
              </label>
              <label className="flex items-center gap-1">
                <input type="checkbox" checked={form.ip} onChange={(e) => setForm({ ...form, ip: e.target.checked })} /> IP
              </label>
              <label className="flex items-center gap-1">
                <input type="checkbox" checked={form.account} onChange={(e) => setForm({ ...form, account: e.target.checked })} /> 实名账号
              </label>
            </div>
            <select aria-label="封禁时长" className="bg-lobby-bg rounded-sm px-2 py-1" value={form.durationHours ?? 0} onChange={(e) => setForm({ ...form, durationHours: Number(e.target.value) })}>
              {DURATIONS.map((d) => (
                <option key={d.value} value={d.value}>
                  {d.label}
                </option>
              ))}
            </select>
            <textarea aria-label="封禁原因" className="w-full bg-lobby-bg rounded-sm px-2 py-1" rows={2} value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} placeholder="封禁原因（必填）" />
            {errors.length > 0 && <div className="text-danger text-xs">{errors.join('；')}</div>}
            <button type="button" className="px-3 py-1 rounded-sm bg-danger text-sm" onClick={ban}>
              封禁并处理
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function BansView({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [active, setActive] = useState<'true' | 'false'>('true')
  const [bans, setBans] = useState<AdminBan[]>([])
  const [cursor, setCursor] = useState<string | null>(null)

  const load = useCallback(
    async (reset: boolean) => {
      try {
        const r = await adminBans({ active: active === 'true', cursor: reset ? undefined : cursor ?? undefined, limit: 20 })
        setBans((prev) => (reset ? r.bans : [...prev, ...r.bans]))
        setCursor(r.nextCursor ?? null)
      } catch (e) {
        onError(e)
      }
    },
    [active, cursor, onError],
  )

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换筛选时加载数据，状态更新在异步请求之后
    void load(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  async function revoke(id: string) {
    if (!confirm('确定要撤销该封禁吗？')) return
    try {
      await adminRevokeBan(id)
      setToast('已撤销封禁')
      setBans((prev) => prev.filter((b) => b.id !== id))
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3">
      <select aria-label="封禁筛选" className="bg-lobby-bg rounded-sm px-2 py-1 text-sm" value={active} onChange={(e) => setActive(e.target.value as 'true' | 'false')}>
        <option value="true">生效中</option>
        <option value="false">全部</option>
      </select>
      <div className="space-y-2">
        {bans.map((b) => (
          <div key={b.id} className="rounded-sm bg-lobby-card border border-lobby-line p-3 text-sm flex justify-between items-start">
            <div>
              <div>
                {b.subjectType}: {b.subjectValue}
              </div>
              <div className="text-lobby-sub">原因：{b.reason}</div>
              <div className="text-lobby-sub">
                {b.active ? '生效中' : '已失效'} {b.expiresAt ? `· 至 ${new Date(b.expiresAt).toLocaleString()}` : '· 永久'}
              </div>
            </div>
            {b.active && (
              <button type="button" className="px-2 py-1 rounded-sm bg-danger text-xs" onClick={() => void revoke(b.id)}>
                撤销
              </button>
            )}
          </div>
        ))}
        {bans.length === 0 && <p className="text-lobby-sub">暂无封禁</p>}
      </div>
      {cursor && (
        <button type="button" className="px-3 py-1 rounded-sm bg-lobby-bg text-sm" onClick={() => void load(false)}>
          加载更多
        </button>
      )}
    </div>
  )
}

function AuditView({ onError }: { onError: (e: unknown) => void }) {
  const [logs, setLogs] = useState<AdminAuditLog[]>([])
  const [cursor, setCursor] = useState<number | null>(null)

  const load = useCallback(
    async (reset: boolean) => {
      try {
        const r = await adminAuditLogs({ cursor: reset ? undefined : cursor != null ? String(cursor) : undefined, limit: 20 })
        setLogs((prev) => (reset ? r.logs : [...prev, ...r.logs]))
        setCursor(r.nextCursor ?? null)
      } catch (e) {
        onError(e)
      }
    },
    [cursor, onError],
  )

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载审计日志
    void load(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div className="space-y-2 text-sm">
      {logs.map((l) => (
        <div key={l.id} className="rounded-sm bg-lobby-card border border-lobby-line p-2">
          <div className="flex justify-between">
            <span>{l.action}</span>
            <span className="text-lobby-sub">{new Date(l.createdAt).toLocaleString()}</span>
          </div>
          {l.target && <div className="text-lobby-sub">对象：{l.target}</div>}
        </div>
      ))}
      {logs.length === 0 && <p className="text-lobby-sub">暂无审计日志</p>}
      {cursor != null && (
        <button type="button" className="px-3 py-1 rounded-sm bg-lobby-bg" onClick={() => void load(false)}>
          加载更多
        </button>
      )}
    </div>
  )
}

// ---- 公告 ----

function AnnouncementsView({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [list, setList] = useState<AdminAnnouncement[]>([])
  const [content, setContent] = useState('')

  const load = useCallback(async () => {
    try {
      const r = await adminAnnouncements()
      setList(r.announcements)
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载公告
    void load()
  }, [load])

  async function create() {
    if (content.trim() === '') return
    try {
      await adminCreateAnnouncement({ content: content.trim() })
      setContent('')
      setToast('公告已发布')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(id: string) {
    try {
      await adminDeleteAnnouncement(id)
      setToast('公告已删除')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3 text-sm">
      <div className="rounded-sm border border-lobby-line bg-lobby-card p-3 space-y-2">
        <textarea
          aria-label="公告内容"
          className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
          rows={2}
          maxLength={120}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="公告内容（1–120 字），会在首页跑马灯滚动"
        />
        <button type="button" className="rounded-sm bg-brand px-3 py-1 text-white" onClick={() => void create()}>
          发布公告
        </button>
      </div>
      {list.length === 0 && <p className="text-lobby-sub">暂无公告</p>}
      {list.map((a) => (
        <div key={a.id} className="flex items-start justify-between gap-3 rounded-sm border border-lobby-line bg-lobby-card p-3">
          <div>
            <div className="text-lobby-ink">{a.content}</div>
            <div className="text-xs text-lobby-sub">{new Date(a.createdAt).toLocaleString()}</div>
          </div>
          <button type="button" className="rounded-sm bg-danger px-2 py-1 text-xs text-white" onClick={() => void remove(a.id)}>
            删除
          </button>
        </div>
      ))}
    </div>
  )
}

// ---- 广播审核 ----

function BroadcastsView({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [status, setStatus] = useState('pending')
  const [list, setList] = useState<AdminBroadcast[]>([])
  const [reason, setReason] = useState('')

  const load = useCallback(async () => {
    try {
      const r = await adminBroadcasts(status)
      setList(r.broadcasts)
    } catch (e) {
      onError(e)
    }
  }, [status, onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换筛选时加载审核队列
    void load()
  }, [load])

  async function approve(id: string) {
    try {
      await adminApproveBroadcast(id)
      setToast('已通过，展示 24 小时')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function reject(id: string) {
    try {
      await adminRejectBroadcast(id, reason)
      setToast('已驳回并退还广播卡')
      setReason('')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(id: string) {
    try {
      await adminRemoveBroadcast(id)
      setToast('已下架')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3 text-sm">
      <select
        aria-label="广播状态"
        className="rounded-sm bg-lobby-bg border border-lobby-line px-2 py-1"
        value={status}
        onChange={(e) => setStatus(e.target.value)}
      >
        <option value="pending">待审核</option>
        <option value="approved">已通过</option>
        <option value="rejected">已驳回</option>
        <option value="removed">已下架</option>
      </select>
      <input
        aria-label="驳回原因"
        className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="驳回原因（可选）"
      />
      {list.length === 0 && <p className="text-lobby-sub">暂无广播</p>}
      {list.map((b) => (
        <div
          key={b.id}
          data-testid="admin-broadcast"
          className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-3"
        >
          <div className="text-lobby-ink">{b.content}</div>
          <div className="text-xs text-lobby-sub">
            状态：{b.status} · 提交者编号：{b.publicCode} · {new Date(b.createdAt).toLocaleString()}
          </div>
          <div className="flex gap-2">
            {b.status === 'pending' && (
              <>
                <button type="button" className="rounded-sm bg-brand px-3 py-1 text-xs text-white" onClick={() => void approve(b.id)}>
                  通过
                </button>
                <button type="button" className="rounded-sm bg-danger px-3 py-1 text-xs text-white" onClick={() => void reject(b.id)}>
                  驳回
                </button>
              </>
            )}
            {b.status === 'approved' && (
              <button type="button" className="rounded-sm bg-lobby-bg px-3 py-1 text-xs text-lobby-ink" onClick={() => void remove(b.id)}>
                下架
              </button>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

// ---- 激活码批次 ----

function CodesView({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [batches, setBatches] = useState<ActivationBatch[]>([])
  const [form, setForm] = useState({ label: '', vipDays: 30, broadcastCards: 3, count: 10, days: 30 })
  /** 明文激活码只在创建后展示一次。 */
  const [codes, setCodes] = useState<string[]>([])

  const load = useCallback(async () => {
    try {
      const r = await adminActivationBatches()
      setBatches(r.batches)
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时加载批次列表
    void load()
  }, [load])

  async function create() {
    try {
      const expiresAt = new Date(Date.now() + form.days * 24 * 3600 * 1000).toISOString()
      const r = await adminCreateActivationBatch({
        label: form.label || '未命名批次',
        vipDays: form.vipDays,
        broadcastCards: form.broadcastCards,
        count: form.count,
        expiresAt,
      })
      setCodes(r.codes)
      setToast('批次已创建，请立即下载明文激活码')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function revoke(id: string) {
    try {
      const r = await adminRevokeActivationBatch(id)
      setToast(`已撤销 ${r.revoked} 个未兑换的激活码`)
      void load()
    } catch (e) {
      onError(e)
    }
  }

  /** CSV 下载：明文只在创建时出现一次，由前端直接生成本地文件。 */
  function downloadCSV() {
    const csv = `code\n${codes.join('\n')}\n`
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `activation-codes-${Date.now()}.csv`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="space-y-3 text-sm">
      <div className="grid gap-2 rounded-sm border border-lobby-line bg-lobby-card p-3 sm:grid-cols-5">
        <input
          aria-label="批次名称"
          className="rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
          placeholder="批次名称"
          value={form.label}
          onChange={(e) => setForm({ ...form, label: e.target.value })}
        />
        <input
          aria-label="VIP 天数"
          type="number"
          className="rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
          value={form.vipDays}
          onChange={(e) => setForm({ ...form, vipDays: Number(e.target.value) })}
        />
        <input
          aria-label="广播卡张数"
          type="number"
          className="rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
          value={form.broadcastCards}
          onChange={(e) => setForm({ ...form, broadcastCards: Number(e.target.value) })}
        />
        <input
          aria-label="激活码数量"
          type="number"
          className="rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
          value={form.count}
          onChange={(e) => setForm({ ...form, count: Number(e.target.value) })}
        />
        <button type="button" className="rounded-sm bg-brand px-3 py-1 text-white" onClick={() => void create()}>
          生成批次
        </button>
      </div>

      {codes.length > 0 && (
        <div className="space-y-2 rounded-sm border border-brand/40 bg-brand/5 p-3">
          <div className="text-xs text-brand">明文激活码只显示这一次，请立即下载。</div>
          <div className="flex gap-2">
            <button type="button" className="rounded-sm bg-brand px-3 py-1 text-xs text-white" onClick={downloadCSV}>
              下载 CSV
            </button>
            <button type="button" className="rounded-sm bg-lobby-bg px-3 py-1 text-xs" onClick={() => setCodes([])}>
              我已保存
            </button>
          </div>
          <div className="max-h-40 overflow-y-auto font-mono text-xs text-lobby-ink">
            {codes.map((c) => (
              <div key={c}>{c}</div>
            ))}
          </div>
        </div>
      )}

      {batches.length === 0 && <p className="text-lobby-sub">暂无批次</p>}
      {batches.map((b) => (
        <div key={b.id} className="flex items-start justify-between gap-3 rounded-sm border border-lobby-line bg-lobby-card p-3">
          <div>
            <div className="text-lobby-ink">{b.label}</div>
            <div className="text-xs text-lobby-sub">
              VIP {b.vipDays} 天 · 广播卡 {b.broadcastCards} 张 · 共 {b.total} 个 · 已兑换 {b.redeemed} · 已撤销 {b.revoked}
            </div>
          </div>
          <button type="button" className="rounded-sm bg-danger px-2 py-1 text-xs text-white" onClick={() => void revoke(b.id)}>
            撤销未兑换
          </button>
        </div>
      ))}
    </div>
  )
}

// ---- 作品审核 ----

function WorksView({ onError }: { onError: (e: unknown) => void }) {
  const setToast = useUiStore((s) => s.setToast)
  const [status, setStatus] = useState('pending')
  const [list, setList] = useState<AdminWork[]>([])
  const [reason, setReason] = useState('')

  const load = useCallback(async () => {
    try {
      const r = await adminWorks(status)
      setList(r.works)
    } catch (e) {
      onError(e)
    }
  }, [status, onError])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换筛选时加载作品列表
    void load()
  }, [load])

  async function approve(id: string) {
    try {
      await adminApproveWork(id)
      setToast('作品已通过')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function reject(id: string) {
    try {
      await adminRejectWork(id, reason)
      setToast('作品已驳回')
      setReason('')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(id: string) {
    try {
      await adminRemoveWork(id)
      setToast('作品已下架')
      void load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="space-y-3 text-sm">
      <select
        aria-label="作品状态"
        className="rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
        value={status}
        onChange={(e) => setStatus(e.target.value)}
      >
        <option value="pending">待审核</option>
        <option value="approved">已通过</option>
        <option value="rejected">已驳回</option>
        <option value="removed">已下架</option>
      </select>
      <input
        aria-label="作品驳回原因"
        className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="驳回原因（可选）"
      />
      {list.length === 0 && <p className="text-lobby-sub">暂无作品</p>}
      {list.map((w) => (
        <div
          key={w.id}
          data-testid="admin-work"
          className="space-y-2 rounded-sm border border-lobby-line bg-lobby-card p-3"
        >
          <div className="text-lobby-ink">《{w.title}》</div>
          <div className="text-xs text-lobby-sub">
            状态：{w.status} · {w.likeCount} 赞 · {new Date(w.createdAt).toLocaleString()}
          </div>
          <div className="flex gap-2">
            {w.status === 'pending' && (
              <>
                <button type="button" className="rounded-sm bg-brand px-3 py-1 text-xs text-white" onClick={() => void approve(w.id)}>
                  通过
                </button>
                <button type="button" className="rounded-sm bg-danger px-3 py-1 text-xs text-white" onClick={() => void reject(w.id)}>
                  驳回
                </button>
              </>
            )}
            {w.status === 'approved' && (
              <button type="button" className="rounded-sm bg-lobby-bg px-3 py-1 text-xs text-lobby-ink" onClick={() => void remove(w.id)}>
                下架
              </button>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

// ---- 按匿名编号查身份 ----

function LookupView({ onError }: { onError: (e: unknown) => void }) {
  const [code, setCode] = useState('')
  const [result, setResult] = useState<AdminIdentityLookup | null>(null)

  async function lookup() {
    try {
      setResult(await adminLookupIdentity(code.trim()))
    } catch (e) {
      setResult(null)
      onError(e)
    }
  }

  return (
    <div className="space-y-3 text-sm">
      <div className="flex gap-2">
        <input
          aria-label="匿名编号"
          className="flex-1 rounded-sm border border-lobby-line bg-lobby-bg px-2 py-1 font-mono"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="例如 ABCD3FGH9K"
        />
        <button type="button" className="rounded-sm bg-brand px-3 py-1 text-white" onClick={() => void lookup()}>
          查询
        </button>
      </div>
      {result && (
        <div className="space-y-1 rounded-sm border border-lobby-line bg-lobby-card p-3">
          <div>身分 ID：{result.identityId}</div>
          <div>匿名编号：{result.publicCode}</div>
          <div>创建时间：{new Date(result.createdAt).toLocaleString()}</div>
          <div>VIP：{result.vipActive ? '生效中' : '未开通'}</div>
          <div>封禁：{result.banned ? '已封禁' : '正常'}</div>
          <div>昵称：{result.summary.nickname}</div>
          <div>最近连接 IP：{result.summary.lastIp || '—'}</div>
          <div>历史被举报：{result.summary.reportCount} 次</div>
        </div>
      )}
    </div>
  )
}
