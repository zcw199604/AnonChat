# 接口协议（REST + WebSocket）

> 以代码为准，最后同步于 V1。REST 路由见 `backend/internal/api/routes.go`；WS 类型/错误码见 `backend/internal/realtime/protocol.go`；错误码常量另见 `backend/internal/platform/httpx/response.go`。

## 通用

- 所有 `/api/*`（除 `POST /api/identity`）需身份 Cookie `__Host-anon`；聊天类接口在 `VERIFICATION_MODE=required` 时还需已实名。
- 管理后台使用独立 Cookie `__Host-admin`（8 小时），身份 Cookie 对其无效；`ADMIN_IP_ALLOWLIST` 可限制访问 IP。
- 错误统一格式：`{"error":{"code":"<snake_case>","message":"<中文>"}}`。
- 修改类请求（POST/PUT/PATCH/DELETE）校验 `Origin` 是否在 `ALLOWED_ORIGINS`，否则 `forbidden_origin`。
- REST 请求体上限 64KB；WS 单帧上限 16KB。

## REST 接口

### 运维
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 存活检查 |
| GET | `/readyz` | 就绪检查（Ping 数据库） |
| GET | `/ws` | WebSocket 握手（Origin→身份→封禁→升级） |

### 身份
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/identity` | 无有效 Cookie 时创建身份，有则返回现有；下发 Cookie |
| GET | `/api/identity/me` | `{nickname,avatarSeed,verified,verificationMode}` |
| POST | `/api/identity/reset` | 换身份（旧令牌失效），下发新 Cookie |

### 实名验证
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/verify/sms/send` | `{phone}` → 204（`VERIFICATION_MODE=off` 时不注册） |
| POST | `/api/verify/sms/confirm` | `{phone,code}` → `{verified:true}` |

### 会话与消息
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/conversations` | 我参与进行中的会话（含未读数，不含 room） |
| GET | `/api/conversations/{id}` | 会话详情 `{id,kind,burnAfterRead,endedAt,endReason,myMemberId,peer?}` |
| GET | `/api/conversations/{id}/messages` | 历史分页 `before/after/limit` |
| POST | `/api/conversations/{id}/block` | 1v1 屏蔽对方并结束会话 |
| POST | `/api/conversations/{id}/members/{memberId}/block` | 房间屏蔽成员 |
| POST | `/api/reports` | 举报 `{conversationId,memberId,messageIds[],reason,note?}` → `{id}` |

### 聊天室
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/rooms` | 未归档房间 + 在线人数 |
| GET | `/api/rooms/{id}/messages` | 房间历史（7 天窗口、过滤屏蔽） |

### 漂流瓶
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/bottles` | `{content}` → `{id}` |
| POST | `/api/bottles/pick` | `{id,content,createdAt}` 或 404 `empty_sea` |
| POST | `/api/bottles/{id}/throw-back` | 204 |
| POST | `/api/bottles/{id}/reply` | `{content}` → `{conversationId}` |
| GET | `/api/bottles/mine` | `[{id,contentPreview,createdAt,replies:[{conversationId,unread}]}]` |
| POST | `/api/bottles/{id}/report` | `{reason,note?}` → `{id}`（仅当前持有者） |

### 私密链接
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/private-links` | `{password?,ttlHours,burnAfterRead}` → `{token,conversationId,expiresAt}` |
| GET | `/api/private-links/{token}` | `{needPassword,burnAfterRead,status}`（status: active\|full\|expired\|destroyed） |
| POST | `/api/private-links/{token}/join` | `{password?}` → `{conversationId}` |
| POST | `/api/private-links/{token}/destroy` | 204（仅成员） |

### 管理后台（`__Host-admin`）
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/admin/login` | `{username,password}` |
| POST | `/api/admin/logout` | 退出 |
| GET | `/api/admin/reports` | 举报列表（status 筛选、游标） |
| GET | `/api/admin/reports/{id}` | 举报详情（快照、被举报者摘要、生效封禁） |
| POST | `/api/admin/reports/{id}/resolve` | `{action:"dismiss"\|"ban", ban?}` |
| GET | `/api/admin/bans` | 封禁列表（active 筛选、游标） |
| POST | `/api/admin/bans` | `{subjects:[{type,value}],durationHours?,reason}` |
| DELETE | `/api/admin/bans/{id}` | 撤销封禁 |
| GET | `/api/admin/stats` | `{online,activeConversations,messages24h,openReports}` |
| GET | `/api/admin/audit-logs` | 审计日志（游标） |

## WebSocket 协议

连接 `GET /ws`（Cookie 鉴权、校验 Origin）。信封：`{"t","id","d"}`，服务端下行加 `ts`（毫秒）。

客户端 → 服务端：`ping`、`match.start`、`match.cancel`、`match.next`、`match.leave`、`room.join`、`room.leave`、`conv.subscribe`、`msg.send`、`msg.read`、`typing`。

服务端 → 客户端：`pong`、`ack`、`error`、`hello`（`{nickname,avatarSeed,resumable[]}`）、`match.waiting`、`match.found`、`match.ended`、`conv.ended`、`room.presence`、`msg.new`、`msg.burned`、`typing`、`bottle.reply`、`kicked`。

要点：
- 客户端每 25 秒发 `ping`；服务端 60 秒收不到任何帧则断开；单帧 >16KB 断开（1009）；写缓冲满被视为慢客户端。
- `msg.send`：服务端先回 `ack {messageId,createdAt}` 再广播 `msg.new`；重复 `clientMsgId` 幂等返回同一 `messageId` 且不重复广播。
- `match.found` 的 peer 仅含对方 `memberId/nickname/avatarSeed`；`match.ended`/`conv.ended` 的 reason ∈ `peer_left|peer_skipped|destroyed|expired|blocked`。
- `kicked` 的 reason ∈ `banned|identity_reset`，发送后服务端关闭连接。
- `hello.resumable` 为可恢复的会话 ID 列表（匹配页据此恢复）。

## 错误码

`unauthorized`、`verification_required`、`banned`、`rate_limited`、`content_blocked`、`not_member`、`conversation_ended`、`room_full`、`link_full`、`link_expired`、`bad_password`、`empty_sea`、`quota_exceeded`、`invalid_request`、`internal`、`forbidden_origin`（本批安全加固新增，用于 Origin 校验）；V2 预留 `muted`、`captcha_required`、`media_rejected`。
