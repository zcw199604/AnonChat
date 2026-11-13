import { useNavigate } from 'react-router-dom'
import { useIdentityStore } from '../../stores/identity'
import { BroadcastBanner } from './BroadcastBanner'
import { ProfileCard } from './ProfileCard'

/**
 * 首页（how.md 2.2）：容器最大宽度 1200px。
 * 产品只保留「匿名匹配」，因此首页主体是资料+匹配卡（居中限宽 720px）。
 * 广播以窄横幅形式保留在资料卡上方；品牌行、话题房间与匿名动态均已从首页移除
 * （品牌名改由 TopBar 渲染，/vip、/rooms 等页面仍可直接访问）。
 */
export default function HomePage() {
  const nav = useNavigate()
  const nickname = useIdentityStore((s) => s.nickname)

  function goMatch() {
    nav('/match?auto=1')
  }

  return (
    <main className="relative mx-auto max-w-[1200px] px-4 py-8">
      <div className="grid grid-cols-12 gap-4 md:gap-5">
        {/* 广播窄横幅 + 资料/匹配大卡（居中限宽；移动端占满宽度） */}
        <div className="col-span-12 mx-auto flex w-full max-w-[720px] flex-col gap-3">
          <BroadcastBanner />
          <ProfileCard onMatch={goMatch} nickname={nickname} />
        </div>
      </div>
    </main>
  )
}
