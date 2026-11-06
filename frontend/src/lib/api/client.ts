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

export type Gender = 'male' | 'female' | 'secret'

export interface ProfileInfo {
  gender: Gender
  age: number | null
  province: string | null
  provincePublic: boolean
  tags: string[]
  cleanMode: boolean
}

export interface MatchPrefs {
  gender: 'any' | 'male' | 'female'
  sameProvince: boolean
}

export interface VipInfo {
  active: boolean
  until: string | null
}

export interface MeInfo extends IdentityInfo {
  verificationMode: 'off' | 'optional' | 'required'
  publicCode: string
  profile: ProfileInfo
  matchPrefs: MatchPrefs
  vip: VipInfo
  broadcastCards: number
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

/** 部分更新资料与匹配偏好（how.md 3.5 PATCH /api/identity/profile）。 */
export interface ProfilePatch {
  gender?: Gender
  age?: number | null
  province?: string | null
  provincePublic?: boolean
  tags?: string[]
  cleanMode?: boolean
  matchPrefs?: Partial<MatchPrefs>
}

export function patchProfile(patch: ProfilePatch): Promise<MeInfo> {
  return apiRequest<MeInfo>('PATCH', '/api/identity/profile', patch)
}

/** 随机昵称。 */
export function shuffleNickname(): Promise<MeInfo> {
  return apiRequest<MeInfo>('POST', '/api/identity/nickname/shuffle')
}

/** 自定义昵称（2–12 字，服务端做违禁词检查）。 */
export function setNickname(nickname: string): Promise<MeInfo> {
  return apiRequest<MeInfo>('PUT', '/api/identity/nickname', { nickname })
}

// ---- VIP 与广播卡 ----

export function redeemVip(code: string): Promise<MeInfo> {
  return apiRequest<MeInfo>('POST', '/api/vip/redeem', { code })
}

// ---- 统计 / 公告 / 广播 / 动态 ----

export interface OnlineStats {
  online: number
  matching: number
  inRooms: number
}

export function onlineStats(): Promise<OnlineStats> {
  return apiRequest<OnlineStats>('GET', '/api/stats/online')
}

export interface Announcement {
  id: string
  content: string
  createdAt: string
}

export function listAnnouncements(): Promise<Announcement[]> {
  return apiRequest<Announcement[]>('GET', '/api/announcements')
}

export type BroadcastStatus = 'pending' | 'approved' | 'rejected' | 'removed'

export interface Broadcast {
  id: string
  content: string
  status: BroadcastStatus
  rejectReason?: string | null
  showUntil?: string | null
  createdAt: string
}

export function currentBroadcasts(): Promise<Broadcast[]> {
  return apiRequest<Broadcast[]>('GET', '/api/broadcasts/current')
}

export function postBroadcast(content: string): Promise<{ id: string }> {
  return apiRequest<{ id: string }>('POST', '/api/broadcasts', { content })
}

export function myBroadcasts(): Promise<Broadcast[]> {
  return apiRequest<Broadcast[]>('GET', '/api/broadcasts/mine')
}

export interface FeedItem {
  type: 'announcement' | 'broadcast' | 'work'
  id: string
  text: string
  at: string
}

export function getFeed(): Promise<FeedItem[]> {
  return apiRequest<FeedItem[]>('GET', '/api/feed')
}

// ---- 房间 ----

export interface RoomInfo {
  id: string
  slug: string
  name: string
  description: string
  emoji: string
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

// ---- 故事书 ----

export interface WaitingStory {
  id: string
  title: string
  opening: string
  tags: string[]
  roleA: string
  roleB: string
  openRole: string
  expiresAt: string
  createdAt: string
}

export interface StoryDetail {
  id: string
  title: string
  opening: string
  tags: string[]
  roleA: string
  roleB: string
  hostRole: 'a' | 'b'
  status: 'waiting' | 'active' | 'ended' | 'expired' | 'removed'
  valid: boolean | null
  hostConsent: boolean
  guestConsent: boolean
  conversationId?: string | null
  myRole: 'host' | 'guest' | null
  expiresAt: string
  createdAt: string
}

export function listWaitingStories(before?: string): Promise<WaitingStory[]> {
  const q = before ? `?status=waiting&before=${encodeURIComponent(before)}` : '?status=waiting'
  return apiRequest<WaitingStory[]>('GET', `/api/stories${q}`)
}

export function createStory(body: {
  title: string
  opening: string
  tags: string[]
  roleA: string
  roleB: string
  hostRole: 'a' | 'b'
}): Promise<StoryDetail> {
  return apiRequest<StoryDetail>('POST', '/api/stories', body)
}

export function getStory(id: string): Promise<StoryDetail> {
  return apiRequest<StoryDetail>('GET', `/api/stories/${id}`)
}

export function joinStory(id: string): Promise<{ conversationId: string }> {
  return apiRequest<{ conversationId: string }>('POST', `/api/stories/${id}/join`)
}

export function endStory(id: string): Promise<StoryDetail> {
  return apiRequest<StoryDetail>('POST', `/api/stories/${id}/end`)
}

export function cancelStory(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/stories/${id}`)
}

export function consentStory(id: string, agree: boolean): Promise<StoryDetail> {
  return apiRequest<StoryDetail>('POST', `/api/stories/${id}/consent`, { agree })
}

export interface WorkListItem {
  id: string
  title: string
  opening: string
  roleA: string
  roleB: string
  tags: string[]
  likeCount: number
  at: string
}

export interface WorkLine {
  role: string
  text: string
  ts: number
}

export interface WorkDetail {
  id: string
  title: string
  opening: string
  tags: string[]
  roleA: string
  roleB: string
  lines: WorkLine[]
  status: 'pending' | 'approved' | 'rejected' | 'removed'
  likeCount: number
  liked: boolean
  mine: 'host' | 'guest' | null
  createdAt: string
}

export function listWorks(before?: string): Promise<WorkListItem[]> {
  const q = before ? `?before=${encodeURIComponent(before)}` : ''
  return apiRequest<WorkListItem[]>('GET', `/api/works${q}`)
}

export function getWork(id: string): Promise<WorkDetail> {
  return apiRequest<WorkDetail>('GET', `/api/works/${id}`)
}

export function likeWork(id: string): Promise<{ likeCount: number }> {
  return apiRequest<{ likeCount: number }>('POST', `/api/works/${id}/like`)
}

export function unlikeWork(id: string): Promise<{ likeCount: number }> {
  return apiRequest<{ likeCount: number }>('DELETE', `/api/works/${id}/like`)
}

export function deleteWork(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/works/${id}`)
}

export function withdrawWork(id: string): Promise<void> {
  return apiRequest<void>('POST', `/api/works/${id}/withdraw`)
}

export function reportWork(id: string, req: { reason: string; note?: string }): Promise<{ id: string }> {
  return apiRequest<{ id: string }>('POST', `/api/works/${id}/report`, req)
}

// ---- 管理后台：公告 / 广播 / 激活码 / 作品 / 身份 ----

export interface AdminAnnouncement {
  id: string
  content: string
  createdAt: string
  expiresAt?: string
}

export function adminAnnouncements(): Promise<{ announcements: AdminAnnouncement[] }> {
  return apiRequest('GET', '/api/admin/announcements')
}

export function adminCreateAnnouncement(req: {
  content: string
  expiresAt?: string | null
}): Promise<AdminAnnouncement> {
  return apiRequest('POST', '/api/admin/announcements', req)
}

export function adminDeleteAnnouncement(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/api/admin/announcements/${id}`)
}

export interface AdminBroadcast extends Broadcast {
  publicCode: string
}

export function adminBroadcasts(status = 'pending'): Promise<{ broadcasts: AdminBroadcast[] }> {
  return apiRequest('GET', `/api/admin/broadcasts?status=${encodeURIComponent(status)}`)
}

export function adminApproveBroadcast(id: string): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/broadcasts/${id}/approve`)
}

