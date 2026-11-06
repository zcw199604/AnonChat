import { Link } from 'react-router-dom'
import { useUiStore } from '../stores/ui'
import { ResetIdentityConfirm } from '../components/ResetIdentityConfirm'

/** 抽屉条目（how.md 2.1）。尚未接通的路由点击后提示「即将开放」。 */
const ITEMS: Array<{ to: string; label: string; icon: string; ready: boolean }> = [
  { to: '/', label: '首页', icon: '🏠', ready: true },
  { to: '/match', label: '随机匹配', icon: '🎲', ready: true },
  { to: '/rooms', label: '话题房间', icon: '💬', ready: true },
  { to: '/bottles', label: '匿名信', icon: '✉️', ready: true },
  { to: '/p/new', label: '私密链接', icon: '🔗', ready: true },
  { to: '/stories', label: '故事书（公测）', icon: '📖', ready: true },
  { to: '/vip', label: 'VIP 与激活码', icon: '✨', ready: true },
]

interface DrawerNavProps {
  onNavigate: () => void
}

/** 抽屉导航：路由条目 + 换个身份（二次确认）。 */
export function DrawerNav({ onNavigate }: DrawerNavProps) {
  const setToast = useUiStore((s) => s.setToast)

  return (
    <nav className="space-y-1">
      <div className="mb-3 px-2 text-xs tracking-[0.2em] text-lobby-sub">菜单</div>
      {ITEMS.map((item) =>
        item.ready ? (
          <Link
            key={item.to}
            to={item.to}
            onClick={onNavigate}
            className="flex items-center gap-3 rounded-sm px-3 py-2 text-sm text-lobby-ink transition duration-150 ease-out hover:bg-lobby-bg"
          >
            <span aria-hidden>{item.icon}</span>
            {item.label}
          </Link>
        ) : (
          <button
            key={item.to}
            type="button"
            onClick={() => {
              setToast('即将开放')
            }}
            className="flex w-full items-center gap-3 rounded-sm px-3 py-2 text-left text-sm text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg"
          >
            <span aria-hidden>{item.icon}</span>
            {item.label}
          </button>
        ),
      )}

      <div className="mt-3">
        <ResetIdentityConfirm
          className="flex w-full items-center gap-3 rounded-sm px-3 py-2 text-left text-sm text-lobby-ink transition duration-150 ease-out hover:bg-lobby-bg"
          onDone={onNavigate}
        />
      </div>

    </nav>
  )
}
