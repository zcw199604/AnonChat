# 任务清单: Bot 人设包、双核驱动与长期记忆

目录: `helloagents/main/plan/202611092002_bot-persona-memory/`

## 给实施者（Codex）的执行约定

- 实施者：Codex（`gpt-6.1-sol`，reasoning medium）。审查者：Claude，每个阶段结束后复核。
- **设计以 `how.md` 为准**。`how.md` 没写到的地方，沿用现有代码的写法，不要自行扩展范围。确实需要偏离时，先在汇报里说明原因。
- 先通读 `why.md`、`how.md` 和 §0 硬性约束，再读 `backend/internal/bot/` 的现有代码。
- 每个阶段完成后，运行该阶段列出的验证命令。全部通过后，把对应条目的 `[ ]` 改为 `[√]`，然后**停下来汇报**：改了哪些文件、验证命令的输出摘要、与设计的偏差及原因。
- 标注 TDD 的任务：先写失败的测试（RED），再写实现（GREEN），最后整理（REFACTOR）。汇报时附上 RED 阶段的失败输出摘要。
- 不要 git commit；不要回滚或改动与本任务无关的文件（工作区里已有的 `analysis/` 和其他方案包的改动不要动）。
- 只有在需要确认数据库时，才连接本机开发库（`127.0.0.1:55432`）。不要连接任何远程或生产环境。

通用验证命令（在项目根目录执行）：

- `make sqlc`
- `make test`
- `make test-integration`（本机 PostgreSQL：127.0.0.1:55432）
- `make lint`
- `make web-test`
- `cd frontend && pnpm build`

---

## 0. 方案边界确认
- [√] 0.1 核对 why.md 的范围内和范围外，确认 how.md 的 ADR-5（TOML 依赖）是否已被用户否决（用户否决时按 ADR-5 的替代方案执行）
  - 执行模式: HITL
  - 涉及文件: `why.md`, `how.md`
  - 完成标准: 汇报里写明 ADR-5 的最终状态
  - 备注: 用户 2026-11-09 同意 TOML 依赖，ADR-5 已采纳
  - 验证方式: 只读核对

## P1. 数据层与设置
- [√] P1.1 迁移 `00009_bot_persona_memory.sql`（how.md §1）；更新 `queries/bot.sql`，新建 `queries/memory.sql`；`make sqlc`；`testutil.Truncate` 加入新表
  - 执行模式: AFK
  - 涉及文件: `backend/migrations/00009_bot_persona_memory.sql`, `backend/queries/bot.sql`, `backend/queries/memory.sql`, `backend/internal/store/*`（生成）, `backend/internal/testutil/testutil.go`
  - 完成标准: 迁移在开发库上 up → down → up 都成功；`make sqlc` 没有差异残留；所有现有调用点编译通过（`InsertBotUsage` 的调用点暂时传 `reply`）
  - 验证方式: `make migrate && make migrate-down && make migrate && make sqlc && go build ./... && make test`
- [√] P1.2 `Settings` 新字段的读取、后台 GET/PUT 和校验；模型选择函数（how.md §2）。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/settings.go`, `settings_handler.go`, `engine.go`（模型选择）, 对应测试
  - 完成标准: 新字段可读写，越界返回 400；模型选择三条回退链有单元测试覆盖
  - 验证方式: `make test && make test-integration`（`settings_integration_test.go` 补用例）

## P2. 角色卡解析与导入
- [√] P2.1 `internal/bot/persona` 包：`Parse`、ST V1/V2/V3 JSON、PNG chunk、`.krp` zip、`Validate`、宏替换函数、§3.4 安全限制。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/persona/*.go`, `backend/internal/bot/persona/*_test.go`
  - 完成标准: how.md「测试与部署」里列出的 persona 测试全部存在并通过；zip 炸弹、路径穿越、伪造图片、坏 chunk 都返回错误或 warning，并且不会 panic
  - 验证方式: `cd backend && go test ./internal/bot/persona/... -race`
