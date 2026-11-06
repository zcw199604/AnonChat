# 架构与模块概览

> 以代码为准。最后同步：界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书（202611062140）。

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

## 匿名代号与资料可见性

- 1v1 场景（match / bottle / private）不显示昵称：消息 `nickname` 由服务端置空，前端用 `codename(conversationId, memberId)` 计算「匿名·XXXX」；自己显示「我」。
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

## 实时状态（V1 内存 / V2 可换 Redis）

`realtime.Realtime` 组合 `Presence` + `Broker` + `Subscriber` + `ConnStats` 接口，V1 由 `*Hub` 实现。
业务模块（room/match/bottle/privatechat）只依赖该接口，便于 V2 用 Redis 实现替换。
