import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Marquee } from '../../components/Marquee'
import { currentBroadcasts } from '../../lib/api/client'
import type { Broadcast } from '../../lib/api/client'
import { useIdentityStore } from '../../stores/identity'

/**
 * 首页广播横幅（how.md 2.2）：单行矮横幅，宽度跟随资料卡容器（最大 720px）。
 * 有广播时用 Marquee 横向滚动展示全部内容，没有时显示占位小字；
 * 右侧链接是首页进入 /vip 的唯一入口，任何状态下都保留。
 */
export function BroadcastBanner() {
  const [list, setList] = useState<Broadcast[]>([])
  const initialized = useIdentityStore((s) => s.initialized)

  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    currentBroadcasts()
      .then((b) => {
        if (!cancelled) setList(b)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [initialized])

  const hasBroadcasts = list.length > 0

  return (
    <div
      data-testid="broadcast-banner"
      className="flex h-10 items-center gap-2 rounded-sm border border-lobby-line bg-lobby-card px-3 text-sm"
    >
      <span aria-hidden className="shrink-0 text-base leading-none">
        📣
      </span>
      <div className="min-w-0 flex-1">
        {hasBroadcasts ? (
          <Marquee
            items={list.map((b) => b.content)}
            className="h-8 bg-transparent px-0 text-lobby-ink"
          />
        ) : (
          <span className="block truncate text-xs text-lobby-sub">还没有广播</span>
        )}
      </div>
      <Link
        to="/vip"
        className="shrink-0 rounded-sm px-2 py-1 text-xs font-medium text-brand transition duration-150 ease-out hover:bg-brand/10"
      >
        {hasBroadcasts ? '发布广播' : '了解广播卡'}
      </Link>
    </div>
  )
}