- [√] P2.2 `go.mod`：把 `github.com/BurntSushi/toml` 提升为直接依赖，版本不变（ADR-5），依赖 0.1
  - 执行模式: AFK
  - 涉及文件: `backend/go.mod`, `backend/go.sum`
  - 完成标准: `git diff backend/go.mod` 只有该依赖从 indirect 区移到直接依赖区，版本号不变；没有其他依赖变化
  - 验证方式: `cd backend && go mod tidy && git diff go.mod`
- [√] P2.3 `bot.Service`：角色卡新字段的 CRUD、`ImportCard`（how.md §3.5，失败回滚）、表情 CRUD、删除卡片时删除表情文件夹；media 依赖通过接口注入；装配写在 `cmd/server`
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/service.go`, `handlers.go`, `backend/internal/api/routes.go`, `backend/cmd/server/main.go`
  - 完成标准: how.md §10 中角色卡和表情相关的接口都可用，审计动作名正确
  - 验证方式: 集成测试：导入 `.krp` 后数据完整；导入中途失败时没有残留（卡片、文件夹、文件都不存在）；表情增删改；`make test-integration`

## P3. 心核提示词扩展
- [√] P3.1 `prompt.go`：新字段渲染、前缀稳定性规则、世界书触发、开场白随机挑选、宏替换、表情行解析、关系渲染、`FixHint`。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/prompt.go`, `prompt_test.go`, `prompt_user_mode_test.go`
  - 完成标准: 现有提示词测试仍然通过（期望值只在新增字段为空以外的情况下变化）；新增的前缀稳定性测试通过
  - 验证方式: `cd backend && go test ./internal/bot/ -run 'Prompt|Parse|Lore|Greeting' -race`

## P4. 长期记忆
- [√] P4.1 `memory.go`：画像合并与容量裁剪、`scrubPII`、`RenderMemory`、`affinityStage`、抽取提示词与 JSON 解析。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/memory.go`, `memory_test.go`
  - 完成标准: how.md §6.1、§6.3、§6.4、§6.5 的纯函数都有单元测试
  - 验证方式: `cd backend && go test ./internal/bot/ -run 'Memory|PII|Affinity' -race`
- [√] P4.2 `memory_worker.go`：候选查询、抽取事务、失败退避、超限跳过、`OnConversationEnded` 提前触发；在 `Engine.Run` 中启动，依赖 P4.1
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/memory_worker.go`, `engine.go`, `backend/queries/memory.sql`
  - 完成标准: how.md「测试与部署」中记忆 worker 的集成用例全部通过
  - 验证方式: `make test-integration`

## P5. 智核与引擎接入
- [√] P5.1 `reason.go`：智核提示词、`ParseReasonOutput`、三个内置工具、循环与降级。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/reason.go`, `reason_test.go`
  - 完成标准: how.md §5 的解析、截断、工具上限、降级都有单元测试
  - 验证方式: `cd backend && go test ./internal/bot/ -run Reason -race`
- [√] P5.2 `ooc.go`：`CheckStyle`、`Sanitize`；"我是 AI"不得命中任何规则。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/ooc.go`, `ooc_test.go`
  - 完成标准: how.md §7 每条规则都有命中和不命中的用例
  - 验证方式: `cd backend && go test ./internal/bot/ -run 'Style|Sanitize' -race`
