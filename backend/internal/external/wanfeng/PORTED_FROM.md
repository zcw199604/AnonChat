# 晚风协议包来源与本地改动

来源：`/Users/zcw/project/liao/internal/upstream/wanfeng/`。
来源 commit：`5fe3a8ebbd1590ec008dde462bce6c8673d6268f`（`5fe3a8e`）。
复制了来源目录全部 26 个 `.go` 文件，包名保持 `wanfeng`。
原测试全部保留；`live_smoke_test.go` 原样保留，默认跳过，本轮未设置 `WANFENG_LIVE_TEST=1`。

仅以下四项生产代码改动（对应方案 how.md §4.1）：

1. `ip_ban.go`：新增 `IsIPBanMessage(msg string) bool`，直接包装既有 `isIPBan("", msg)`。
2. `client.go`：新增 `ConnState`（`ConnReady` / `ConnLost` / `ConnStopped`）、`ClientConfig.OnConnState` 与 `OnCommandFailed` 可选回调。
   - `onConnected` 通知就绪；已建立连接清理时通知非主动断开，通知发生在内部重连之前。
   - `Close` 或连接循环彻底退出通知停止，通过独立 `stoppedOnce` 保证只通知一次。
   - 队列满报告实际丢弃的动作；`dispatchLoop` 报告 `deliver` 返回的写入或媒体搬运错误。
   - 回调均在客户端互斥锁外调用；nil 回调保持原行为。
3. `adapter.go`：新增 `Protocol.TextCommand` 和 `MediaCommand`。
   - 复制 `BuildCommand` 对应分支的载荷与 `localTid` 生成方式；不改既有 `BuildCommand`。
   - `TextCommand` 始终发送纯文本，`[x.jpg]` 不会被解析成媒体；媒体意图只由 `MediaCommand` 显式构造。
4. `httpx.go`：`APIClient.Clone()` 额外保留原 HTTP 客户端 `CheckRedirect`，继续使用原有 Transport、独立 Cookie Jar 和共享会话。

新增文件：

- `port_extensions_test.go`：上述四项的本地回归测试，包括 IP 封禁提示、显式文本/媒体载荷、克隆重定向守卫、连接就绪/非主动断开/停止及致命启动错误、队列满、下发失败和媒体搬运失败回调。仅使用本地假上游。
- `staticcheck.conf`：仅本移植目录禁用 U1000；来源 `e2e_media_test.go` 的 `mediaURL` 字段未使用，为维持原样不修改来源测试。其余检查继承默认值。
- 本文件。

依赖：`github.com/gorilla/websocket v1.5.3`、`github.com/google/uuid v1.6.0`，以及原有 `golang.org/x/sync v0.24.0` 由 indirect 转为直接依赖。无其他依赖版本变化。
