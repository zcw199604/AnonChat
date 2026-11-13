# P8 安全约束与测试覆盖核对

本轮只核对本方案；代码和测试名为检索依据。后端包路径 `external/`、`match/`、`media/`、`conversation/`、`jobs/` 均省略 `backend/internal/` 前缀，`migrations/` 和 `queries/` 省略 `backend/` 前缀。外站客户端/Bridge 逐场景表见 [p8-external-coverage.md](p8-external-coverage.md)。不连接真实外站，不执行 P11。P4/P6/P7 用户已批准的修正优先于 how.md 原始概括。

## how.md §0 与「安全与性能」

| 约束 | 代码位置 / 测试证据 | 结论 |
|---|---|---|
| 总开关默认关闭，关闭/未注册保持原匹配与 bot 兜底时机 | `migrations/00010_external_bridge.sql` 默认 false；`external/settings.go`；`matcher_external_test.go::TestExternalFallbackTimingAndHotDisable` disabled/unregistered；原 `matcher_fallback_test.go` | 通过。现有测试仅 bot 目标测试按用户授权修正时序，其他断言/辅助函数不变 |
| 匹配 goroutine 无 DB/网络、无 do() 重入 | `match/matcher.go::Run`、`process`、`tick`；`startExternal` 的调用带 go，Start/Cancel/建会话/兜底查询均在独立 goroutine；`TestExternalStartFailureDoesNotBlockMatcher` | 通过。`startExternal` 内存在 do，但调用点在独立 goroutine，不能仅按文本命中判断违规 |
| 本站等待/外站超时/兜底计时仅匹配器负责 | `matcher.go::tryExternal`、`tryFallback`；Bridge 只含空闲 60 秒关闭、冷却、操作 IO deadline；`TestExternalStagesAndOnlineThreshold` | 通过，空闲/冷却计时不是匹配阶段计时 |
| 入站一律 SendAs，过滤/幂等/广播不绕管线 | `external/frames.go::sendInboundLocked`、`media.go::sendInboundMediaLocked`；`conversation/pipeline.go::SendAs`；`TestBridgeEndToEndWithFakeSession`、`TestBridgeRealWanfengMediaRoundTrip` | 通过；文本 ClientMsgID 用 provider/peer/tid 的 UUID SHA1、SkipRateLimit=true；文本 1000 字上限且仍经内容过滤 |
| 隐私：仅随机绑定昵称、性别、已设置年龄，不发本站 IP/省份/昵称 | `binding.go::Chat2019`、`provider_chat2019.go::open`、`chat2019/client.go::Dial`；`provider_wanfeng.go::open`、`connectWanfeng`；`TestDialSignsPrivatelyAndSendsMatchingFrames`、`TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost` | 通过。chat2019 userip/useraddree 空、不含 matchPreference；晚风先 PushShowCity(false) 再就绪 |
| 晚风源码只允许四项本地扩展 | `wanfeng/PORTED_FROM.md`；本轮 `diff -qr` 仅 adapter.go/client.go/httpx.go/ip_ban.go 及登记的新增文件有差异 | 通过。日志修复在外层 handler，移植文件本轮未改 |
| 无提交、无生产/真实外站、测试只用本地 fake | `external/*_test.go`、`chat2019/*_test.go`、`cmd/probe-external/*_test.go` 均本地假 session/httptest；live_smoke_test 默认跳过 | 通过；本轮数据库验证仅 127.0.0.1:55432，未启用 live 测试 |
| 每次匹配 attempt 不复用，Start/Cancel/Found/Failed 全链路校验 | `matcher.go::cmdClaimAttempt`、`cmdExternalFailed`、`cmdRequeueExternal`；`bridge.go::validAttempt`、`StartMatch`、`CancelMatch`、`frames.go::activate` | 通过，身份/代次/队列/phase/超时全部检查；intents 用 seq/inflight，非时间戳；下方 match 表列明边界测试 |
| 本站文件仅由已授权的数据库媒体消息触发外传；文本不解析成媒体 | `frames.go::outboundLoop` 先 GetMessageForRelay 并复验 conversation/sender；`provider_wanfeng.go::sendText` 只 TextCommand，sendMedia 只 MediaCommand | 通过；`TestBridgeOutboundVerifiesStoredSenderAndConversation`、`TestProviderWanfengCommandsKeepBracketTextAsText` |
| 随机内部 token、按 link 登记、一次性；入站只认本 link MirrorInbound 登记 | `external/media.go::mediaToken`（rand 16 bytes hex）、`MirrorOutbound`、`MirrorInbound`、`receiveMediaLocked`；`TestMediaRejectsUnregisteredAndOtherLinkTokens`、`TestBridgeMediaTokenOneUseEvenWhenFileReadFails` | 通过；消费失败也不能二次使用；URLKey 缓存避重复下载，不复用入站一次性 token |
| 媒体归属、source/kind/1v1 范围、失效外键错误 | `conversation/pipeline.go::HandleSend`、`SendAs`；`media/service.go::GetUserMediaForSend`；`queries/media.sql::GetUserMediaForSend`；`TestUserMediaValidationAndOneToOneRestriction`、`TestUserMediaDeletedBeforeInsertReturnsInvalidRequest` | 通过；只接受 source=user 且 owner=发送者，RequireOneToOne 仅 match/bottle/private；FK 23503 精确约束映射 |
| MIME 只信嗅探、限额与白名单、上传者预览与他人 404 | `media/service.go::saveFile`、`allowedMIME`、`sniffMIME`；`handler.go::UploadUserHandler`；`TestMediaSourcesOwnershipLimitsAndRollback`、`TestUserUploadAuthenticationPreviewAndRateLimit`、`TestUserUploadVerificationLimitSettingsAndSize` | 通过；jpeg/png/gif/webp/mp4/webm，大小、认证/验证、每身份默认 10/min 动态额度 |
| 直连只拨已检查 IP、重定向/克隆不绕过；代理主机白名单 | `netguard.go::dialDirect`、`HTTPClient`、`GorillaDialer`；`bootstrap.go`；`media.go::MirrorInbound`；NetGuard 场景表见外站审计文件 | 通过；晚风 URL 先 BaseURL.ResolveReference，再查主机白名单；chat2019 媒体只相对路径且禁重定向 |
| token 加密，不配置密钥晚风不可用；注册令牌桶 | `binding.go::Save/Get/WanfengAvailable`（secretbox）；`IdentityLimiter`、`provider_wanfeng.go::open`；`TestExternalAccountBindingPersistence`、`TestBindingWithoutKeyRejectsWanfeng`、`TestIdentityLimiterGlobalBucketAndRefill/Concurrent` | 通过；SETTINGS_ENC_KEY 32 字节，nonce 密文存储；账号复用不消耗新注册额度 |
| 日志无 token/Cookie/正文全文/带签名 URL/原始错误 | **本轮修复** `external/logging.go`、`bootstrap.go::NewConfiguredBridge`、`bridge.go::NewBridge`；`TestExternalLogsNeverEchoUpstreamData` | 原有日志存在完整 data/map、URL/error 泄露；外层 handler 白名单保留事件/provider/数值及错误类型，丢弃上游 data/message/URL/身份/城市/错误原文；With/WithGroup 同样处理。probe 丢弃库日志，输出先脱敏 |
| 热关闭：先锁内失效，再锁外结束；激活 publish 顺序 | `bridge.go::ApplySettings/closeLink/Close`、`frames.go::activate`；`TestBridgeRejectsActivationOnEveryClosePath`、`TestBridgeDisconnectInvalidatesActivationImmediately`、`TestBridgePublishedBeforeConcurrentHotClose`、`TestBridgeProviderDisableOnlyClosesThatProvider` | 通过，Activate=false 不 publish，ExternalFound 拒绝激活清理与回队另由 match 测试覆盖 |
| OnMessage 快速返回，串行有界转发，连接容量/空闲复用 | `frames.go::OnMessage/outboundLoop/inboundLoop/idleLocked`；`TestBridgeNonExternalMessageDoesNotReadDatabase`、`TestBridgeActivationGateAndInboundOrdering`、`TestBridgeConversationEndedLeavesAndReusesIdleSession` | 通过。OnMessage 只查内存与非阻塞队列；pending 最多 50，发送队列有界，容量上限/60s idle 关闭，自己的 idle 不占新名额 |
| 孤儿媒体发送/清理互斥、不饿死后续批次 | `queries/media.sql::ListOrphanNonLibraryMedia/DeleteOrphanNonLibraryMedia`、`jobs/cleaner.go::orphanMediaBatch/cleanOrphanMedia` | 通过。READ COMMITTED 两语句：先 FOR UPDATE SKIP LOCKED 候选，再重新检查 NOT EXISTS 删除；(created_at,id) 游标、500/批、20 批/轮，提交后删除文件。下方列举并发/大批次测试 |

