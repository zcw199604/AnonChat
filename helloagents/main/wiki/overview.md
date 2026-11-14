# 架构与模块概览

> 以代码为准。最后同步：后台可配置 bot 开关与模型接入（202611062351 / B7）。

## 架构图

```mermaid
flowchart LR
    B[浏览器 React SPA] -- HTTPS --> C[Caddy web]
    C -- /api/* --> S[Go backend]
    C -- /ws --> S
    subgraph S[Go backend]
      API[api 路由] --> ID[identity/verify]
      API --> CONV[conversation]
      WS[realtime 网关] --> CONV
      CONV --> MOD[moderation]
      MATCH[match] --> CONV
      ROOM[room] --> CONV
      BOT[bottle] --> CONV
      PRIV[privatechat] --> CONV
      ADM[admin] --> MOD
      JOBS[jobs] --> CONV
      PROF[profile] --> ID
      VIP[vip] --> ID
      FEED[feed] --> ADMIN[admin]
      STORY[story] --> CONV
      BOTAI[bot 引擎] --> CONV
      BOTAI --> LLM[(OpenAI 兼容 API)]
      MEDIA[media 媒体库] --> FS[(MEDIA_ROOT 磁盘)]
    end
    S --> PG[(PostgreSQL)]
```

## 模块职责（`backend/internal`）

| 模块 | 职责 |
|---|---|
| `config` | 从环境变量加载并校验配置（含生产约束） |
| `platform/db` | pgxpool 连接池 |
| `platform/httpx` | JSON 响应与统一错误码、RequestID/日志/Recover/安全头、Origin 校验、ClientIP 解析 |
| `store` | sqlc 生成代码 |
| `identity` | 匿名身份签发/校验/重置；`__Host-anon` Cookie；昵称/头像种子；`RequireIdentity` |
| `verify` | 手机号验证码；`SMSProvider`(console)；AES-256-GCM + HMAC；`RequireVerified` |
| `realtime` | `GET /ws` 握手（Origin→身份→封禁→升级）；Hub/Broker/Presence 内存实现；协议编解码；Router；心跳/慢客户端/优雅关闭 |
| `conversation` | 会话/成员/消息仓储；发送管道；阅后即焚；历史分页；会话详情；屏蔽会话/成员 |
| `match` | 随机匹配队列、配对规则、断线宽限、`match.*` 处理 |
| `room` | 官方聊天室列表/历史、`room.join/leave`、在线人数合并推送 |
| `bottle` | 漂流瓶投放/捞取/扔回/回复/我的/举报 |
| `privatechat` | 私密链接创建/查询/加入/销毁，过期扫描 |
| `moderation` | 限流（固定窗口/令牌桶 + Limits 配置）、敏感词 AC、屏蔽、举报快照、`BanService`（含缓存放踢） |
| `admin` | 管理员会话、举报处理、封禁管理、统计、审计；`RequireAdmin`；IP 白名单 |
| `jobs` | 保留期清理（消息/会话/瓶子/链接/验证码/日志/举报/管理员会话/身份/故事/广播） |
| `profile` | 资料常量（`consts.json` + go:embed，与前端 `src/lib/profileConsts.ts` 由测试保证一致）、校验、部分更新、随机/自定义昵称、`PublicProfile` 公开视图 |
| `vip` | 激活码生成（16 位 Crockford Base32，80 bit，展示 `XXXX-XXXX-XXXX-XXXX`）、规范化、sha256 哈希、兑换（事务 + `FOR UPDATE`，并发安全）、批次创建/撤销、`IsVIP` |
| `feed` | 公告列表与管理、广播提交（事务扣卡）/审核/退卡、匿名动态聚合、在线统计（10 秒缓存） |
| `story` | 故事发起/认领（`FOR UPDATE` 防并发）/结束判定/收录同意与作品生成、作品列表与阅读/点赞/删除/撤回/举报、等待超时 |
| `bot` | AI 机器人：OpenAI 兼容 LLM 客户端（前缀缓存、429/5xx 重试 1 次）、提示词组装（四段式、历史按 30 条整块截断）、`ParseReply`、限额与节奏纯函数、`Engine`（`conversation.Observer` + 匹配兜底 `match.BotProvider`：防抖回复、开场、漂流瓶循环、用量记账）、角色卡/bot 后台接口 |
| `media` | 媒体库：`MEDIA_ROOT` 文件存储（临时文件 + rename、`http.DetectContentType` 嗅探 MIME）、后台文件夹/条目管理与上传、用户侧 `GET /api/media/{id}`（会话内可见性 + Range） |

## 匿名代号与资料可见性

- 1v1 场景（match / bottle / private）同样显示真实昵称与头像：消息 `nickname`/`avatarSeed` 由服务端下发真实值，`match.found.peer` 也带 `nickname`/`avatarSeed`；前端不再用 `codename(conversationId, memberId)` 计算「匿名·XXXX」，自己仍显示「我」。
- 房间聊天继续显示昵称（群聊需要称呼）；故事剧场显示角色名。
- 资料只在 `GET /api/identity/me`（自己）与 `match.found.peer`（对方的公开资料）中出现；省份仅在 `province_public=true` 时输出，性别为 `secret` 时输出 `null`。

