/** 错误码 → 面向用户的中文提示（不暴露技术细节）。 */

const GENERIC = '操作失败，请稍后重试'

const MESSAGES: Record<string, string> = {
  // how.md 错误码
  unauthorized: '登录状态已失效，请刷新页面',
  verification_required: '需要先完成实名验证',
  banned: '你已被封禁，暂时无法使用',
  rate_limited: '操作太频繁了，请稍后再试',
  content_blocked: '内容包含违规信息，请修改后再发送',
  not_member: '你不在该会话中',
  conversation_ended: '会话已结束',
  room_full: '房间人数已满',
  link_full: '该链接已满员',
  link_expired: '链接已过期',
  bad_password: '密码错误',
  empty_sea: '海里暂时没有瓶子，晚点再来看看',
  quota_exceeded: '次数已用完，请明天再来',
  invalid_request: '请求有误，请检查后重试',
  internal: '服务器开小差了，请稍后重试',
  // V2 错误码
  muted: '你已被禁言',
  captcha_required: '请先完成人机验证',
  media_rejected: '图片未通过审核',
  // 本批安全加固新增
  forbidden_origin: '来源不被允许',
}

/** 把错误码映射为中文提示；未知错误码返回通用提示。 */
export function errorMessage(code: string | undefined | null): string {
  if (!code) return GENERIC
  return MESSAGES[code] ?? GENERIC
}

/** 全部已知错误码（供测试与排查）。 */
export const KNOWN_ERROR_CODES = Object.keys(MESSAGES)
