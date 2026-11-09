/**
 * 管理后台的枚举值 → 中文文案与色调映射。
 * 全部是纯函数：已知值返回中文与色调，未知值原样返回并兜底为 neutral。
 */

/** 徽标色调，对应 ui.tsx 中 StatusBadge 的配色。 */
export type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger'

/** 一条「文案 + 色调」映射结果。 */
export interface Labeled {
  label: string
  tone: Tone
}

function mapLookup<K extends string>(
  table: Record<K, Labeled>,
  key: string,
  fallbackTone: Tone = 'neutral',
): Labeled {
  return (table as Record<string, Labeled>)[key] ?? { label: key, tone: fallbackTone }
}

/** 举报原因（与用户端 ReportDialog 的选项保持一致）。 */
export const REPORT_REASON_OPTIONS = [
  { value: 'spam', label: '广告' },
  { value: 'harassment', label: '骚扰' },
  { value: 'porn', label: '色情' },
  { value: 'illegal', label: '违法' },
  { value: 'other', label: '其他' },
] as const

const REPORT_REASONS: Record<string, Labeled> = {
  spam: { label: '广告', tone: 'warning' },
  harassment: { label: '骚扰', tone: 'danger' },
  porn: { label: '色情', tone: 'danger' },
  illegal: { label: '违法', tone: 'danger' },
  other: { label: '其他', tone: 'neutral' },
}

/** 举报原因 → 「中文 + 色调」。 */
export function reportReason(reason: string): Labeled {
  return mapLookup(REPORT_REASONS, reason)
}

const REPORT_STATUSES: Record<string, Labeled> = {
  open: { label: '待处理', tone: 'warning' },
  resolved: { label: '已处理', tone: 'success' },
}

/** 举报状态 → 「中文 + 色调」。 */
export function reportStatus(status: string): Labeled {
  return mapLookup(REPORT_STATUSES, status)
}

const CONTENT_STATUSES: Record<string, Labeled> = {
  pending: { label: '待审核', tone: 'warning' },
  approved: { label: '已通过', tone: 'success' },
  rejected: { label: '已驳回', tone: 'danger' },
  removed: { label: '已下架', tone: 'neutral' },
}

/** 广播/作品状态 → 「中文 + 色调」。 */
export function contentStatus(status: string): Labeled {
  return mapLookup(CONTENT_STATUSES, status)
}

const BAN_SUBJECTS: Record<string, string> = {
  identity: '匿名身份',
  ip: 'IP',
  account: '实名账号',
}

/** 封禁主体类型 → 中文。 */
export function banSubject(type: string): string {
  return BAN_SUBJECTS[type] ?? type
}

const AUDIT_ACTIONS: Record<string, string> = {
  login_success: '登录成功',
  login_failed: '登录失败',
  logout: '退出登录',
  create_ban: '创建封禁',
  revoke_ban: '撤销封禁',
  resolve_report: '处理举报',
  create_announcement: '发布公告',
  delete_announcement: '删除公告',
  approve_broadcast: '通过广播',
  reject_broadcast: '驳回广播',
  remove_broadcast: '下架广播',
  approve_work: '通过作品',
  reject_work: '驳回作品',
  remove_work: '下架作品',
  create_activation_batch: '生成激活码批次',
  revoke_activation_batch: '撤销激活码批次',
  'bot.create': '新建 Bot',
  'bot.update': '修改 Bot',
  'bot.delete': '删除 Bot',
  'bot.settings.update': '修改 Bot 设置',
  'bot.usage.reset': '重置 Bot 用量',
  'bot.card.create': '新建角色卡',
  'bot.card.update': '修改角色卡',
  'bot.card.delete': '删除角色卡',
  'media.upload': '上传媒体',
  'media.item.update': '修改媒体',
  'media.item.delete': '删除媒体',
  'media.folder.create': '新建媒体文件夹',
  'media.folder.rename': '重命名媒体文件夹',
  'media.folder.delete': '删除媒体文件夹',
}

/** 审计动作 → 中文，未知值原样显示。 */
export function auditAction(action: string): string {
  return AUDIT_ACTIONS[action] ?? action
}

/** 时间格式化：空值显示「—」，非法时间原样显示。 */
export function formatTime(iso?: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('zh-CN', { hour12: false })
}

/** 只保留时分秒，用于「更新于 HH:mm:ss」。 */
export function formatClockTime(date: Date): string {
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}
