import { useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { adminLogout, adminStats } from '../../lib/api/client'
import { useUiStore } from '../../stores/ui'
import { useAdmin } from './context'
import { Button } from './ui'

interface NavItem {
  to: string
  label: string
  /** 概览只在精确匹配时高亮。 */
  end?: boolean
  /** 角标：待处理举报数。 */
  badgeKey?: 'openReports'
}

interface NavGroup {
  title?: string
  items: NavItem[]
}

const NAV_GROUPS: NavGroup[] = [
  { items: [{ to: '/admin', label: '概览', end: true }] },
  {
    title: '内容审核',
    items: [
      { to: '/admin/reports', label: '举报处理', badgeKey: 'openReports' },
      { to: '/admin/broadcasts', label: '广播审核' },
      { to: '/admin/works', label: '作品审核' },
    ],
  },
  {
    title: '用户管理',
    items: [
      { to: '/admin/bans', label: '封禁管理' },
      { to: '/admin/rate-limits', label: '限流规则' },
      { to: '/admin/lookup', label: '身份查询' },
    ],
  },
  {
    title: '运营',
    items: [
      { to: '/admin/announcements', label: '公告' },
      { to: '/admin/codes', label: '激活码' },
    ],
  },
  {
    title: '系统',
    items: [
      { to: '/admin/bot', label: 'Bot 配置' },
      { to: '/admin/audit', label: '审计日志' },
    ],
  },
]

function linkClass({ isActive }: { isActive: boolean }): string {
  return `flex items-center justify-between gap-2 rounded-sm px-3 py-1.5 text-sm transition duration-150 ease-out ${
    isActive
      ? 'bg-brand/10 font-medium text-brand'
      : 'text-lobby-sub hover:bg-lobby-bg hover:text-lobby-ink'
  }`
}

/** 后台外壳：宽屏左侧边栏，窄屏顶部横向滚动导航。 */
export function AdminLayout() {
  const { onError, onLogout } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [openReports, setOpenReports] = useState(0)
  const [loggingOut, setLoggingOut] = useState(false)

  // 侧边栏角标：挂载时拉一次，之后每 60 秒刷新。
  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const s = await adminStats()
        if (!cancelled) setOpenReports(s.openReports)
      } catch (e) {
        if (!cancelled) onError(e)
      }
    }
    void load()
    const timer = setInterval(() => void load(), 60000)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [onError])

  async function logout() {
    setLoggingOut(true)
    try {
      await adminLogout()
      setToast('已退出登录')
      onLogout()
    } catch (e) {
      onError(e)
    } finally {
      setLoggingOut(false)
    }
  }

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <aside className="border-b border-lobby-line bg-lobby-card md:sticky md:top-0 md:max-h-screen md:w-[220px] md:shrink-0 md:self-start md:overflow-y-auto md:border-r md:border-b-0">
        <div className="flex flex-col gap-3 p-4 md:h-full">
          <div className="shrink-0">
            <div className="text-base font-semibold text-lobby-ink">AnonChat 管理后台</div>
            <div className="text-xs text-lobby-sub">内容审核与运营控制台</div>
          </div>

          <nav
            aria-label="后台导航"
            className="flex gap-1 overflow-x-auto pb-1 md:flex-1 md:flex-col md:overflow-visible md:pb-0"
          >
            {NAV_GROUPS.map((group, i) => (
              <div key={group.title ?? `group-${i}`} className="contents md:block md:space-y-0.5">
                {group.title && (
                  <div className="hidden px-3 pt-3 pb-1 text-[11px] font-medium tracking-wide text-lobby-sub md:block">
                    {group.title}
                  </div>
                )}
                {group.items.map((item) => (
                  <NavLink key={item.to} to={item.to} end={item.end} className={linkClass}>
                    <span className="whitespace-nowrap">{item.label}</span>
                    {item.badgeKey === 'openReports' && openReports > 0 && (
                      <span className="rounded-full bg-brand px-1.5 py-0.5 text-[11px] leading-none text-white">
                        {openReports}
                      </span>
                    )}
                  </NavLink>
                ))}
              </div>
            ))}
          </nav>

          <div className="shrink-0 md:border-t md:border-lobby-line md:pt-3">
            <Button
              variant="ghost"
              size="sm"
              loading={loggingOut}
              onClick={() => void logout()}
              className="w-full md:justify-start"
            >
              退出登录
            </Button>
          </div>
        </div>
      </aside>

      <main className="min-w-0 flex-1 px-4 py-5 sm:px-6">
        <div className="mx-auto w-full max-w-[1200px]">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
