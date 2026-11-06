/** 统一 REST 请求封装：基于 fetch，同源 Cookie，JSON 收发，非 2xx 解析为 ApiError。 */

/** ApiError 表示后端返回的业务错误。 */
export class ApiError extends Error {
  code: string
  status: number

  constructor(code: string, message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

/** 通用请求方法。204 返回 undefined。 */
export async function apiRequest<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: 'same-origin',
    headers: {},
  }
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' }
    init.body = JSON.stringify(body)
  }

  const res = await fetch(path, init)
  if (res.status === 204) {
    return undefined as T
  }

  const text = await res.text()
  let data: unknown = undefined
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = undefined
    }
  }

  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string } } | undefined)?.error
    throw new ApiError(err?.code ?? 'internal', err?.message ?? '请求失败', res.status)
  }
  return data as T
}

// ---- 身份 ----

export interface IdentityInfo {
  nickname: string
  avatarSeed: string
  verified: boolean
}

export interface MeInfo extends IdentityInfo {
  verificationMode: 'off' | 'optional' | 'required'
}

export function postIdentity(): Promise<IdentityInfo> {
  return apiRequest<IdentityInfo>('POST', '/api/identity')
}

export function getMe(): Promise<MeInfo> {
  return apiRequest<MeInfo>('GET', '/api/identity/me')
}

export function resetIdentity(): Promise<IdentityInfo> {
  return apiRequest<IdentityInfo>('POST', '/api/identity/reset')
}

// ---- 房间 ----

export interface RoomInfo {
  id: string
  slug: string
  name: string
  description: string
  online: number
  maxMembers: number
}

export function listRooms(): Promise<RoomInfo[]> {
  return apiRequest<RoomInfo[]>('GET', '/api/rooms')
}

// ---- 会话消息 ----

export interface ChatMessage {
  id: number
  memberId: number
  nickname: string
  avatarSeed: string
  kind: string
  body: string
  createdAt: string
  burnAfterRead?: boolean
}

export function conversationMessages(
  conversationId: string,
  params: { before?: number; after?: number; limit?: number } = {},
): Promise<ChatMessage[]> {
  const q = new URLSearchParams()
  if (params.before !== undefined) q.set('before', String(params.before))
  if (params.after !== undefined) q.set('after', String(params.after))
  if (params.limit !== undefined) q.set('limit', String(params.limit))
  const qs = q.toString()
  return apiRequest<ChatMessage[]>('GET', `/api/conversations/${conversationId}/messages${qs ? `?${qs}` : ''}`)
}

export function roomMessages(roomId: string, params: { before?: number; limit?: number } = {}): Promise<ChatMessage[]> {
  const q = new URLSearchParams()
  if (params.before !== undefined) q.set('before', String(params.before))
  if (params.limit !== undefined) q.set('limit', String(params.limit))
  const qs = q.toString()
  return apiRequest<ChatMessage[]>('GET', `/api/rooms/${roomId}/messages${qs ? `?${qs}` : ''}`)
}

// ---- 举报 / 屏蔽 ----

export function createReport(req: {
  conversationId: string
  memberId: number
  messageIds: number[]
  reason: string
  note?: string
}): Promise<{ id: string }> {
  return apiRequest<{ id: string }>('POST', '/api/reports', req)
}

export function blockConversation(conversationId: string): Promise<void> {
  return apiRequest<void>('POST', `/api/conversations/${conversationId}/block`)
}

export function blockMember(conversationId: string, memberId: number): Promise<void> {
  return apiRequest<void>('POST', `/api/conversations/${conversationId}/members/${memberId}/block`)
}

// ---- 实名验证 ----

export function sendSMSCode(phone: string): Promise<void> {
  return apiRequest<void>('POST', '/api/verify/sms/send', { phone })
}

export function confirmSMSCode(phone: string, code: string): Promise<{ verified: boolean }> {
  return apiRequest<{ verified: boolean }>('POST', '/api/verify/sms/confirm', { phone, code })
}

// ---- 漂流瓶 ----

export interface PickedBottle {
  id: string
  content: string
  createdAt: string
}

export function postBottle(content: string): Promise<{ id: string }> {
  return apiRequest<{ id: string }>('POST', '/api/bottles', { content })
}

export function pickBottle(): Promise<PickedBottle> {
  return apiRequest<PickedBottle>('POST', '/api/bottles/pick')
}

export function throwBackBottle(id: string): Promise<void> {
  return apiRequest<void>('POST', `/api/bottles/${id}/throw-back`)
}

export function replyBottle(id: string, content: string): Promise<{ conversationId: string }> {
  return apiRequest<{ conversationId: string }>('POST', `/api/bottles/${id}/reply`, { content })
}

export interface MyBottle {
  id: string
  contentPreview: string
  createdAt: string
  replies: Array<{ conversationId: string; unread: number }>
}

export function myBottles(): Promise<MyBottle[]> {
  return apiRequest<MyBottle[]>('GET', '/api/bottles/mine')
}

export function reportBottle(id: string, req: { reason: string; note?: string }): Promise<{ id: string }> {
  return apiRequest<{ id: string }>('POST', `/api/bottles/${id}/report`, req)
}

