# 任务清单: AI 机器人（Bot）

目录: `helloagents/main/plan/202611062351_ai-bot/`

- 实施者：pi agent（deepseek-flash）。审查者：Claude，每个阶段结束后复核。
- 设计以 `how.md` 为准，设计中没写到的地方沿用现有代码的写法，不要自行扩展范围。
- 每个阶段结束后运行该阶段列出的验证命令，确认全部通过后，把对应条目的 `[ ]` 改为 `[√]`，然后**停下来汇报**：改了哪些文件、验证命令的输出摘要、与设计的偏差及原因。
- 不要 git commit，不要回滚或改动与本任务无关的文件。

通用验证命令（在项目根目录执行）：

- `make sqlc`
- `make test`
- `make test-integration`（本机 PostgreSQL：127.0.0.1:55432）
- `make lint`
- `make web-test`
- `cd frontend && pnpm build`

---

## B1. 数据层与配置

- [√] B1.1 迁移 `00005_bots.sql`（how.md §1），`queries/bot.sql` 和 `queries/media.sql`，执行 `make sqlc`
- [√] B1.2 配置项（§2），`getBool`，配置测试
- [√] B1.3 `testutil.Truncate` 加入新表；`jobs/cleaner.go` 增加 `bot_usage` 30 天清理
- 验证：`make sqlc && make test`，并对迁移执行一次 up → down → up（可以用 `make migrate` 和 `make migrate-down`，针对开发库 anonchat_dev）

## B2. 消息管道与媒体

- [√] B2.1 `conversation.SendAs`、`SendError`、`Observer`、`BroadcastTyping`、`mediaUrl`（§3）；`InsertMessage` 增加 `media_id`，同步修改所有调用方
- [√] B2.2 `internal/media` 包：存储、管理接口、用户侧 `GET /api/media/{id}`（§4）；在 routes 中注册
- [√] B2.3 对应的集成测试（§13 第 5 条中媒体相关的部分），以及 mediaUrl 的访问控制
- 验证：`make test && make test-integration && make lint`

## B3. LLM 客户端、提示词与引擎

- [√] B3.1 `bot/llm.go` 和单元测试（§5）
- [√] B3.2 `bot/prompt.go`、`limits.go`、`pacing.go` 和单元测试（§6、§7.1）
- [√] B3.3 `bot/service.go`、`handlers.go`：角色卡和 bot 的管理接口与用量统计（§10）
- [√] B3.4 `bot/engine.go`：对话中的回复流程和开场（§7.2、§7.3）
- 验证：`make test && make lint`

## B4. 匹配兜底与漂流瓶

- [√] B4.1 `match` 的 `SetBotFallback`、导出 `Compatible`、matcher 的认领流程，以及单元测试（§8）
- [√] B4.2 `bottle.HoldForBot` 和 bot 的漂流瓶循环（§9）
- [√] B4.3 装配（§11），`testutil` 扩展
- [√] B4.4 集成测试：§13 第 1–5 条
- 验证：`make test && make test-integration && make lint`

## B5. 前端

- [√] B5.1 `client.ts` 中的类型和后台接口函数
- [√] B5.2 `MessageList` 渲染图片和视频，以及测试
- [√] B5.3 `AdminPage` 的 Bot 标签页：角色卡、Bot、媒体库三个子面板（§12）
- 验证：`make web-test && cd frontend && pnpm build`

## B6. 部署与知识库

- [√] B6.1 docker-compose 的媒体卷、环境变量示例、`deploy/README.md`
- [√] B6.2 知识库同步（§14）
- 验证：全部通用验证命令

## B7. 增补：后台配置全局开关和模型接入（how.md §15）

- [√] B7.1 迁移 `00006_bot_settings.sql`、查询、首次初始化、`SETTINGS_ENC_KEY`、加密复用、配置校验调整
- [√] B7.2 引擎改为始终运行：缓存生效配置、动态构建 LLM 客户端、全局开关和"不可用"处理、`FallbackAfter` 动态化
- [√] B7.3 后台接口：GET、PUT、test（§15.3）
- [√] B7.4 前端"全局设置"子面板（§15.4），API 函数不进主包
- [√] B7.5 测试（§15.5）、文档（§15.6）
- 验证：make sqlc、make test、make test-integration（连续跑 2 次）、make lint、make web-test、cd frontend && pnpm build，并检查主包中没有 bot 相关的字符串
