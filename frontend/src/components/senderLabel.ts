/**
 * 消息发送者标签：1v1 场景（match / bottle / private）按产品决定显示对方的真实昵称，
 * 自己在这些场景里显示为「我」，昵称缺失时回退「对方」。
 */
export function senderLabel(nickname: string, self: boolean): string {
  if (self) return '我'
  return nickname.trim() !== '' ? nickname : '对方'
}