export function adminRejectBroadcast(id: string, reason: string): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/broadcasts/${id}/reject`, { reason })
}

export function adminRemoveBroadcast(id: string): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/broadcasts/${id}/remove`)
}

export interface ActivationBatch {
  id: string
  label: string
  vipDays: number
  broadcastCards: number
  codeCount: number
  expiresAt: string
  createdAt: string
  total: number
  redeemed: number
  revoked: number
}

export function adminActivationBatches(): Promise<{ batches: ActivationBatch[] }> {
  return apiRequest('GET', '/api/admin/activation-batches')
}

export function adminCreateActivationBatch(req: {
  label: string
  vipDays: number
  broadcastCards: number
  count: number
  expiresAt: string
}): Promise<{ batch: ActivationBatch; codes: string[] }> {
  return apiRequest('POST', '/api/admin/activation-batches', req)
}

export function adminRevokeActivationBatch(id: string): Promise<{ revoked: number }> {
  return apiRequest('POST', `/api/admin/activation-batches/${id}/revoke`)
}

export interface AdminWork {
  id: string
  title: string
  status: string
  likeCount: number
  rejectReason?: string | null
  createdAt: string
}

export function adminWorks(status = 'pending'): Promise<{ works: AdminWork[] }> {
  return apiRequest('GET', `/api/admin/works?status=${encodeURIComponent(status)}`)
}

export function adminApproveWork(id: string): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/works/${id}/approve`)
}

export function adminRejectWork(id: string, reason: string): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/works/${id}/reject`, { reason })
}

export function adminRemoveWork(id: string): Promise<void> {
  return apiRequest<void>('POST', `/api/admin/works/${id}/remove`)
}

export interface AdminIdentityLookup {
  identityId: string
  publicCode: string
  createdAt: string
  vipActive: boolean
  banned: boolean
  summary: {
    nickname: string
    createdIp: string
    lastIp: string
    phoneMasked: string
    reportCount: number
  }
}

export function adminLookupIdentity(code: string): Promise<AdminIdentityLookup> {
  return apiRequest<AdminIdentityLookup>('GET', `/api/admin/identities/by-code/${encodeURIComponent(code)}`)
}

export function adminBroadcastReport(id: string, req: { reason: string; note?: string }): Promise<{ id: string }> {
  return apiRequest<{ id: string }>('POST', `/api/broadcasts/${id}/report`, req)
}
