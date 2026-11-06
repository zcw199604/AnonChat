# 接口协议（REST + WebSocket）

> 以代码为准。REST 路由见 `backend/internal/api/routes.go`（本文件的接口表与其逐条对应）；WS 类型见 `backend/internal/realtime/protocol.go`；错误码常量见 `backend/internal/platform/httpx/response.go`。
> 最后同步：界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书（202611062140）。

## 通用

- 所有 `/api/*`（除 `POST /api/identity`）需身份 Cookie `__Host-anon`；聊天类接口在 `VERIFICATION_MODE=required` 时还需已实名。
- 管理后台使用独立 Cookie `__Host-admin`（8 小时），身份 Cookie 对其无效；`ADMIN_IP_ALLOWLIST` 可限制访问 IP。
- 错误统一格式：`{"error":{"code":"<snake_case>","message":"<中文>"}}`。
- 修改类请求（POST/PUT/PATCH/DELETE）校验 `Origin` 是否在 `ALLOWED_ORIGINS`，否则 `forbidden_origin`。
- REST 请求体上限 64KB；WS 单帧上限 16KB。
- 新接口的中间件分工：只读公开接口（统计、动态、公告、作品列表与阅读）用 `protected`；发布公开 UGC 的接口（`POST /api/broadcasts`、`POST /api/stories`）与参与聊天的接口（故事认领、故事结束）用 `chatProtected`；管理后台接口一律 `admin.RequireAdmin`，且写操作写入 `admin_audit_logs`。

## REST 接口

### 运维
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 存活检查 |
| GET | `/readyz` | 就绪检查（Ping 数据库） |
| GET | `/ws` | WebSocket 握手（Origin→身份→封禁→升级） |

### 身份与资料
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/identity` | 无有效 Cookie 时创建身份，有则返回现有；下发 Cookie |
| GET | `/api/identity/me` | 增加 `publicCode`、`profile{gender,age,province,provincePublic,tags,cleanMode}`、`matchPrefs{gender,sameProvince}`、`vip{active,until}`、`broadcastCards` |
| PATCH | `/api/identity/profile` | 部分更新资料与 `matchPrefs`，返回完整 me；校验失败 `invalid_request`；非 VIP 设置 matchPrefs 返回 `vip_required` |
| POST | `/api/identity/nickname/shuffle` | 随机昵称（限流：每身份 10 次/分钟） |
| PUT | `/api/identity/nickname` | `{nickname}` 自定义昵称 2–12 字（限流：每身份 10 次/天；违禁词 `content_blocked`） |
| POST | `/api/identity/reset` | 换身份：继承 `vip_until` 与 `broadcast_cards`，资料重置，生成新匿名编号 |
| POST | `/api/verify/sms/send` | `{phone}` → 204（`VERIFICATION_MODE=off` 时不注册） |
| POST | `/api/verify/sms/confirm` | `{phone,code}` → `{verified:true}` |

### 会话与消息
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/conversations` | 我参与进行中的会话（含未读数，不含 room） |
| GET | `/api/conversations/{id}` | 会话详情 `{id,kind,burnAfterRead,endedAt,endReason,myMemberId,peer?}` |
| GET | `/api/conversations/{id}/messages` | 历史分页 `before/after/limit`；1v1 会话 `nickname` 恒为空串，故事会话 `nickname` 为角色名 |
| POST | `/api/conversations/{id}/block` | 1v1 屏蔽对方并结束会话 |
| POST | `/api/conversations/{id}/members/{memberId}/block` | 房间屏蔽成员 |
| POST | `/api/reports` | 举报 `{conversationId,memberId,messageIds[],reason,note?}` → `{id}` |

### 聊天室
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/rooms` | 未归档房间 + `emoji` + 在线人数 |
| GET | `/api/rooms/{id}/messages` | 房间历史（7 天窗口、过滤屏蔽） |

### 漂流瓶（前端称「匿名信」）
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

### VIP 与激活码
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/vip/redeem` | `{code}` → 完整 me。规范化：去空格/连字符、转大写、`O→0`、`I/L→1`。失败统一 `invalid_code`（不区分不存在/已用/已撤销/已过期）。限流：每身份 5 次/10 分钟、每 IP 20 次/10 分钟，超出 `rate_limited`。成功写 `access_logs(event=vip_redeem)` |

