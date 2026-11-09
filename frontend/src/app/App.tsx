import { lazy, Suspense, useEffect, useState } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { useIdentityStore, onIdentityReset } from '../stores/identity'
import { useConversationsStore } from '../stores/conversations'
import { usePresenceStore } from '../stores/presence'
import { useUiStore } from '../stores/ui'
import { getWsClient } from '../lib/ws'
import { errorMessage } from '../lib/api/errorMessages'
import { getMe } from '../lib/api/client'
import { TopBar } from './TopBar'
import { AnnouncementBar } from './AnnouncementBar'
import { isChatRoute, syncLobbyThemeFromStorage, useRouteTheme } from './useRouteTheme'
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
import VipPage from '../features/vip/VipPage'
import StoriesPage from '../features/stories/StoriesPage'
import StoryNewPage from '../features/stories/StoryNewPage'
import StoryPlayPage from '../features/stories/StoryPlayPage'
import WorkPage from '../features/stories/WorkPage'

const AdminPage = lazy(() => import('../features/admin/AdminPage'))

/**
 * 两层固定定位背景：浅色层始终存在，暗色层按路由切换 opacity（300ms ease-out）。
 * 从大厅进入聊天页时由浅变暗。
 */
function BackgroundLayers() {
  const { theme } = useRouteTheme()
  return (
    <>
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 -z-10 bg-lobby-bg"
        data-testid="bg-light"
      />
      <div
        aria-hidden
        className="theme-layer pointer-events-none fixed inset-0 -z-10"
        data-testid="bg-dark"
        style={{
          background:
            'linear-gradient(160deg, var(--color-night-from), var(--color-night-to))',
          opacity: theme === 'dark' ? 1 : 0,
        }}
      />
    </>
  )
}

function Shell() {
  const { pathname } = useLocation()
  const inPrivateConversation = useUiStore((s) => s.inPrivateConversation)
  const chat = isChatRoute(pathname, inPrivateConversation)

  return (
    <div className="relative flex min-h-screen flex-col">
      {!chat && (
        <>
          <TopBar />
          <AnnouncementBar />
        </>
      )}
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
          <Route path="/vip" element={<VipPage />} />
          <Route path="/stories" element={<StoriesPage />} />
          <Route path="/stories/new" element={<StoryNewPage />} />
          <Route path="/stories/:storyId/play" element={<StoryPlayPage />} />
          <Route path="/works/:workId" element={<WorkPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </div>
      <VerificationModal />
    </div>
  )
}

function IdentityInitError({ message }: { message: string }) {
  return (
    <main
      role="alert"
      className="flex min-h-screen flex-col items-center justify-center gap-4 px-6 text-center"
    >
      <p className="text-lobby-sub">{message}</p>
      <button
        type="button"
        className="rounded-full bg-brand px-5 py-2 font-medium text-white transition duration-150 ease-out"
        onClick={() => location.reload()}
      >
        刷新页面
      </button>
    </main>
  )
}

/** 应用壳：启动时完成身份初始化，再建立 WS 连接；管理后台懒加载。 */
export default function App() {
  const [initError, setInitError] = useState<string | null>(null)
  useEffect(() => {
    // 管理后台不触发匿名身份初始化，也不建立用户 WS 连接。
    if (typeof window !== 'undefined' && window.location.pathname.startsWith('/admin')) {
      return
    }
    syncLobbyThemeFromStorage()
    let cancelled = false
    void (async () => {
      try {
        await useIdentityStore.getState().init()
      } catch (e) {
        if (!cancelled) {
          const code = (e as { code?: string }).code
          setInitError(code === 'rate_limited' ? '身份创建过于频繁，请稍后再试' : code === 'banned' ? '当前网络已被封禁，请等待封禁解除后重试' : '身份初始化失败，请刷新页面重试')
        }
        return
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
        useUiStore.getState().setToast(code === 'rate_limited' ? (d as { message?: string }).message || errorMessage(code) : errorMessage(code))
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
    <div className="relative min-h-screen text-lobby-ink">
      <BackgroundLayers />
      <Toast />
      <KickedOverlay />
      <Routes>
        <Route
          path="/admin/*"
          element={
            <Suspense fallback={<div className="p-8 text-lobby-sub">加载中…</div>}>
              <AdminPage />
            </Suspense>
          }
        />
        <Route
          path="/*"
          element={initError ? <IdentityInitError message={initError} /> : <Shell />}
        />
      </Routes>
    </div>
  )
}
