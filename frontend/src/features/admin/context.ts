import { createContext, useContext } from 'react'

/** 后台各页面共享的错误处理与登录态回调（由 AdminPage 下发）。 */
export interface AdminActions {
  /** 401 之外走 toast，401 由 AdminPage 统一退回登录页。 */
  onError: (e: unknown) => void
  /** 退出登录后回到登录页。 */
  onLogout: () => void
}

const fallback: AdminActions = { onError: () => undefined, onLogout: () => undefined }

export const AdminContext = createContext<AdminActions>(fallback)

/** 读取后台共享上下文（脱离 Provider 时退化为空实现，方便单测渲染单个页面）。 */
export function useAdmin(): AdminActions {
  return useContext(AdminContext)
}
