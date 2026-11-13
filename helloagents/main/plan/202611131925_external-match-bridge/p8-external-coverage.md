# P8 外站客户端与 Bridge 场景核对

范围：本方案 how.md §0、§4、§5、§6.5 及「测试与部署」中的 `external/chat2019`、`external/netguard`、`external/wanfeng`、`external` 场景。仅检查本地代码与测试，没有连接数据库或真实外站。日志安全由主实施者另行记录。

以下路径均相对项目根目录，行号为本轮核对时位置；测试名为稳定检索依据。P4 后续用户修正规则优先于 how.md 原始 code=15 的概括：matching 处理、idle 离开、activating/inRoom 忽略，见 `frames.go:76` 与 `TestP4Code15RespectsLinkState`。

## 硬约束与安全/生命周期映射

| 约束 | 实现位置 | 证据 |
|---|---|---|
| 外站开关默认关闭、Plan 只读内存 | `external/settings.go`；`external/bridge.go:177` | `TestExternalSettingsAPI`、`TestConfiguredBridgeBuildsGuardedClientsWithoutNetwork`；匹配旧行为由 match 审计核对 |
| Bridge 不计匹配超时，只计空闲/冷却 | `bridge.go:211` StartMatch、`frames.go:247` idleLocked；idle timer 为 60s | `TestBridgeConversationEndedLeavesAndReusesIdleSession`、`TestBridgeFailureRequeueBlockedPeerAndCooldowns` |
| 所有入站经 SendAs、影子身份 | `frames.go:221`、`media.go:206`，NewBridge 装配 `Conv.SendAs` | `TestBridgeEndToEndWithFakeSession`、`TestBridgeRealWanfengMediaRoundTrip` |
| 外站仅发送随机绑定昵称、性别/年龄；chat2019 不传 IP/地址 | `provider_chat2019.go:28`、`chat2019/client.go:82`；`provider_wanfeng.go:43`，`binding.go:108` | `TestDialSignsPrivatelyAndSendsMatchingFrames`、`TestExternalAccountBindingPersistence`、`TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost` |
| 晚风先关闭城市公开再允许匹配 | `provider_wanfeng.go:165–177` | `TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost` |
| 代次只认最新且高于 cancelledUpTo | `bridge.go:188` validAttempt、`:211` StartMatch、`:346` CancelMatch | `TestBridgeAttemptsCapacityAndCancelledBeforeStart`、`TestBridgeNewAttemptReplacesOldMatchingLinkAtCapacity`、`TestBridgeIgnoresLateOlderStart` |
| 每轮先选站再复用；取消匹配 socket 不复用 | `bridge.go:248–299` | `TestBridgeSelectsBeforeReusingAndOwnIdleDoesNotUseCapacity`、`TestBridgeAttemptsCapacityAndCancelledBeforeStart` |
| 每 link 串行入站，pending ≤50，Found 成功返回后才开闸 | `frames.go:28–62`、`:128`、`:180–220` | `TestBridgeActivationGateAndInboundOrdering` |
| 激活检查/登记/publish 在 link 锁内 | `frames.go:187–208` | `TestBridgeRejectsActivationOnEveryClosePath`、`TestBridgeDisconnectInvalidatesActivationImmediately`、`TestBridgePublishedBeforeConcurrentHotClose` |
| 所有关闭先使激活失效，再锁外结束会话 | `bridge.go:378–431`；`:472–502` | 同上，另 `TestBridgeProviderDisableOnlyClosesThatProvider` |
| 第一次晚风断线即 Close，不依赖内部重连 | `provider_wanfeng.go:140–149`、`:178` | `TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost`、`TestBridgeRealWanfengMediaRoundTrip` |
| OnMessage 只查内存，消费时再查消息归属 | `frames.go:310–364` | `TestBridgeNonExternalMessageDoesNotReadDatabase`、`TestBridgeOutboundVerifiesStoredSenderAndConversation` |
| 文本只走 TextCommand；媒体只走 MediaCommand | `provider_wanfeng.go:115–119` | `TestProviderWanfengCommandsKeepBracketTextAsText`、`TestBridgeRealWanfengMediaRoundTrip` |
| 内部 token 随机 16 字节、按 link 登记且一次消费 | `media.go:45–51`、`:89–101`、`:218–234`、`:255–291` | `TestMediaRejectsUnregisteredAndOtherLinkTokens`、`TestMediaUnknownWanfengTokenRemainsText`、`TestBridgeMediaTokenOneUseEvenWhenFileReadFails`、真实客户端链路 token/cache 断言 |
| 入站地址先按 BaseURL 解析、查主机白名单，再访问网络/缓存 | `media.go:130–183` | `TestMediaRejectsUnlistedHostBeforeNetworkOrCache`、`TestBridgeRealWanfengMediaRoundTrip`（相对路径） |
| MIME/媒体种类信任保存时嗅探 | `media.go:225–233`、`:237–250` | `TestBridgeChatMediaUsesSniffedKindAndUpload`（伪装图片/视频后缀与 MIME） |
| token 加密与无密钥禁用；新账号令牌桶 | `binding.go:40–86`、`:141–176`；`provider_wanfeng.go:22–41` | `TestExternalAccountBindingPersistence`、`TestBindingWithoutKeyRejectsWanfeng`、`TestIdentityLimiterGlobalBucketAndRefill`、`TestIdentityLimiterConcurrent` |
| 直连只拨已检查 IP；代理白名单；重定向/Clone 保持守卫 | `netguard.go:102–161`、`:179–225`；`wanfeng/httpx.go` Clone | 见下 NetGuard 逐场景表 |

