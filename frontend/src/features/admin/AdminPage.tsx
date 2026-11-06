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
} from '../../lib/api/client'
import type { AdminStats, AdminReportSummary, AdminReportDetail, AdminBan, AdminAuditLog } from '../../lib/api/client'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { validateBanForm, banSubjects } from './banForm'

type Tab = 'overview' | 'reports' | 'bans' | 'audit'

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
    return <main className="p-8 text-slate-400">加载中…</main>
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
          className="text-sm px-3 py-1 rounded bg-slate-800"
          onClick={() => {
            void adminLogout().finally(() => setAuthed(false))
          }}
        >
          退出登录
        </button>
      </div>
      <div className="flex gap-2 mb-4 text-sm">
        {([
          ['overview', '概览'],
          ['reports', '举报'],
          ['bans', '封禁'],
          ['audit', '审计'],
        ] as Array<[Tab, string]>).map(([k, label]) => (
          <button
            key={k}
            type="button"
            className={`px-3 py-1 rounded ${tab === k ? 'bg-indigo-600 text-white' : 'bg-slate-800'}`}
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
      <input aria-label="用户名" className="w-full bg-slate-800 rounded px-3 py-2" placeholder="用户名" value={username} onChange={(e) => setUsername(e.target.value)} />
      <input aria-label="密码" type="password" className="w-full bg-slate-800 rounded px-3 py-2" placeholder="密码" value={password} onChange={(e) => setPassword(e.target.value)} />
      <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white disabled:opacity-40" disabled={busy} onClick={submit}>
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

  if (!stats) return <p className="text-slate-400">加载中…</p>
  const cards = [
    { label: '在线人数', value: stats.online },
    { label: '进行中会话', value: stats.activeConversations },
    { label: '24 小时消息', value: stats.messages24h },
    { label: '待处理举报', value: stats.openReports },
  ]
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {cards.map((c) => (
        <div key={c.label} className="rounded-lg bg-slate-900 border border-slate-800 p-4">
          <div className="text-3xl font-bold">{c.value}</div>
          <div className="text-sm text-slate-400">{c.label}</div>
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
      <select aria-label="举报状态" className="bg-slate-800 rounded px-2 py-1 text-sm" value={status} onChange={(e) => setStatus(e.target.value)}>
        <option value="open">待处理</option>
        <option value="resolved">已处理</option>
      </select>
      <div className="space-y-2">
        {reports.map((r) => (
          <button key={r.id} type="button" className="w-full text-left rounded bg-slate-900 border border-slate-800 p-3 hover:bg-slate-800" onClick={() => setDetailID(r.id)}>
            <div className="flex justify-between text-sm">
              <span>{r.reason}</span>
              <span className="text-slate-500">{new Date(r.createdAt).toLocaleString()}</span>
            </div>
            <div className="text-xs text-slate-500">状态: {r.status}</div>
          </button>
        ))}
        {reports.length === 0 && <p className="text-slate-400">暂无举报</p>}
      </div>
      {cursor && (
        <button type="button" className="px-3 py-1 rounded bg-slate-800 text-sm" onClick={() => void load(false)}>
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

  if (!detail) return <p className="text-slate-400">加载中…</p>

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
      <button type="button" className="text-sm text-indigo-400" onClick={onBack}>
        ← 返回列表
      </button>
      <div className="rounded bg-slate-900 border border-slate-800 p-3 text-sm space-y-1">
        <div>原因：{detail.reason}</div>
        <div>状态：{detail.status}</div>
        <div>被举报者：{detail.reported.nickname}</div>
        <div>创建 IP：{detail.reported.createdIp || '—'}</div>
        <div>最近连接 IP：{detail.reported.lastIp || '—'}</div>
        <div>实名号码：{detail.reported.phoneMasked || '未实名'}</div>
        <div>历史被举报：{detail.reported.reportCount} 次</div>
      </div>

      <div className="rounded bg-slate-900 border border-slate-800 p-3">
        <h3 className="font-semibold mb-2 text-sm">证据快照</h3>
        <div className="space-y-1 text-sm">
          {snapshot.map((s) => (
            <div key={s.id} className={`whitespace-pre-wrap break-words rounded px-2 py-1 ${s.reported ? 'bg-red-950/50 border border-red-800' : 'bg-slate-800/50'}`}>
              <span className="text-slate-500 mr-2">{s.nickname}</span>
              {s.body}
            </div>
          ))}
        </div>
      </div>

      {detail.status === 'open' && (
        <div className="rounded bg-slate-900 border border-slate-800 p-3 space-y-3">
          <h3 className="font-semibold text-sm">处理</h3>
          <div className="flex gap-2">
            <button type="button" className="px-3 py-1 rounded bg-slate-700 text-sm" onClick={dismiss}>
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
            <select aria-label="封禁时长" className="bg-slate-800 rounded px-2 py-1" value={form.durationHours ?? 0} onChange={(e) => setForm({ ...form, durationHours: Number(e.target.value) })}>
              {DURATIONS.map((d) => (
                <option key={d.value} value={d.value}>
                  {d.label}
                </option>
              ))}
            </select>
            <textarea aria-label="封禁原因" className="w-full bg-slate-800 rounded px-2 py-1" rows={2} value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} placeholder="封禁原因（必填）" />
            {errors.length > 0 && <div className="text-red-400 text-xs">{errors.join('；')}</div>}
            <button type="button" className="px-3 py-1 rounded bg-red-600 text-sm" onClick={ban}>
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
      <select aria-label="封禁筛选" className="bg-slate-800 rounded px-2 py-1 text-sm" value={active} onChange={(e) => setActive(e.target.value as 'true' | 'false')}>
        <option value="true">生效中</option>
        <option value="false">全部</option>
      </select>
      <div className="space-y-2">
        {bans.map((b) => (
          <div key={b.id} className="rounded bg-slate-900 border border-slate-800 p-3 text-sm flex justify-between items-start">
            <div>
              <div>
                {b.subjectType}: {b.subjectValue}
              </div>
              <div className="text-slate-500">原因：{b.reason}</div>
              <div className="text-slate-500">
                {b.active ? '生效中' : '已失效'} {b.expiresAt ? `· 至 ${new Date(b.expiresAt).toLocaleString()}` : '· 永久'}
              </div>
            </div>
            {b.active && (
              <button type="button" className="px-2 py-1 rounded bg-red-600 text-xs" onClick={() => void revoke(b.id)}>
                撤销
              </button>
            )}
          </div>
        ))}
        {bans.length === 0 && <p className="text-slate-400">暂无封禁</p>}
      </div>
      {cursor && (
        <button type="button" className="px-3 py-1 rounded bg-slate-800 text-sm" onClick={() => void load(false)}>
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
        <div key={l.id} className="rounded bg-slate-900 border border-slate-800 p-2">
          <div className="flex justify-between">
            <span>{l.action}</span>
            <span className="text-slate-500">{new Date(l.createdAt).toLocaleString()}</span>
          </div>
          {l.target && <div className="text-slate-500">对象：{l.target}</div>}
        </div>
      ))}
      {logs.length === 0 && <p className="text-slate-400">暂无审计日志</p>}
      {cursor != null && (
        <button type="button" className="px-3 py-1 rounded bg-slate-800" onClick={() => void load(false)}>
          加载更多
        </button>
      )}
    </div>
  )
}