- [√] P5.3 引擎接入：`runTurn` 新流程、表情发送、`hasPendingInput`、`TouchPeerMemory`、`recordUsage` 的 kind、`bottleReply` 注入记忆和跑偏检测（how.md §8），依赖 P3、P4.1、P5.1、P5.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/engine.go`, `bottles.go`, `bot_integration_test.go`, `bot_helpers_integration_test.go`
  - 完成标准: how.md「测试与部署」中 dual 模式、打断不发表情、跨 bot 隔离、用户侧字段不变这几个集成用例都通过；现有集成测试全部通过
  - 验证方式: `make test && make test-integration`（连续跑 2 次，确认不 flaky）

## P6. 清理任务
- [√] P6.1 `cleaner.go` 新增 how.md §9 的三项清理，并补集成测试
  - 执行模式: AFK
  - 涉及文件: `backend/internal/jobs/cleaner.go`, 对应集成测试
  - 完成标准: 过期记忆、吊销身份的记忆、旧游标被删除；未过期的保留
  - 验证方式: `make test-integration`

## P7. 后台接口补全
- [√] P7.1 bot 新字段、列表新增的统计列、记忆列表、详情、删除、清空接口与审计（how.md §10）
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bot/handlers.go`, `service.go`, `backend/queries/bot.sql`, `backend/queries/memory.sql`, `backend/internal/api/routes.go`
  - 完成标准: 接口可用；审计 detail 里不含记忆内容；非管理员访问返回 401/403
  - 验证方式: 集成测试补充记忆接口和权限用例；`make test-integration && make lint`

## P8. 前端
- [√] P8.1 `botApi.ts` 新增类型和函数；`botForm.ts` 校验与测试。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/admin/botApi.ts`, `botForm.ts`, `botForm.test.ts`
  - 完成标准: 新字段的校验范围与后端一致
  - 验证方式: `make web-test`
- [√] P8.2 `CharacterCardsPanel`：导入、分区编辑、世界书、工具、表情管理（how.md §11）
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/admin/CharacterCardsPanel.tsx`, `CharacterCardsPanel.test.tsx`
  - 完成标准: 导入成功后列表刷新并展示 warnings；表情上传和删除可用
  - 验证方式: `make web-test`
- [√] P8.3 Bot 表单与列表新字段、记忆弹窗、全局设置新字段
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/admin/BotListPanel.tsx`, `BotPanel.tsx`, 对应测试
  - 完成标准: 记忆的删除和清空都需要二次确认；全局设置能保存新字段
  - 验证方式: `make web-test && cd frontend && pnpm build`；检查主包里没有 bot 相关字符串（沿用 B7 的检查方法）

## P9. 安全检查
- [√] P9.1 按 how.md「安全与性能」逐项核对：导入限制、用户侧接口字段不变、日志不含记忆、智核输出和消息正文、审计不含内容、PII 清洗、已焚毁消息不参与抽取、未执行任何上传的代码
  - 执行模式: AFK
  - 涉及文件: 本包全部改动文件
  - 完成标准: 汇报里逐项写明结论和证据（测试名或代码位置）
  - 验证方式: `rg -n "slog.*(Body|Content|profile|gist|thought|guidance)" backend/internal/bot` 结果逐条人工核对；相关测试通过

## P10. 知识库与部署文档
- [√] P10.1 更新 `helloagents/main/wiki/glossary.md`、`wiki/data.md`、`project.md`、`deploy/README.md`（how.md「知识库同步」）
  - 执行模式: AFK
  - 涉及文件: 上述 4 个文件
  - 完成标准: 内容与代码事实一致，并链接到本方案包
  - 验证方式: 只读核对

## P11. 最终验证
- [√] P11.1 运行全部通用验证命令，依赖 P1–P10
  - 执行模式: AFK
  - 涉及文件: —
  - 完成标准: 全部通过，`make test-integration` 连续 2 次都通过
  - 验证方式: `make sqlc && make test && make test-integration && make test-integration && make lint && make web-test && (cd frontend && pnpm build)`
- [ ] P11.2 用真实模型手工验收（部署前，在本机或测试环境）：导入一个 ST PNG 卡和一个 `.krp` 包，分别用 single 和 dual 模式和 bot 聊天，确认能发表情；聊天结束 10 分钟后记忆出现在后台；再次匹配时 bot 自然地提到之前的事
  - 执行模式: HITL
  - 涉及文件: —
  - 完成标准: 用户确认验收通过
  - 验证方式: 人工
