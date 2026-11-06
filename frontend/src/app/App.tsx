import { lazy, Suspense, useEffect } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { useIdentityStore, onIdentityReset } from '../stores/identity'
import { useConversationsStore } from '../stores/conversations'
import { usePresenceStore } from '../stores/presence'
import { useUiStore } from '../stores/ui'
import { getWsClient } from '../lib/ws'
import { errorMessage } from '../lib/api/errorMessages'
import { getMe } from '../lib/api/client'
import { TopBar } from './TopBar'
import { Toast } from '../components/Toast'
import { KickedOverlay } from '../components/KickedOverlay'
import { VerificationModal } from '../components/VerificationModal'
import HomePage from '../features/home/HomePage'
import MatchPage from '../features/match/MatchPage'
import RoomsPage from '../features/rooms/RoomsPage'
import RoomPage from '../features/rooms/RoomPage'
import BottlesPage from '../features/bottles/BottlesPage'
import BottleConversationPage from '../features/bottles/BottleConversationPage'
import PrivateNewPage from '../features/private/PrivateNewPage'
import PrivateJoinPage from '../features/private/PrivateJoinPage'

const AdminPage = lazy(() => import('../features/admin/AdminPage'))

function Shell() {
  return (
    <div className="min-h-screen flex flex-col">
      <TopBar />
      <div className="flex-1">
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/match" element={<MatchPage />} />
          <Route path="/rooms" element={<RoomsPage />} />
          <Route path="/rooms/:roomId" element={<RoomPage />} />
          <Route path="/bottles" element={<BottlesPage />} />
          <Route path="/bottles/:conversationId" element={<BottleConversationPage />} />
          <Route path="/p/new" element={<PrivateNewPage />} />
          <Route path="/p/:token" element={<PrivateJoinPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </div>
      <VerificationModal />
    </div>
  )
}

/** 应用壳：启动时完成身份初始化，再建立 WS 连接；管理后台懒加载。 */
export default function App() {
  useEffect(() => {
    // 管理后台不触发匿名身份初始化，也不建立用户 WS 连接。
    if (typeof window !== 'undefined' && window.location.pathname.startsWith('/admin')) {
      return
    }
    let cancelled = false
    void (async () => {
      try {
        await useIdentityStore.getState().init()
      } catch {
        return // 初始化失败（如未登录环境）时不连接 WS
      }
      if (cancelled) return
      // 刷新后若已被封禁，直接展示被踢出提示。
      try {
        await getMe()
      } catch (e) {
        if ((e as { code?: string }).code === 'banned') {
          useUiStore.getState().setKicked('banned')
          return
        }
      }
      const ws = getWsClient()
      ws.on('open', () => useUiStore.getState().setConnected(true))
      ws.on('close', () => useUiStore.getState().setConnected(false))
      ws.on('kicked', (d) => {
        const reason = (d as { reason?: string })?.reason ?? 'banned'
        useUiStore.getState().setKicked(reason)
      })
      ws.on('error', (d) => {
        const code = (d as { code?: string })?.code
        if (!code) return
        useUiStore.getState().setToast(errorMessage(code))
        if (code === 'verification_required') useUiStore.getState().openVerification()
      })
      ws.connect()
    })()

    const offReset = onIdentityReset(() => {
      useConversationsStore.getState().clearAll()
      usePresenceStore.getState().clearAll()
      const ws = getWsClient()
      ws.close()
      ws.connect()
    })

    return () => {
      cancelled = true
      offReset()
      getWsClient().close()
    }
  }, [])

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <Toast />
      <KickedOverlay />
      <Routes>
        <Route
          path="/admin/*"
          element={
            <Suspense fallback={<div className="p-8 text-slate-400">加载中…</div>}>
              <AdminPage />
            </Suspense>
          }
        />
        <Route path="/*" element={<Shell />} />
      </Routes>
    </div>
  )
}
