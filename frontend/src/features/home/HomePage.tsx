import { useNavigate } from 'react-router-dom'
import { useUiStore } from '../../stores/ui'
import { useIdentityStore } from '../../stores/identity'
import { BroadcastCard } from './BroadcastCard'
import { ProfileCard } from './ProfileCard'

/**
 * 首页（how.md 2.2）：Bento Grid，容器最大宽度 1200px，12 列。
 * 产品只保留「匿名匹配」，因此布局为：广播卡 → 品牌行 → 资料+匹配卡（唯一主体，居中限宽）。
 * 话题房间与匿名动态已从首页移除（/rooms 等页面仍可直接访问）。
 */
export default function HomePage() {
  const nav = useNavigate()
  const connected = useUiStore((s) => s.connected)
  const nickname = useIdentityStore((s) => s.nickname)

  function goMatch() {
    nav('/match?auto=1')
  }

  return (
    <main className="relative mx-auto max-w-[1200px] px-4 py-8">
      <div className="grid grid-cols-12 gap-4 md:gap-5">
        {/* 广播卡 */}
        <div className="col-span-12">
          <BroadcastCard />
        </div>

        {/* 品牌行 */}
        <div className="col-span-12 flex items-center justify-between gap-3 px-1">
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

        {/* 资料 + 匹配大卡（唯一主体，居中限宽；移动端占满宽度） */}
        <div className="col-span-12 mx-auto w-full max-w-[720px]">
          <ProfileCard onMatch={goMatch} nickname={nickname} />
        </div>
      </div>
    </main>
  )
}