## how.md「测试与部署」剩余逐场景映射

### match

| 场景 | 对应测试（backend/internal/match/） |
|---|---|
| 阶段推进/在线阈值/本站等待/外站超时 | `matcher_external_test.go::TestExternalStagesAndOnlineThreshold`，在线数 0/1/5/6，9s/10s 边界 |
| 本站抢先配对并取消、取消/离开/重入 | `TestExternalLocalPairingAndRemovalCancel`（pair/cancel/leave/replace） |
| 认领成功与取消/超时/身份/代次/阶段/重入旧代次拒绝 | `TestExternalClaimRequiresCurrentUnexpiredAttempt`（success/wrong-id/wrong-attempt/cancelled/timeout/fallback/local/reentered） |
| 旧失败作废、当前失败回队 | `TestExternalFailedIgnoresStaleResults`、`TestExternalRequeueUsesIntentVersion` |
| 启用时外站结束立即兜底；关闭/未注册原行为；热关闭 Cancel 正确代次；Start 失败兜底 | `TestExternalFallbackTimingAndHotDisable`（enabled/disabled/unregistered/hot-disable/start-failed）、`TestExternalStartFailureDoesNotBlockMatcher`；既有 matcher_fallback_test 原样通过 |
| seq/inflight 在途保护；旧流程回队恢复意图；创建 bot 失败重试 | `TestExternalIntentSurvivesUntilAllInflightFinish`、`TestExternalClaimAfterLegacyRequeueRestoresIntent`、`TestExternalBotCreationFailurePreservesFallbackPhaseAndRetry` |
| Found 事务成功/提交后激活/同步订阅/推送只在 publish 内 | `match_external_integration_test.go::TestExternalFoundCommitsBeforeActivateAndPublishesInsideHook` |
| 拒绝激活结束、不订阅/推送 found、按规则回队 | `TestExternalFoundRejectedActivationEndsAndRequeues` |
| 固定时钟同刻认领后取消/重入，失败不覆盖新意图 | `TestExternalFoundFailureRequeueRespectsIntentAtSameTime`（none/cancel/reenter/cancel-reenter-cancel/success-after-cancel） |
| 建会话失败回滚/回队；数据库上下文限时 | `TestExternalFoundInvalidPeerRollsBackAndRequeues`、`TestExternalFoundBoundsDatabaseContext` |
| race 无竞争 | 定向 match race 通过；完整集成场景在 P10 -count=1 串行验证 |

