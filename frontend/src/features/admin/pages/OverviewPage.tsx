import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { adminStats } from '../../../lib/api/client'
import type { AdminStats } from '../../../lib/api/client'
import { useAdmin } from '../context'
import { formatClockTime } from '../labels'
import { Skeleton } from '../../../components/Skeleton'
import { Button, PageHeader, StatusBadge } from '../ui'

const QUICK_LINKS = [
  // 文案刻意与侧边栏导航区分，避免同一页出现两个可访问名相同的链接。
  { to: '/admin/reports', label: '待处理举报', desc: '查看证据快照并执行封禁' },
  { to: '/admin/broadcasts', label: '待审广播', desc: '审核用户提交的广播内容' },
  { to: '/admin/works', label: '待审作品', desc: '收录双方同意的共创作品' },
  { to: '/admin/lookup', label: '查身份详情', desc: '按匿名编号查询身份' },
]

/** 概览：四个核心指标 + 手动刷新 + 快捷入口。 */
export function OverviewPage() {
  const { onError } = useAdmin()
  const [stats, setStats] = useState<AdminStats | null>(null)
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const s = await adminStats()
        if (cancelled) return
        setStats(s)
        setUpdatedAt(new Date())
      } catch (e) {
        if (!cancelled) onError(e)
      }
    }
    void load()
    const timer = setInterval(() => void load(), 30000)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [onError])

  async function refresh() {
    setRefreshing(true)
    try {
      const s = await adminStats()
      setStats(s)
      setUpdatedAt(new Date())
    } catch (e) {
      onError(e)
    } finally {
      setRefreshing(false)
    }
  }

  const cards = [
    { label: '在线人数', value: stats?.online, highlight: false },
    { label: '进行中会话', value: stats?.activeConversations, highlight: false },
    { label: '24 小时消息', value: stats?.messages24h, highlight: false },
    { label: '待处理举报', value: stats?.openReports, highlight: (stats?.openReports ?? 0) > 0 },
  ]

  return (
    <div>
      <PageHeader
        title="概览"
        description="站点实时状况，每 30 秒自动刷新。"
        actions={
          <>
            <span className="text-xs text-lobby-sub">
              更新于 {updatedAt ? formatClockTime(updatedAt) : '—'}
            </span>
            <Button size="sm" loading={refreshing} onClick={() => void refresh()}>
              刷新
            </Button>
          </>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map((c) => {
          const body = stats ? (
            <>
              <div className="text-3xl font-semibold tabular-nums text-lobby-ink">
                {c.value}
              </div>
              <div className="mt-1 flex items-center gap-2 text-sm text-lobby-sub">
                {c.label}
                {c.highlight && <StatusBadge tone="warning">需处理</StatusBadge>}
              </div>
            </>
          ) : (
            <>
              <Skeleton className="h-8 w-16" />
              <Skeleton className="mt-2 h-4 w-24" />
            </>
          )
          const base = `block rounded-card border bg-lobby-card p-4 shadow-soft ${
            c.highlight ? 'border-accent/50' : 'border-lobby-line'
          }`
          return c.label === '待处理举报' ? (
            <Link key={c.label} to="/admin/reports" className={`${base} hover:border-brand/40`}>
              {body}
            </Link>
          ) : (
            <div key={c.label} className={base}>
              {body}
            </div>
          )
        })}
      </div>

      <h2 className="mt-6 mb-2 text-sm font-medium text-lobby-sub">快捷入口</h2>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {QUICK_LINKS.map((q) => (
          <Link
            key={q.to}
            to={q.to}
            className="rounded-card border border-lobby-line bg-lobby-card p-4 shadow-soft transition duration-150 ease-out hover:border-brand/40"
          >
            <div className="text-sm font-medium text-lobby-ink">{q.label}</div>
            <div className="mt-1 text-xs text-lobby-sub">{q.desc}</div>
          </Link>
        ))}
      </div>
    </div>
  )
}
