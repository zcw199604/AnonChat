# 领域语言（术语表）

> 以代码为准，最后同步于 V1。

| 推荐名称 | 代码命名 | 含义 | 禁用叫法 |
|---|---|---|---|
| 匿名身份 | `Identity` | 由 Cookie 令牌标识的免注册用户 | 用户、账号（账号专指 VerifiedAccount） |
| 实名账号 | `VerifiedAccount` | 已验证手机号的后台实名记录 | — |
| 会话 | `Conversation` | 所有聊天形态共用的消息容器，kind ∈ match、room、bottle、private | 聊天、频道 |
| 成员别名 | `memberId` | 某人在一个会话内的匿名序号（从 1 递增），对外只暴露该值 | userId |
| 随机匹配 | `match` | A 形态 | — |
| 聊天室 | `Room` | B 形态，每个房间对应一个长期会话 | 群、频道 |
| 漂流瓶 | `Bottle` | C 形态，投放、捞取、回复 | 树洞帖 |
| 私密链接 | `PrivateLink` | D 形态的邀请链接，会话最多两人 | 房间链接 |
| 阅后即焚 | `burnAfterRead` / `burned_at` | 已读后对用户隐藏，服务端保留期内仍保存 | 自毁、删除 |
| 屏蔽 | `Block` | 用户之间互相不可见、不再匹配（单向记录、双向生效） | 拉黑（仅界面文案） |
| 封禁 | `Ban` | 管理员对身份、IP 或实名账号的处罚（可临时/永久） | — |

## 实施中新增的概念

| 名称 | 代码 | 含义 |
|---|---|---|
| 发送管道 | `conversation.HandleSend` | msg.send 的校验→限流→过滤→落库→ack→广播流程 |
| 幂等键 | `(sender_identity_id, client_msg_id)` | 保证客户端重发不重复落库 |
| 发件箱 | `WsClient` outbox | 前端消息状态 sending→sent/failed，重试复用 clientMsgId |
| 可恢复会话 | `hello.resumable` | 重连时可恢复的会话 ID（目前为进行中的匹配会话） |
| 断线宽限 | `MATCH_GRACE` | 匹配会话一方断线后保留会话的时长（默认 30s） |
| 配对放宽 | `MATCH_RELAX_AFTER` | 排队超过该时长后放宽“最近聊过”限制（默认 15s，屏蔽永不放宽） |
| 在线人数推送合并 | `room.presence` | 同一房间每秒最多推送一次在线人数 |
| 敏感词级别 | `block` / `mask` | 命中即拦截 / 命中后打码 |
| 限流倍率 | `RATE_LIMIT_MULTIPLIER` | 所有限流上限的倍率；生产必须为 1 |
| E2E 管理员种子 | `cmd/e2eseed` | 仅 APP_ENV=test 下从 stdin 读密码创建管理员 |
| 健康检查子命令 | `server healthcheck` | distroless 环境下的容器健康检查 |
