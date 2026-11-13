import { useEffect, useRef, useState } from 'react'
import { Drawer } from '../components/Drawer'
import { Sheet } from '../components/Sheet'
import { useUiStore } from '../stores/ui'
import { useIdentityStore } from '../stores/identity'
import { useRouteTheme } from './useRouteTheme'
import { DrawerNav } from './DrawerNav'

/**
 * 顶栏（how.md 2.1）：☰ 打开抽屉，中间是品牌名（首页 h1），
 * 右侧是连接状态圆点、添加到主屏幕、明暗切换。
 * 公告跑马灯由 AnnouncementBar 渲染在顶栏下方（U4 接通数据）。
 */
export function TopBar() {
  const connected = useUiStore((s) => s.connected)
  const drawerOpen = useUiStore((s) => s.drawerOpen)
  const setDrawerOpen = useUiStore((s) => s.setDrawerOpen)
  const { lobbyPref, toggleLobbyTheme } = useRouteTheme()
  const [installOpen, setInstallOpen] = useState(false)
  const publicCode = useIdentityStore((s) => s.publicCode)
  // 捕获 beforeinstallprompt：支持时直接调用原生安装，否则弹出说明 Sheet。
  const installEvent = useRef<{ prompt: () => Promise<void> } | null>(null)

  useEffect(() => {
    const onPrompt = (e: Event) => {
      e.preventDefault()
      installEvent.current = e as unknown as { prompt: () => Promise<void> }
    }
    window.addEventListener('beforeinstallprompt', onPrompt)
    return () => window.removeEventListener('beforeinstallprompt', onPrompt)
  }, [])

  function handleInstall() {
    const ev = installEvent.current
    if (ev) {
      void ev.prompt()
      installEvent.current = null
      return
    }
    setInstallOpen(true)
  }

  const dark = lobbyPref === 'dark'

  return (
    <>
      <header className="sticky top-0 z-30 border-b border-lobby-line bg-lobby-card/80 backdrop-blur-[20px]">
        <div className="mx-auto flex h-16 max-w-[1200px] items-center justify-between px-4">
          <div className="flex items-center gap-3">
            <button
              type="button"
              aria-label="菜单"
              onClick={() => setDrawerOpen(true)}
              className="rounded-sm px-2 py-1 text-lg text-lobby-ink transition duration-150 ease-out hover:bg-lobby-bg"
            >
              ☰
            </button>
          </div>

          <div className="text-center">
            <h1 className="text-[15px] font-bold tracking-tight text-lobby-ink">AnonChat 匿名聊天</h1>
            <div className="text-[10px] tracking-[0.2em] text-lobby-sub">ANONYMOUS CHAT</div>
          </div>

          <div className="flex items-center gap-1">
            {/* E2E 就绪信号：仅在 WS 已连接时出现 data-testid="ws-connected"。 */}
            <span className="flex items-center px-1">
              {connected ? (
                <span data-testid="ws-connected" aria-label="已连接" className="h-2 w-2 rounded-full bg-brand" />
              ) : (
                <span aria-label="连接中" className="h-2 w-2 rounded-full bg-lobby-line" />
              )}
            </span>
            <button
              type="button"
              aria-label="添加到主屏幕"
              onClick={handleInstall}
              className="rounded-sm px-2 py-1 text-base transition duration-150 ease-out hover:bg-lobby-bg"
            >
              📱
            </button>
            <button
              type="button"
              aria-label="切换明暗主题"
              onClick={toggleLobbyTheme}
              className="rounded-sm px-2 py-1 text-base transition duration-150 ease-out hover:bg-lobby-bg"
            >
              {dark ? '☀' : '🌙'}
            </button>
          </div>
        </div>
      </header>

      <Drawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        footer={
          <div className="space-y-1 text-xs text-lobby-sub">
            <div>匿名编号：{publicCode || '—'}</div>
            <div className="flex items-center gap-2">
              <span className={`h-2 w-2 rounded-full ${connected ? 'bg-brand' : 'bg-lobby-line'}`} />
              {connected ? '已连接' : '连接中…'}
            </div>
          </div>
        }
      >
        <DrawerNav onNavigate={() => setDrawerOpen(false)} />
      </Drawer>

      <Sheet open={installOpen} title="添加到主屏幕" onClose={() => setInstallOpen(false)}>
        <div className="space-y-3 text-sm text-lobby-sub">
          <p>把 AnonChat 添加到主屏幕，下次可以从桌面直接打开。</p>
          <div>
            <div className="mb-1 font-medium text-lobby-ink">iOS（Safari）</div>
            <p>点击底部「分享」按钮 → 选择「添加到主屏幕」。</p>
          </div>
          <div>
            <div className="mb-1 font-medium text-lobby-ink">Android（Chrome）</div>
            <p>点击右上角菜单 → 选择「安装应用」或「添加到主屏幕」。</p>
          </div>
        </div>
      </Sheet>
    </>
  )
}