### 统计、公告、广播与匿名动态
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/stats/online` | `{online,matching,inRooms}`，服务端缓存 10 秒 |
| GET | `/api/announcements` | 当前有效公告（最多 5 条，按创建时间倒序） |
| GET | `/api/broadcasts/current` | 展示中的广播（approved 且 `show_until>now`，最多 3 条） |
| POST | `/api/broadcasts` | `chatProtected`（required 模式下需已实名）。`{content}`：先过违禁词（blocked→`content_blocked`，打码级用打码后的文本入库），再事务内扣 1 张广播卡（无卡 `no_broadcast_card`），状态 pending。限流：每身份 5 次/天 |
| GET | `/api/broadcasts/mine` | 自己提交的广播与审核状态（含 `rejectReason`） |
| POST | `/api/broadcasts/{id}/report` | `{reason,note?}` → 举报写入 `reports(target_type=broadcast)` |
| GET | `/api/feed` | 匿名动态：合并有效公告、展示中的广播、已通过作品（各取最新 10 条），按时间倒序，最多 20 条；每条 `{type:'announcement'\|'broadcast'\|'work', id, text, at}` |

### 故事书与作品
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/stories?status=waiting` | 等待开演列表（不含发起人信息），分页 `before`、`limit` |
| POST | `/api/stories` | `chatProtected`（required 模式下需已实名）。发起：`{title,opening,tags,roleA,roleB,hostRole}`；title/opening/roleA/roleB 过违禁词（blocked→`content_blocked`，打码级用打码后的文本）；tags 每项必须属于 profile 预设标签并去重，否则 `invalid_request`；同一身份同时最多 1 个 waiting/active（`quota_exceeded`）；`expires_at=now+24h` |
| GET | `/api/stories/{id}` | 详情：`myRole`、status、valid、双方同意状态；`conversationId` 仅参与者可见 |
| POST | `/api/stories/{id}/join` | 认领剩下的角色 → `{conversationId}`；创建 kind=story 会话；已被认领或双方存在任一方向拉黑时返回 `story_taken`；不能认领自己的故事 |
| POST | `/api/stories/{id}/end` | 任一参与者结束：valid = 时长≥`STORY_MIN_DURATION`（默认 5 分钟）且双方各 ≥5 条非系统消息；推送 `story.ended` |
| DELETE | `/api/stories/{id}` | 发起人在 waiting 状态取消 |
| POST | `/api/stories/{id}/consent` | `{agree}`：仅 valid 的故事可操作（否则 `story_not_valid`）；双方同意时事务内生成 `story_works(pending)` |
| GET | `/api/works` | 已通过作品（书架），分页 `before`、`limit` |
| GET | `/api/works/{id}` | 详情：approved 对所有人可见；pending/rejected 仅参与者；返回 `liked`、`mine` |
| POST | `/api/works/{id}/like` | 点赞（幂等），返回 `{likeCount}` |
| DELETE | `/api/works/{id}/like` | 取消点赞（幂等） |
| DELETE | `/api/works/{id}` | 发起人删除作品（状态改 removed） |
| POST | `/api/works/{id}/withdraw` | 参与者撤回同意（状态改 removed） |
| POST | `/api/works/{id}/report` | 举报作品（`reports.target_type=work`） |

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
| GET | `/api/admin/announcements` | 公告列表 |
| POST | `/api/admin/announcements` | `{content,expiresAt?}` → 公告（写审计 `create_announcement`） |
| DELETE | `/api/admin/announcements/{id}` | 软删除公告（写审计 `delete_announcement`） |
| GET | `/api/admin/broadcasts?status=` | 广播审核队列（含提交者 `publicCode`） |
| POST | `/api/admin/broadcasts/{id}/approve` | 通过：`show_until=now+24h`（写审计） |
| POST | `/api/admin/broadcasts/{id}/reject` | `{reason}`：驳回并事务内退还广播卡（写审计） |
| POST | `/api/admin/broadcasts/{id}/remove` | 下架（不退卡，写审计） |
| GET | `/api/admin/activation-batches` | 批次列表（含 total/redeemed/revoked 统计） |
| POST | `/api/admin/activation-batches` | 创建批次 `{label,vipDays,broadcastCards,count,expiresAt}`；响应中的 `codes` 明文只出现一次（写审计） |
| POST | `/api/admin/activation-batches/{id}/revoke` | 撤销批次内所有未兑换的码（写审计） |
| GET | `/api/admin/works?status=` | 作品审核队列 |
| POST | `/api/admin/works/{id}/approve` | 通过（写审计） |
| POST | `/api/admin/works/{id}/reject` | `{reason}` 驳回（写审计） |
| POST | `/api/admin/works/{id}/remove` | 下架（写审计） |
| GET | `/api/admin/identities/by-code/{code}` | 按匿名编号查身份：`identityId`、`publicCode`、`createdAt`、`vipActive`、`banned`、`activeBans`、摘要 |