### 集成、媒体清理、前端

| 场景 | 对应测试 |
|---|---|
| 临时独立库 00009→00010→单步 down→00010，媒体库保留 | `backend/migrations/external_bridge_integration_test.go::TestExternalBridgeMigrationRoundTrip`，非开发/共享测试库 down |
| 假 session：外站配对→双向消息→结束 | `backend/internal/external/bridge_integration_test.go::TestBridgeEndToEndWithFakeSession` |
| 真实移植晚风客户端+本地加密假上游完整链路 | `external/media_integration_test.go::TestBridgeRealWanfengMediaRoundTrip`，详细断言见外站审计文件 |
| 用户上传认证/类型/大小/频率；发送前预览、他人拒绝 | `media/user_upload_integration_test.go::TestUserUploadAuthenticationPreviewAndRateLimit`、`TestUserUploadVerificationLimitSettingsAndSize`、`external_queries_integration_test.go::TestUserMediaQueriesRespectOwnershipAndBurn` |
| 上传后会话访问/焚毁；他人 mediaId、source、kind 拒绝，群聊禁止媒体 | `media::TestUserMediaQueriesRespectOwnershipAndBurn`；`conversation::TestUserMediaSendInAllOneToOneKinds`、`TestUserMediaValidationAndOneToOneRestriction`、`TestUserMediaRequireOneToOneInSendAs` |
| 孤儿清理：旧无引用删除；媒体库/年轻/仍引用保留 | `jobs/orphan_media_integration_test.go::TestOrphanMediaDeletesOnlyOldUnreferencedFiles` |
| >500 条仍引用旧媒体后仍能清理孤儿；每轮最多20批/游标 | `TestOrphanMediaPassesMoreThan500ReferencedRows`、`TestOrphanMediaStopsAfter20BatchesAndUsesCursor` |
| 并发发送/清理，两锁顺序及第二语句重查，消息引用不被清空 | `TestOrphanMediaSkipsMediaLockedBySending`、`TestOrphanMediaSecondStatementRechecksReference`、`TestOrphanMediaCleanupLockMakesConcurrentSendFailWithoutNullReference`；`conversation::TestUserMediaDeletedBeforeInsertReturnsInvalidRequest` |
| 提交后删除文件；保留期释放后下一轮清理 | `TestOrphanMediaCommitsBeforeRemovingFiles`、`TestOrphanMediaReleasedByRetentionIsDeletedNextRun` |
| Composer 按回调显示、accept、上传禁用、失败恢复 | `frontend/src/components/Composer.test.tsx`，`lib/useChat.test.tsx`、`lib/api/media.test.ts` |
| 匹配/漂流瓶/私聊先上传再发送；房间/故事书无附件 | `frontend/src/features/chatMedia.test.tsx` |
| WS 媒体参数与重试；无 ID 的 media_rejected 统一事件 | `frontend/src/lib/ws/client.test.ts` |
| 后台表单纯函数边界/无效数字、状态轮询/保存/路由 | `features/admin/externalForm.test.ts`、`pages/ExternalPage.test.tsx`、`AdminRoutes.test.tsx` |
| 举报实际快照含 kind/mediaId、后台图片/视频渲染 | `moderation::TestReportSnapshotPersistsMediaKindsAndIDs`、`features/admin/pages/ReportDetailPage.test.tsx` |

## 本轮发现与验证

1. 日志泄露：真实 RED 为 `go test ./internal/external -run TestExternalLogs -count=1`，bridge/configured 两子测试均输出测试用 token/Cookie/正文/URL/error；`/tmp/anonchat-p8-logging-red.log`。修复后同范围 race 通过，`/tmp/anonchat-p8-logging-green.log`。
2. 外站六组覆盖缺口：详见配套外站审计文件，已补 `p8_bridge_test.go`。既有行为补覆盖为 TDD-EXEMPT，不伪造 RED。
3. `make lint`（含 go vet ./...）通过；`go test ./internal/external/... -race -count=1` 通过（external/chat2019/wanfeng）；新增六组定向 race、vet、staticcheck 通过。完整 P10 是最终门禁。
4. 除上述已补缺口，how.md「测试与部署」的每个要求场景均有对应自动化测试。没有用概率测试证明随机均匀性；验证可控随机源两种顺序并核对生产 choose=rand.IntN。