// ---- 私密链接 ----

export interface CreatedLink {
  token: string
  conversationId: string
  expiresAt: string
}

export function createPrivateLink(req: {
  password?: string
  ttlHours: number
  burnAfterRead: boolean
}): Promise<CreatedLink> {
  return apiRequest<CreatedLink>('POST', '/api/private-links', req)
}

export type PrivateLinkStatus = 'active' | 'full' | 'expired' | 'destroyed'

export interface PrivateLinkInfo {
  needPassword: boolean
  burnAfterRead: boolean
  status: PrivateLinkStatus
}

export function getPrivateLink(token: string): Promise<PrivateLinkInfo> {
  return apiRequest<PrivateLinkInfo>('GET', `/api/private-links/${encodeURIComponent(token)}`)
}

export function joinPrivateLink(token: string, req: { password?: string }): Promise<{ conversationId: string }> {
  return apiRequest<{ conversationId: string }>(
    'POST',
    `/api/private-links/${encodeURIComponent(token)}/join`,
    req,
  )
}

export function destroyPrivateLink(token: string): Promise<void> {
  return apiRequest<void>('POST', `/api/private-links/${encodeURIComponent(token)}/destroy`)
}

// ---- 管理后台 ----

export function adminLogin(username: string, password: string): Promise<void> {
  return apiRequest<void>('POST', '/api/admin/login', { username, password })
}

export function adminLogout(): Promise<void> {
  return apiRequest<void>('POST', '/api/admin/logout')
}

export interface AdminStats {
  online: number
  activeConversations: number
  messages24h: number
  openReports: number
}

export function adminStats(): Promise<AdminStats> {
  return apiRequest<AdminStats>('GET', '/api/admin/stats')
}

export interface AdminReportSummary {
  id: string
  reportedIdentityId: string
  conversationId?: string | null
  reason: string
  note?: string | null
  status: string
  createdAt: string
}

export function adminReports(params: {
  status?: string
  cursor?: string
  limit?: number
} = {}): Promise<{ reports: AdminReportSummary[]; nextCursor?: string | null }> {
  const q = new URLSearchParams()
  if (params.status) q.set('status', params.status)
  if (params.cursor) q.set('cursor', params.cursor)
  if (params.limit) q.set('limit', String(params.limit))
  const qs = q.toString()
  return apiRequest('GET', `/api/admin/reports${qs ? `?${qs}` : ''}`)
}

export interface AdminReportDetail extends AdminReportSummary {
  snapshot: unknown
  reported: {
    nickname: string
    createdIp: string
    lastIp: string
    phoneMasked: string
    reportCount: number
  }
  activeBans: AdminBan[]
}

export function adminReport(id: string): Promise<AdminReportDetail> {
  return apiRequest<AdminReportDetail>('GET', `/api/admin/reports/${id}`)
}

export function adminResolveReport(
  id: string,
  body: { action: 'dismiss' | 'ban'; ban?: { subjects: string[]; durationHours: number | null; reason: string } },
): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/reports/${id}/resolve`, body)
}

export interface AdminBan {
  id: string
  subjectType: string
  subjectValue: string
  reason: string
  expiresAt?: string | null
  active: boolean
}

export function adminBans(params: {
  active?: boolean
  cursor?: string
  limit?: number
} = {}): Promise<{ bans: AdminBan[]; nextCursor?: string | null }> {
  const q = new URLSearchParams()
  if (params.active !== undefined) q.set('active', String(params.active))
  if (params.cursor) q.set('cursor', params.cursor)
  if (params.limit) q.set('limit', String(params.limit))
  const qs = q.toString()
  return apiRequest('GET', `/api/admin/bans${qs ? `?${qs}` : ''}`)
}

export function adminCreateBan(body: {
  subjects: Array<{ type: string; value: string }>
  durationHours: number | null
  reason: string
}): Promise<{ bans: AdminBan[] }> {
  return apiRequest<{ bans: AdminBan[] }>('POST', '/api/admin/bans', body)
}

export function adminRevokeBan(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/admin/bans/${id}`)
}

export interface AdminAuditLog {
  id: number
  adminId: string
  action: string
  target?: string | null
  detail: unknown
  createdAt: string
}

export function adminAuditLogs(params: {
  cursor?: string
  limit?: number
} = {}): Promise<{ logs: AdminAuditLog[]; nextCursor?: number | null }> {
  const q = new URLSearchParams()
  if (params.cursor) q.set('cursor', params.cursor)
  if (params.limit) q.set('limit', String(params.limit))
  const qs = q.toString()
  return apiRequest('GET', `/api/admin/audit-logs${qs ? `?${qs}` : ''}`)
}

// ---- 会话详情 ----

export interface ConversationPeer {
  memberId: number
  nickname: string
  avatarSeed: string
}

export interface ConversationDetail {
  id: string
  kind: string
  burnAfterRead: boolean
  endedAt?: string | null
  endReason?: string | null
  myMemberId: number
  peer?: ConversationPeer
}

export function getConversation(id: string): Promise<ConversationDetail> {
  return apiRequest<ConversationDetail>('GET', `/api/conversations/${id}`)
}