## 前端设计系统（`frontend/src`）

- `index.css` 的 `@theme` 定义 token（品牌紫 `#8B7CF6`、辅助粉 `#E88BC4`、`--shadow-soft`、三档圆角、Inter 字体、fade-up/breathe 动画）；`html[data-theme=dark]` 把大厅 token 切换为聊天页夜间 token。
- `app/useRouteTheme.ts`：路由 → 主题判定（聊天类固定暗色、大厅类跟随 `localStorage['anonchat.theme']`），并驱动 `App.tsx` 中两层固定背景的 300ms 透明度过渡。
- 公共组件：`GradientAvatar`(保留 `Avatar` 导出名)、`Card`、`GlassPanel`、`Bubble`、`TypingDots`、`Toggle`、`Segmented`、`RangeSlider`、`TagPicker`、`Sheet`、`Marquee`、`Drawer`、`VipLock`。
- PWA：`public/manifest.webmanifest` + `public/icon.svg` + `theme-color`，不注册 Service Worker。

## 消息发送管道（`msg.send`）

`realtime.Router` 分发 → `conversation.HandleSend`：
1. 校验载荷（conversationId、clientMsgId、kind=text、正文去空白后 1..1000 rune）；
2. 成员校验（未离开）→ 会话未结束；
3. 限流钩子（身份令牌桶；房间内另有 1 秒/房间间隔）；
4. 内容过滤钩子（敏感词 block/mask）；
5. 幂等落库 `INSERT ... ON CONFLICT (sender_identity_id, client_msg_id) DO NOTHING`；
6. 先回 `ack`，再 `Broker.Broadcast` 给会话订阅连接（排除屏蔽了发送者的身份）。

## AI 机器人（bot）要点

- bot 是一个普通匿名身份（`bots.identity_id`），消息走同一条 `conversation.SendAs` 落库与广播路径；
  任何用户侧接口、事件与字段都不体现 bot（`msg.new`、`match.found`、`GET /api/media/{id}` 都只依赖会话关系）。
- 引擎**始终创建并运行**：`convSvc.AddObserver(engine)` 让它收到会话事件，
  `matchSvc.SetBotFallback(engine)` 让它成为排队兜底的 provider，`StartBackground` 里随 `Run(ctx)` 启动。
  是否真的工作由 `bot_settings` 里的生效配置决定（开关 + Base URL + 模型 + API Key 齐全才可用）。
- 生效配置（`SettingsCache`）在内存中缓存：启动时加载，后台保存后 `engine.Invalidate()` 立即刷新，
  另外每 30 秒自动刷新；读取只走内存（匹配 goroutine 不访问数据库），`FallbackAfter()` 也读缓存。
- `SETTINGS_ENC_KEY` 存在时，后台保存的 API Key 以 AES-256-GCM 密文入库（`platform/secretbox`），
  接口只返回是否设置与末 4 位掩码；`LLM_API_KEY` 只作为数据库未设置时的回退。
- **key 与 Base URL 绑定**：数据库 key 只在当前 Base URL 等于保存时的地址时生效，环境变量 key 只在当前
  Base URL 等于 `LLM_BASE_URL` 时生效；改地址后旧 key 自动失效并提示重填，「测试连接」换地址时必须同时填新 key，
  避免 key 被引到任意地址。
- 环境变量只用于**首次初始化** `bot_settings`（表为空时写一次），之后以数据库为准。
- 回复节奏：先读消息延迟（`readDelay`）→ 广播 typing → 按字数 sleep → 再确认会话未结束 → `SendAs`
  （`SkipRateLimit=true`）；用户连发会合并成一次回复（最长等 2×`reply_delay_max_ms`）。
- 停止条件：bot 被停用、或任一限额（总量/窗口/单对话）触发时，匹配会话以 `peer_left` 结束，
  漂流瓶会话保持沉默。
- 提示词四段：bot 级 system（角色卡 + 排序后的媒体标签 + 输出格式）→ 对话级 system（会话类型、对方公开资料、
  瓶子原文）→ 历史（`[图片:标签]`/`[视频:标签]`，同角色合并）→ 结尾 system（当前时间 + 本轮指令）。
  前缀只在角色卡/媒体标签变化时改变，便于命中服务商的前缀缓存；缓存键 `anonchat-bot-<botID>`。
- 用量：每次调用写 `bot_usage`（prompt/completion/cached/replies）并在同一事务里累加 `bots.used_*`；
  后台可见总量进度、窗口用量、缓存命中率与进行中匹配数。

## 实时状态（V1 内存 / V2 可换 Redis）

`realtime.Realtime` 组合 `Presence` + `Broker` + `Subscriber` + `ConnStats` 接口，V1 由 `*Hub` 实现。
业务模块（room/match/bottle/privatechat）只依赖该接口，便于 V2 用 Redis 实现替换。