## how.md「测试与部署」逐场景覆盖

### external/chat2019

| 场景 | 测试位置/名称 |
|---|---|
| sign 字段，不含 IP/地址/matchPreference；匹配和取消帧 | `chat2019/client_test.go:48` `TestDialSignsPrivatelyAndSendsMatchingFrames`；`provider_chat2019_test.go` `TestProviderChat2019Commands` |
| forceout、意外断线 | `chat2019/client_test.go:115` `TestForceoutAndUnexpectedDisconnect` |
| WS ping | `chat2019/client_test.go:192` `TestConnectionSendsWebSocketPing` |
| 图片上传/下载、请求头、缓存、端口回退 | `chat2019/media_test.go:25` `TestMediaUploadDownloadHeadersCacheAndPortFallback` |
| 非法 WS、发现失败 fallback | `chat2019/client_test.go:157` `TestDialRejectsInvalidWSAndFallsBackOnDiscoveryFailure` |
| 非相对媒体路径、非法图片主机、超限、上游失败 | `chat2019/media_test.go:111` `TestMediaRejectsNonRelativePathsBeforeNetwork`；`:128` `TestMediaRejectsInvalidHostsOversizeAndUpstreamFailure` |

### external/netguard

| 场景 | 测试位置/名称 |
|---|---|
| 回环、私有、链路本地、CGNAT、未指定/组播拒绝 | `netguard_test.go:38` `TestNetGuardRejectsNonPublicAddresses` |
| 二次解析返回不同地址时仍只拨已检查 IP | `netguard_test.go:54` `TestNetGuardDialsCheckedIPWithoutSecondResolution` |
| 混合公网/私网 DNS、空答案拒绝 | `netguard_test.go:85` `TestNetGuardRejectsMixedDNSAndEmptyAnswers` |
| chat2019 禁止重定向；晚风白名单外/内网重定向拒绝 | `netguard_test.go:99` `TestNetGuardRedirectPolicy` |
| 代理白名单外主机拒绝 | `netguard_test.go:141` `TestNetGuardProxyRequiresAndEnforcesAllowlist` |
| 重定向次数/协议限制 | `netguard_test.go:175` `TestNetGuardRedirectLimitAndSchemes` |
| coder/gorilla WS 共用策略、TLS 原域名、Clone 守卫 | `netguard_test.go:195` `TestNetGuardWebSocketDialersSharePolicy`；`:254` `TestNetGuardTLSKeepsOriginalServerName`；`:280` `TestNetGuardWanfengCloneKeepsRedirectGuard` |
| HTTPS 代理 TLS CONNECT | `netguard_test.go:294` `TestNetGuardGorillaHTTPSProxyUsesTLSConnect` |