## WebSocket 协议

连接 `GET /ws`（Cookie 鉴权、校验 Origin）。信封：`{"t","id","d"}`，服务端下行加 `ts`（毫秒）。

客户端 → 服务端：`ping`、`match.start`、`match.cancel`、`match.next`、`match.leave`、`room.join`、`room.leave`、`conv.subscribe`、`msg.send`、`msg.read`、`typing`。

服务端 → 客户端：`pong`、`ack`、`error`、`hello`、`match.waiting`、`match.found`、`match.ended`、`conv.ended`、`room.presence`、`msg.new`、`msg.burned`、`typing`、`bottle.reply`、`kicked`、`story.started`、`story.ended`、`story.consent`。

要点：
- 客户端每 25 秒发 `ping`；服务端 60 秒收不到任何帧则断开；单帧 >16KB 断开（1009）；写缓冲满被视为慢客户端。
- `msg.send`：服务端先回 `ack {messageId,createdAt}` 再广播 `msg.new`；重复 `clientMsgId` 幂等返回同一 `messageId` 且不重复广播。
- `msg.new.message.nickname`：房间会话为真实昵称；故事会话为角色名；1v1 会话（match/bottle/private）为空字符串——前端在 1v1 场景改用匿名代号 `匿名·XXXX`（`sha256(conversationId+':'+memberId)` 前 4 位，见 `frontend/src/lib/codename.ts`）。
- `match.found` 的负载：`{conversationId, peer:{memberId,gender,age,province,tags,cleanMode}}`（**不含** nickname 与 avatarSeed；gender 为 secret 时输出 null，province 仅在对方公开时输出）。
- `match.waiting` 的负载：`{filters:{cleanMode,gender,sameProvince}}`，即服务端最终生效的条件。
- `match.ended`/`conv.ended` 的 reason ∈ `peer_left|peer_skipped|destroyed|expired|blocked|ended`。
- `kicked` 的 reason ∈ `banned|identity_reset`，发送后服务端关闭连接。
- `story.started`：`{storyId, conversationId}`；`story.ended`：`{storyId, valid}`；`story.consent`：`{storyId, hostConsent, guestConsent, workId?}`。三个事件都不含任何身份标识。
- `hello.resumable` 为可恢复的会话 ID 列表（匹配页据此恢复）。

## 错误码

V1：`unauthorized`、`verification_required`、`banned`、`rate_limited`、`content_blocked`、`not_member`、`conversation_ended`、`room_full`、`link_full`、`link_expired`、`bad_password`、`empty_sea`、`quota_exceeded`、`invalid_request`、`internal`、`forbidden_origin`。

本包新增：`vip_required`(403)、`invalid_code`(400)、`no_broadcast_card`(409)、`story_taken`(409)、`story_not_valid`(409)、`not_found`(404)。

V2 预留：`muted`、`captcha_required`、`media_rejected`。

## 匹配兼容规则（`internal/match`）

兼容判定双向满足：① 未互相拉黑、最近聊过不在放宽窗口内；② `clean_mode` 相同；③ VIP 且 `match_gender≠any` 时要求对方 `gender` 精确匹配（保密性别不满足任何筛选）；④ VIP 且 `match_same_province` 时要求双方都设置且省份相同；⑤ 非 VIP 的 `match_gender`/`match_same_province` 一律视为 any/false；⑥ 硬条件（②–④）不随时间放宽。候选人选择：标签交集最大优先，交集相同选排队最久。
