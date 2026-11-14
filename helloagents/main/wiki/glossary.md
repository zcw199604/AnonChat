# 领域语言（术语表）

> 以代码为准。最后同步：外站匹配接入与用户媒体（202611131925）。

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
| 匿名代号 | `codename` | 1v1 会话内按 `sha256(conversationId+':'+memberId)` 前 4 位生成的「匿名·XXXX」；昵称改为透传真名后已停用，仅保留历史实现 | 昵称（1v1 现在显示真实昵称） |
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

## Bot 人设与记忆

适用模块：`bot`、`bot/persona`、后台管理与 `jobs`。来源：[Bot 人设包、双核驱动与长期记忆方案](../plan/202611092002_bot-persona-memory/how.md)。

| 推荐名称 | 代码命名 | 含义 |
|---|---|---|
| 心核 | `heart` / `heart_prompt` | 按角色人设生成对外回复 |
| 智核 | `reason` / `reason_prompt` | 用 JSON 文本协议规划回复，可调用内置工具；想法不向用户或日志公开 |
| 驱动模式 | `core_mode` / `coreMode` | `single` 单核；`dual` 先智核后心核，智核失败时本轮降级单核 |
| 人设包 | `.krp` | 含 TOML、心核/智核提示及表情的 ZIP；上传代码不执行 |
| 表情 | `expression` / `card_expressions` | 角色卡绑定的图片，走普通 image 消息，每轮最多一个，受会话媒体限额约束 |
| 世界书 | `lorebook` | 常驻或关键词触发的角色设定；触发只看最近六条对方文本及瓶子原文 |
| 长期记忆 | `memory` | 按 bot 与对方匿名身份隔离的画像和事件 |
| 画像 | `profile` | 六个固定 topic 下的可更新事实；与公开资料 Profile 区分 |
| 事件 | `event` / `gist` | 每对最多 50 条的简短时间线 |
| 好感度 | `affinity` | 每对身份独立的 -100 至 100 关系值 |
| 情绪 | `mood` | 标签与 0–10 强度，仅用于后台和提示词 |
| 跑偏检测 | `OOC guard` / `ooc_guard` | 检查 Markdown、设定泄露、客服腔和超长；不检测或改写 AI 身份表述 |

复制角色卡保留所有可编辑字段，来源记为手动，不复制表情媒体。

## 外站匹配与媒体

适用模块：`match`、`external`、`media`、`conversation` 与 `jobs`。来源：[外站匹配接入与用户媒体方案](../plan/202611131925_external-match-bridge/how.md)。

| 推荐名称 | 代码命名 | 含义 | 禁用叫法 |
|---|---|---|---|
| 外站 | `provider`（`chat2019` / `wanfeng`） | 接入的第三方匿名聊天站点 | 上游、线路（业务文档中） |
| 外站阶段 | `phaseExternal` | 本站等待之后、bot 兜底之前的外站匹配阶段 | — |
| 匹配代次 | `attempt` | 每轮外站匹配唯一编号；旧代次的开始、取消或结果不能影响新一轮 | — |
| 兜底阶段 | `phaseFallback` | 外站阶段结束后等待 bot 的阶段 | — |
| 外站会话 | `external_conversations` | 本站用户与外站对方组成的 `match` 会话 | 桥接会话 |
| 影子身份 | `external_peers.identity_id` / `peerIdent` | 同一外站用户在本站复用的匿名身份，用于消息管线与屏蔽 | 虚拟用户、外站 bot |
| 外站账号 | `external_bindings` | 本站匿名身份在某个外站的绑定账号；出站昵称优先用本站昵称，绑定昵称作为回退（含数字时） | — |
| Bridge | `external.Bridge` | 外站连接、帧翻译及消息/媒体中转模块 | — |
| 媒体来源 | `media_items.source` | `library` 后台媒体库、`user` 用户上传、`external` 外站下载 | — |
| 媒体归属 | `owner_identity_id` | 用户媒体的上传身份；library/external 来源为空。用户仅能发送自己的 `user` 媒体 | — |
| 内部媒体 token | 每个 link 登记的随机 token | 媒体转发的一次性引用；普通文本不能触发媒体读取 | 媒体 UUID（两者不同） |