### external/wanfeng 移植扩展

| 场景 | 测试位置/名称 |
|---|---|
| 封禁识别导出 | `wanfeng/port_extensions_test.go:15` `TestPortIsIPBanMessage` |
| TextCommand 与旧文本分支一致、[x.jpg] 仍为文本；MediaCommand 一致 | `wanfeng/port_extensions_test.go:28` `TestPortExplicitCommandsDoNotParseTextAsMedia` |
| Clone 保留 CheckRedirect | `wanfeng/port_extensions_test.go:56` `TestPortClonePreservesRedirectGuard` |
| OnConnState ready/lost/stopped、致命启动失败 stopped | `wanfeng/port_extensions_test.go:82` `TestPortConnStateReadyLostStopped`；`:129` `TestPortConnStoppedOnFatalSetup` |
| OnCommandFailed 队列满、下发失败、媒体搬运失败 | `wanfeng/port_extensions_test.go:144` `TestPortCommandFailedOnQueueOverflow`；`:167` `TestPortCommandFailedOnDeliveryAndMediaError`（delivery/media 子测试） |

### external Bridge

| 场景 | 测试位置/名称 |
|---|---|
| 随机站点顺序、每轮先选站再复用、自己的 idle link 不占新名额 | `bridge.go:165` choose=rand.IntN；`bridge_test.go:155` `TestBridgeSelectsBeforeReusingAndOwnIdleDoesNotUseCapacity` 用可控 choose=0/1 验证两种选择；不做不稳定概率断言 |
| 跳过不可用、两个都不可用 | **本轮补齐** `p8_bridge_test.go:15` `TestBridgeSkipsUnavailableProvidersAndRejectsAllUnavailable` |
| 容量上限；上限下调不踢已有 | `bridge_test.go:116` `TestBridgeAttemptsCapacityAndCancelledBeforeStart`；**本轮补齐** `p8_bridge_test.go:90` `TestBridgeLowerCapacityKeepsExistingSessions` |
| 冷却、IP 封禁 10 分钟、到期恢复 | `bridge_test.go:379` `TestBridgeFailureRequeueBlockedPeerAndCooldowns`；**本轮补齐** `p8_bridge_test.go:72` `TestBridgeIPBanCooldownRejectsUntilExpiry` |
| 过期 Cancel、Cancel 先于 Start、取消 link 关闭不复用 | `bridge_test.go:116` `TestBridgeAttemptsCapacityAndCancelledBeforeStart` |
| 旧 Start 无效、新 attempt 替换正在匹配 link、清理 intent | **本轮补齐** `TestBridgeIgnoresLateOlderStart`；`bridge_test.go:504` `TestBridgeNewAttemptReplacesOldMatchingLinkAtCapacity`；`:485` `TestBridgeReapsInactiveAttemptAndRetainsActive` |
| open 期间 Cancel；打开失败换站 | `bridge_test.go:185` `TestBridgeOpenCancellationAndFailureFallback` |
| 会话结束 leaveRoom；真实 idle 后复用 | `bridge_integration_test.go:249–261` 验证 End/leave；**本轮补齐** `p8_bridge_test.go:38` `TestBridgeConversationEndedLeavesAndReusesIdleSession` |
| activating 断线/热关闭/Close 后拒绝 Activate、不 publish | `bridge_test.go:274` `TestBridgeRejectsActivationOnEveryClosePath`（lost/settings/close）；`:431` `TestBridgeDisconnectInvalidatesActivationImmediately` |
| 激活后立刻热关闭，found 在 ended 前 | `bridge_test.go:453` `TestBridgePublishedBeforeConcurrentHotClose` |
| 入站缓存顺序/上限；Found 返回前不发入站 | `bridge_test.go:230` `TestBridgeActivationGateAndInboundOrdering` |
| 回显识别、非当前对方丢弃、晚风 code=0 提示和 code=16；chat2019 code=16 不结束 | `bridge_test.go:312` `TestBridgeEchoWrongPeerSystemNoticeAndProviderLeave`（chat2019/wanfeng） |
| code=15 状态修正 | `p4_corrections_test.go:26` `TestP4Code15RespectsLinkState`（matching/idle/activating/inroom） |
| 影子身份复用、资料映射 | `peer_integration_test.go:16` `TestExternalPeerReuseAndProfileRefresh`；`:58` 并发创建；`peer_test.go:5` `TestPeerProfileMapping` |
| 被屏蔽对方 leave 后再 startMatch | `bridge_test.go:379` `TestBridgeFailureRequeueBlockedPeerAndCooldowns` |
| 入站文本经 SendAs、出站归属复验 | `bridge_integration_test.go` `TestBridgeEndToEndWithFakeSession`；`bridge_test.go:349` `TestBridgeOutboundVerifiesStoredSenderAndConversation` |
| 入站/出站媒体、嗅探 kind、媒体后配文 | `media_integration_test.go:217` `TestBridgeRealWanfengMediaRoundTrip`；`:350` `TestBridgeChatMediaUsesSniffedKindAndUpload` |
| 出站媒体失败推 error，文本失败不推；队列满分类 | `p4_corrections_test.go:61` `TestP4CommandFailuresNotifyOnlyMedia`；`media_test.go:75` `TestMediaQueueOverflowOnlyNotifiesForMedia`；真实晚风链路上传失败段 |
| 文本/未登记/其他 link 的伪 token 不触发读取或发送 | `media_test.go:17` `TestMediaUnknownWanfengTokenRemainsText`；`:35` `TestMediaRejectsUnregisteredAndOtherLinkTokens`；`provider_wanfeng_test.go:22` `TestProviderWanfengCommandsKeepBracketTextAsText` |
| token 随机、一次消费、缓存回显不再下载 | 真实晚风链路 token/cache 段；`media_integration_test.go:429` `TestBridgeMediaTokenOneUseEvenWhenFileReadFails` |
| 连接中断结束会话 | 真实晚风链路最后断线段；`provider_wanfeng_connection_test.go:30` `TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost` |
| 热关闭全站/单站隔离 | `TestBridgeRejectsActivationOnEveryClosePath`、`TestBridgePublishedBeforeConcurrentHotClose`；**本轮补齐** `p8_bridge_test.go:114` `TestBridgeProviderDisableOnlyClosesThatProvider` |
| 真实移植客户端 + 假上游：配对→双向文本→入站图片→出站图片→断线 | `media_integration_test.go:217` `TestBridgeRealWanfengMediaRoundTrip`（真实加密握手、WS 和媒体 HTTP） |

## 本轮缺失项与处理

原来明确缺少「不可用站点跳过/双不可用」直接测试；原 idle 复用测试直接设置 state，未串起 OnConversationEnded；IP 封禁冷却选择阻断/到期恢复、下调容量不踢、单站关闭隔离和晚到旧 Start 没有独立断言。现已全部在新文件 `backend/internal/external/p8_bridge_test.go` 补齐，既有代码和测试未改。这些是现有行为补覆盖，按 TDD-EXEMPT 记录，不伪造 RED。

定向验证：`cd backend && go test ./internal/external -run 'TestBridge(SkipsUnavailable|ConversationEndedLeaves|IPBanCooldown|LowerCapacity|ProviderDisable|IgnoresLateOlder)' -race -count=1` 退出 0，external 1.417s；`go vet ./internal/external`、`go tool staticcheck ./internal/external` 均退出 0。未发现上述范围内需要修改生产代码的新增缺陷；日志问题由主实施者另行处理。本文件不替代 match、用户媒体/清理、前端及全量门禁的核对记录。
