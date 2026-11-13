import { useCallback, useEffect, useState } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { ApiError, adminStats } from '../../lib/api/client'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { AdminContext } from './context'
import { AdminLayout } from './AdminLayout'
import { LoginPage } from './LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { ReportsPage } from './pages/ReportsPage'
import { ReportDetailPage } from './pages/ReportDetailPage'
import { RateLimitsPage } from './pages/RateLimitsPage'
import { BansPage } from './pages/BansPage'
import { LookupPage } from './pages/LookupPage'
import { AnnouncementsPage } from './pages/AnnouncementsPage'
import { CodesPage } from './pages/CodesPage'
import { BroadcastsPage } from './pages/BroadcastsPage'
import { WorksPage } from './pages/WorksPage'
import { AuditPage } from './pages/AuditPage'
import { BotConfigPage } from './pages/BotConfigPage'
import { ExternalPage } from './pages/ExternalPage'

/**
 * 管理后台入口：只负责登录态探测，之后把渲染交给内嵌路由。
 * 路由嵌套在 App 的 /admin/* 下，因此这里的路径都是相对路径。
 */
export default function AdminPage() {
  const setToast = useUiStore((s) => s.setToast)
  const [authed, setAuthed] = useState<boolean | null>(null)

  const onError = useCallback(
    (e: unknown) => {
      if (e instanceof ApiError && e.status === 401) {
        setAuthed(false)
        return
      }
      setToast(errorMessage((e as { code?: string }).code))
    },
    [setToast],
  )

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        await adminStats()
        if (!cancelled) setAuthed(true)
      } catch {
        if (!cancelled) setAuthed(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  if (authed === null) {
    return <main className="p-8 text-sm text-lobby-sub">加载中…</main>
  }

  if (!authed) {
    return <LoginPage onSuccess={() => setAuthed(true)} onError={onError} />
  }

  return (
    <AdminContext.Provider
      value={{
        onError,
        onLogout: () => {
          setAuthed(false)
        },
      }}
    >
      <Routes>
        <Route element={<AdminLayout />}>
          <Route index element={<OverviewPage />} />
          <Route path="reports" element={<ReportsPage />} />
          <Route path="reports/:id" element={<ReportDetailPage />} />
          <Route path="bans" element={<BansPage />} />
          <Route path="rate-limits" element={<RateLimitsPage />} />
          <Route path="lookup" element={<LookupPage />} />
          <Route path="announcements" element={<AnnouncementsPage />} />
          <Route path="codes" element={<CodesPage />} />
          <Route path="broadcasts" element={<BroadcastsPage />} />
          <Route path="works" element={<WorksPage />} />
          <Route path="bot" element={<BotConfigPage />} />
          <Route path="external" element={<ExternalPage />} />
          <Route path="audit" element={<AuditPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/admin" replace />} />
      </Routes>
    </AdminContext.Provider>
  )
}
