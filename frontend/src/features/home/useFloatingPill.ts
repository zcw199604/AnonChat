import { useEffect, useState } from 'react'

/**
 * 桌面端浮动胶囊的最小视口宽度（审查意见 V2）：
 * 首页容器最大宽度 1200px，只有宽度 ≥1440px 时两侧才有足够留白，
 * 否则浮动胶囊会盖住右列的匿名动态卡。更窄时改用快捷入口卡。
 */
export const FLOATING_PILL_MIN_WIDTH = 1440

/** 纯函数：给定视口宽度判断是否显示浮动胶囊。 */
export function showFloatingPill(viewportWidth: number): boolean {
  return viewportWidth >= FLOATING_PILL_MIN_WIDTH
}

/** 订阅视口宽度，返回当前是否显示浮动胶囊。 */
export function useFloatingPill(): boolean {
  const [show, setShow] = useState(() => (typeof window === 'undefined' ? false : showFloatingPill(window.innerWidth)))

  useEffect(() => {
    const update = () => setShow(showFloatingPill(window.innerWidth))
    update()
    window.addEventListener('resize', update)
    return () => window.removeEventListener('resize', update)
  }, [])

  return show
}
