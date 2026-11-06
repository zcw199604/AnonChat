import { Link, useNavigate } from 'react-router-dom'
import { useUiStore } from '../../stores/ui'
import { useIdentityStore } from '../../stores/identity'
import { BroadcastCard } from './BroadcastCard'
import { ProfileCard } from './ProfileCard'
import { OnlineCard } from './OnlineCard'
import { FeedCard } from './FeedCard'
import { RoomGrid } from './RoomGrid'
import { useFloatingPill } from './useFloatingPill'

interface QuickLink {
  to: string
  label: string
  icon: string
  desc: string
  ready: boolean
}

const QUICK_LINKS: QuickLink[] = [
  { to: '/bottles', label: '匿名信', icon: '✉️', desc: '投一封信，等有缘人捞起', ready: true },
  { to: '/p/new', label: '私密链接', icon: '🔗', desc: '生成链接，两人私聊', ready: true },
  { to: '/stories', label: '故事书 · 公测', icon: '📖', desc: '两个人演一个故事', ready: true },
]

/** 首页（how.md 2.2）：Bento Grid，容器最大宽度 1200px，12 列。 */
export default function HomePage() {
  const nav = useNavigate()
  const connected = useUiStore((s) => s.connected)
  const setToast = useUiStore((s) => s.setToast)
  const nickname = useIdentityStore((s) => s.nickname)

  // 桌面端浮动胶囊只在视口 ≥1440px（容器两侧留白足够）时出现；
  // 更窄的视口改用快捷入口卡，避免遮挡匿名动态卡（审查意见 V2）。
  const wide = useFloatingPill()

  function goMatch() {
    nav('/match?auto=1')
  }

  return (
    <main className="relative mx-auto max-w-[1200px] px-4 py-8">
      <div className="grid grid-cols-12 gap-4 md:gap-5">
        {/* 广播卡 */}
        <div className="order-1 col-span-12 md:order-none">
          <BroadcastCard />
        </div>

        {/* 品牌行 */}
        <div className="order-2 col-span-12 flex items-center justify-between gap-3 px-1 md:order-none">
          <div>
            <h1 className="text-2xl font-bold tracking-tight text-lobby-ink">AnonChat 匿名聊天</h1>
            <p className="text-[10px] tracking-[0.25em] text-lobby-sub">ANONYMOUS CHAT</p>
          </div>
          <span className="flex items-center gap-2 text-xs text-lobby-sub">
            {connected ? (
              <>
                <span data-testid="ws-connected" aria-label="已连接" className="h-2 w-2 rounded-full bg-brand" />
                已连接
              </>
            ) : (
              <>
                <span className="h-2 w-2 rounded-full bg-lobby-line" />
                连接中…
              </>
            )}
          </span>
        </div>

        {/* 资料 + 匹配大卡 */}
        <div className="order-3 col-span-12 md:order-1 md:col-span-8 md:row-span-2">
          <ProfileCard onMatch={goMatch} nickname={nickname} />
        </div>

        {/* 右列：在线人数（按内容自适应）→ 匿名动态（填满剩余高度）。
            移动端用 display:contents 拆开，保持「在线人数 → 房间 → 快捷入口 → 匿名动态」的顺序。 */}
        <div
          data-testid="home-side-column"
          className="contents md:order-2 md:col-span-4 md:row-span-2 md:flex md:flex-col md:gap-5"
        >
          <div className="order-4 col-span-12 md:order-none">
            <OnlineCard />
          </div>
          <div data-testid="feed-card-wrapper" className="order-7 col-span-12 md:order-none md:min-h-0 md:flex-1">
            <FeedCard />
          </div>
        </div>
        <div className="order-5 col-span-12 md:order-4 md:col-span-8">
          <RoomGrid />
        </div>

        {/* 快捷入口 */}
        <section className="order-6 col-span-12 space-y-3 md:order-5 md:col-span-4" aria-label="快捷入口">
          <h2 className="text-sm font-semibold text-lobby-ink">快捷入口</h2>
          {QUICK_LINKS.map((q) =>
            q.ready ? (
              <Link
                key={q.to}
                to={q.to}
                className="flex items-center gap-3 rounded-card border border-lobby-line bg-lobby-card px-4 py-3 shadow-soft transition duration-150 ease-out hover:-translate-y-0.5"
              >
                <span aria-hidden>{q.icon}</span>
                <span className="flex-1">
                  <span className="block text-sm font-medium text-lobby-ink">{q.label} →</span>
                  <span className="block text-xs text-lobby-sub">{q.desc}</span>
                </span>
              </Link>
            ) : (
              <button
                key={q.to}
                type="button"
                onClick={() => setToast('即将开放')}
                className="flex w-full items-center gap-3 rounded-card border border-lobby-line bg-lobby-card px-4 py-3 text-left shadow-soft transition duration-150 ease-out hover:-translate-y-0.5"
              >
                <span aria-hidden>{q.icon}</span>
                <span className="flex-1">
                  <span className="block text-sm font-medium text-lobby-ink">{q.label} →</span>
                  <span className="block text-xs text-lobby-sub">{q.desc}</span>
                </span>
              </button>
            ),
          )}
        </section>
      </div>

      {wide && (
        <div data-testid="floating-entries" className="fixed bottom-6 right-6 z-20 flex flex-col gap-2">
          <Link
            to="/bottles"
            className="rounded-full border border-lobby-line bg-lobby-card px-4 py-2 text-sm text-lobby-ink shadow-soft transition duration-150 ease-out hover:-translate-y-0.5"
          >
            ✉️ 匿名信
          </Link>
          <Link
            to="/stories"
            className="rounded-full border border-lobby-line bg-lobby-card px-4 py-2 text-sm text-lobby-ink shadow-soft transition duration-150 ease-out hover:-translate-y-0.5"
          >
            📖 故事书 · 公测
          </Link>
        </div>
      )}
    </main>
  )
}
