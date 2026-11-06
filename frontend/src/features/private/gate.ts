/** 私密链接状态 → 页面分支映射（纯函数）。 */
import type { PrivateLinkStatus } from '../../lib/api/client'

export type PrivateGate = 'destroyed' | 'expired' | 'full' | 'password' | 'join'

export function privateGate(status: PrivateLinkStatus, needPassword: boolean): PrivateGate {
  if (status === 'destroyed') return 'destroyed'
  if (status === 'expired') return 'expired'
  if (status === 'full') return 'full'
  return needPassword ? 'password' : 'join'
}

export const PRIVATE_GATE_TEXT: Record<PrivateGate, string> = {
  destroyed: '会话已被销毁',
  expired: '链接已过期',
  full: '该链接已满',
  password: '请输入访问密码',
  join: '加入私密聊天',
}
