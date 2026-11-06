# 领域语言（术语表）

> 以代码为准。最后同步：界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书（202611062140）。

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
| 匿名编号 | `public_code` / `publicCode` | 10 位 Crockford Base32 的身份对外编号，可复制给客服；换身份时重新生成 | 用户 ID、账号 |
| 匿名代号 | `codename` | 1v1 会话内按 `sha256(conversationId+':'+memberId)` 前 4 位生成的「匿名·XXXX」，同一会话内稳定、跨会话不可关联 | 昵称（1v1 场景不展示） |
| 资料 | `Profile` | 性别、年龄、省份与是否公开、标签、清流模式 | 个人信息（界面上称「资料」） |
| 匹配偏好 | `matchPrefs` | VIP 权益：按性别筛选、同省匹配；非 VIP 由服务端忽略 | 高级筛选 |
| 清流模式 | `cleanMode` | 只与同样开启清流模式的用户配对 | 纯净模式 |
| VIP | `vip_until` / `isVIP` | 由激活码兑换的权益，`vip_until > now` 即生效 | 会员（界面文案用 VIP） |
| 激活码 | `ActivationCode` | 站外分发的 16 位激活码，库中只存 sha256；批次由管理员生成 | 兑换码、卡密 |
| 广播 | `Broadcast` | 消耗广播卡提交、人工审核后展示 24 小时的公开内容 | 公告（公告由管理员发布） |
| 公告 | `Announcement` | 管理员发布、在首页跑马灯滚动的通知 | 广播 |
| 匿名动态 | `feed` | 公告、广播、作品聚合而成的时间线 | 广场、动态圈 |
| 故事书 | `Story` | 双人角色扮演故事，等待开演 → 演出中 → 结束，可判定是否有效 | 剧本杀、语C |
| 作品 | `StoryWork` | 有效场次 + 双方同意后生成、经人工审核公开的正文快照（只含角色名与正文） | 帖子、文章 |

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
| 有效场次 | `stories.valid` | 时长 ≥ `STORY_MIN_DURATION`（默认 5 分钟）且双方各 ≥5 条非系统消息 | 完成的场次 |
| 收录同意 | `host_consent` / `guest_consent` | 双方对「把这场故事收录为作品」的同意状态，任一方撤回即下架 | 授权 |
| E2E 激活码种子 | `cmd/e2eseed --out` | 仅 APP_ENV=test：创建管理员与一批可用激活码，明文写入 `frontend/e2e/.seed.json` | — |
| 视觉截图脚本 | `e2e/visual.capture.ts` | 桌面/移动端首页与匹配等待页截图，输出到 `frontend/e2e/__screenshots__/`（不入库） | — |
