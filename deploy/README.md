# AnonChat 部署说明

> 以代码为准，最后同步：[外站接入与用户媒体方案](../helloagents/main/plan/202611131925_external-match-bridge/how.md)（202611131925）。

本目录包含生产部署所需的 Compose 编排、Dockerfile、Caddy 配置、备份脚本与敏感词库挂载目录。

## 1. 准备

1. 服务器安装 Docker 与 Docker Compose。
2. 进入 `deploy/`，复制环境变量示例并填写真实值：

   ```sh
   cp .env.example .env
   # 编辑 .env：SITE_ADDRESS、ACME_EMAIL、POSTGRES_PASSWORD、ALLOWED_ORIGINS、
   # VERIFICATION_MODE、PHONE_ENC_KEY/PHONE_HMAC_KEY、SMS_* 等
   ```

3. 生成手机号密钥（VERIFICATION_MODE≠off 时必填）：

   ```sh
   openssl rand -base64 32   # 分别填入 PHONE_ENC_KEY 与 PHONE_HMAC_KEY
   ```

4. 放置敏感词库：把 `words.txt` 放到 `deploy/wordlist/words.txt`（格式见该目录 README）。该文件不入库。

## 2. 启动

```sh
docker compose up -d --build
docker compose ps
```

- `backend` 启动时自动执行数据库迁移；`/healthz` 用于健康检查。
- `web`（Caddy）对外发布 80/443；域名会自动申请 HTTPS，`:80` 表示前面有负载均衡。
- 前面的负载均衡/CDN：设置 `UPSTREAM_TRUSTED_PROXIES=private_ranges`（或具体 CIDR），Caddy 才会采信
  `X-Forwarded-For`；直接对公网暴露时请保持留空（不信任任何上游），否则用户可伪造来源 IP。
- 实名短信：`VERIFICATION_MODE=off` 时允许默认 `SMS_PROVIDER=console`（不发送短信）；一旦设为
  `optional|required`，生产环境必须改用真实服务商，否则启动失败。
- 只有 `web` 对外暴露端口，`postgres`、`backend`、`backup` 仅在内网。
- 媒体库：`backend` 挂载 `media` 卷到 `/data/media`，并设置 `MEDIA_ROOT=/data/media`；后台媒体库上传的
  图片/视频都在这个卷里，删容器不会丢。备份策略需要额外覆盖该卷（见第 4 节）。

## 3. 创建首个管理员

```sh
docker compose exec -it backend /server admin create --username admin
# 交互式输入两次密码（≥12 位，不回显）
```

## 4. 备份与恢复演练

- 自动备份：`backup` 服务每天 03:00 执行 `pg_dump -Fc` 到 `backups` 卷，仅保留最近 7 份。
- 手动触发一次备份：

  ```sh
  docker compose exec backup /backup/run.sh
  ```

- 恢复演练（恢复到新库）：

  ```sh
  # 1) 取最新备份文件
  docker compose exec backup ls -1t /backups/anonchat-*.dump | head -1
  # 2) 在 postgres 中新建一个库
  docker compose exec postgres createdb -U anonchat anonchat_restore
  # 3) 用 pg_restore 恢复到新库
  docker compose exec backup sh -c 'pg_restore -d anonchat_restore /backups/<dump文件名>'
  # 4) 抽查数据后删除演练库
  docker compose exec postgres dropdb -U anonchat anonchat_restore
  ```

  建议另将 `backups` 卷内容定期同步到异地存储（由运维配置）。
- 媒体文件（`media` 卷）不在 `pg_dump` 备份范围内，需要单独同步/快照。

## 5. 回滚

1. 先备份数据库、媒体卷与加密密钥，确认要回到的应用和数据库版本。外站功能仅需停用时，关闭后台总开关即可，无需回滚数据库。
2. 如确需数据层回滚，先停 backend，避免启动自动迁移将版本重新升级；用同版迁移文件和 goose 的目标版本回滚，按第 9 节的 `00010 → 00009` 注意事项处理。
3. 再把后端与前端镜像一起切回兼容版本并重启。迁移文件均包含 Down，但回滚可能删除数据，不代表无损。

**不要将 `/server migrate down` 或 `make migrate-down` 用于单步回滚：当前实现会回滚全部迁移到空库。**

## 6. AI 机器人（Bot）与媒体库配置

启用 bot 引擎需要先配置模型接入（OpenAI 兼容协议，DeepSeek / OpenRouter / 通义等均可）：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `BOT_ENABLED` | `false` | 仅首次初始化全局开关；引擎始终运行，按后台开关与就绪状态工作 |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | OpenAI 兼容接口地址（不带 `/chat/completions`） |
| `LLM_API_KEY` | 空 | 启用模型调用时需提供 |
| `LLM_MODEL` | 空 | 默认模型；启用模型调用时需提供，单个 bot 可在后台覆盖 |
| `LLM_TIMEOUT` | `60s` | 单次请求超时 |
| `LLM_SEND_CACHE_KEY` | `true` | 是否发 `prompt_cache_key`；服务商拒绝未知参数时设为 `false` |
| `BOT_MATCH_FALLBACK_AFTER` | `10s` | 排队多久仍无真人配对时由 bot 兜底 |
| `BOT_BOTTLE_MIN_AGE` | `30m` | 瓶子投出多久仍没人回复，bot 才会去回 |
| `BOT_TIMEZONE` | `Asia/Shanghai` | 提示词里显示当前时间用的时区 |
| `MEDIA_ROOT` | `./data/media` | 媒体文件根目录（compose 已覆盖为 `/data/media`，启动时自动创建） |
| `MEDIA_MAX_UPLOAD_MB` | `100` | 单个媒体文件大小上限；单次上传请求体上限为其 10 倍 |

**配置来源与热切换（how.md §15）**

- 上表里的 bot 相关环境变量**只用于首次初始化**：服务第一次启动时若 `bot_settings` 表为空，就用这些值写入一行；
  之后一律以管理后台「Bot → 全局设置」为准，改环境变量不再生效。
- 后台改完立即生效（无需重启）：总开关、Base URL、模型、API Key、超时、`prompt_cache_key` 开关、
  匹配兜底时间、漂流瓶最短瓶龄、时区都会在保存后刷新到引擎；未保存时每 30 秒自动重新读取一次。
- **API Key 的优先级**：后台保存的 key（数据库，AES-256-GCM 密文）> 环境变量 `LLM_API_KEY` > 无。
  后台只显示 `••••abcd` 这样的末 4 位与来源，任何接口、日志、审计都不会出现明文或密文。
- **API Key 与 Base URL 绑定**：保存 key 时会记下当时的 Base URL（规范化：去首尾空白与末尾 `/`）。
  之后只有 Base URL 与记录一致时才使用该 key；数据库里的 key 只在"当前 Base URL == 保存时的地址"时生效，
  环境变量 `LLM_API_KEY` 只在"当前 Base URL == `LLM_BASE_URL`"时生效。改地址后旧 key 自动失效
  （后台显示"Base URL 已变更，请重新填写 API Key"），必须重新填写 key 才能保存新地址
  （或明确清除 key）；「测试连接」换地址时也必须同时填新 key，不会把已保存的 key 发到别的地址。
- 新增 `SETTINGS_ENC_KEY`：后台保存 API Key 的加密密钥（base64 编码的 32 字节），生成方式：

  ```sh
  openssl rand -base64 32
  ```

  未配置时后台**不能保存** API Key（返回 `invalid_request`），其余设置照常可保存；已保存的 key 若缺少该密钥也无法解密。
  请离线备份这个值。
- 后台还有「测试连接」按钮（每个管理员每分钟最多 10 次），会用已保存的配置发一次极短请求验证连通性。

注意：

- 不配置 `BOT_ENABLED`/`LLM_*` 时，媒体库与 bot 管理接口仍然可用，只是不会有 bot 参与聊天。
- `LLM_*` 里的密钥属于敏感信息，只能放在 `deploy/.env`（不入库）；日志中不会输出密钥与消息正文。
- 媒体文件在 `media` 卷里，异地备份时需要一并备份（例如把该卷同步到对象存储）。

## 7. 上线前人工清单（对应任务 12.3，HITL）

- 域名与服务器；ICP 备案与公安联网备案；互联网信息服务安全评估。
- 用户协议、隐私政策、社区规范（如实说明 7 天保留、阅后即焚语义与日志留存）。
- 生产敏感词库（`deploy/wordlist/words.txt`）。
- 短信服务商开通、签名与模板审核，填写 `SMS_*`；实现对应 `SMSProvider`。
- 生成并离线备份 `PHONE_ENC_KEY`/`PHONE_HMAC_KEY`。
- 创建首个管理员；配置异地备份。

> 注意：当前仓库仅实现 `console` 短信提供方（仅开发用），生产环境禁止 `console`；接入真实服务商前，
> 生产环境无法完成启动（配置校验会拒绝 console）。这属于任务 5.x / 12.3 的 HITL 事项。

> **人设注入**：运行时固定使用 `user`，旧 `system` 配置自动按 `user` 处理，后台不再提供切换选项。身份表述不被检测或改写。如果用的是思考型模型（例如 deepseek-flash），请在 bot 设置里把"输出 token 上限"调到 1000 左右，否则推理过程可能把额度用完，导致没有回复。

## 8. 人设包、双核与长期记忆（00009）

参见 [方案与接口](../helloagents/main/plan/202611092002_bot-persona-memory/how.md) 和 [任务状态](../helloagents/main/plan/202611092002_bot-persona-memory/task.md)。

- **迁移 `00009_bot_persona_memory.sql` 随 backend 启动自动执行**，包含角色卡扩展、表情、画像、事件、游标和用量类型；无需另启服务或新增环境变量。更新前先执行第4节的数据库备份，同时备份媒体卷和 `SETTINGS_ENC_KEY`。
- 在现有后台「Bot 配置 → 全局设置」维护新增项；保存后刷新引擎，无需重启：

| 设置 | JSON 字段 | 默认值 / 范围 |
|---|---|---|
| 智核默认模型 | `reasonModel` | 空，回退心核模型；单个 bot 的智核模型优先 |
| 记忆模型 | `memoryModel` | 空，回退全局默认模型 |
| 记忆保留天数 | `memoryRetentionDays` | **30**，1–365天；从最后互动起算 |
| 记忆抽取间隔 | `memoryExtractEvery` | 20，5–200条；结束或空闲也会触发 |
| 跑偏检测 | `oocGuard` | true，最多重写一次，不处理 AI 身份表述 |

- Bot 默认 `coreMode=single`、`reasonModel=""`、`memoryEnabled=true`；双核每轮至少多一次模型调用，失败按本轮单核回复。用量按 reply/reason/memory/retry 统计并受限额约束。
- 表情以普通图片发送，每轮最多一张，和其他媒体共享 `conv_media_limit`，收到新输入则跳过。复制角色卡不复制表情媒体，新卡来源为手动。
- 记忆按 bot 与对方身份隔离；画像及事件在最后互动超过保留期后由清理任务删除，设置缺行时仍按30天。身份吊销后下次清理删除；已结束超过8天的抽取游标清理。后台可查看、删除或清空，敏感内容不写日志/审计。
- 部署前仍需由人工完成 P11.2 的真实模型验收；本轮最终验证只运行本机测试，不连接远程模型。

## 9. 外站接入与用户媒体（00010）

参见[方案与安全边界](../helloagents/main/plan/202611131925_external-match-bridge/how.md)、[实施状态与人工任务 P11](../helloagents/main/plan/202611131925_external-match-bridge/task.md)。本节命令供部署人员人工运行，自动化测试只使用本地伪造站点。

迁移 `00010_external_bridge.sql` 随 backend 启动执行，新增外站设置、账号绑定、影子身份与会话映射，并扩展媒体来源/归属。数据库的外站总开关与两站开关首次均为 `false`；只配置地址不会主动连接。用户媒体独立开放于匹配、漂流瓶、私聊，聊天室与故事书不支持。

### 环境配置

以下变量由服务启动时读取，修改后重启 backend；开关、匹配时机和连接上限改用后台设置，无需重启。

| 变量 | 默认值 | 作用与校验 |
|---|---|---|
| `USER_MEDIA_MAX_UPLOAD_MB` | `20` | 用户单文件上限，正整数 MB；类型按内容嗅探，仅 JPEG/PNG/GIF/WebP/MP4/WebM |
| `EXTERNAL_TIMEOUT_SECONDS` | `30` | 服务端外站 HTTP 超时，正整数秒且须在 time.Duration 范围内；不是匹配时长 |
| `EXTERNAL_CHAT2019_RAND_SERVER_URL` | `http://v1.chat2019.cn/Act/WebService.asmx/getRandServer?ServerInfo=serversdeskry&_=` | 发现 WS 地址；返回地址必须为 ws/wss |
| `EXTERNAL_CHAT2019_FALLBACK_WS` | 空 | 发现失败时的 WS 备用地址；使用时检查 ws/wss 与地址守卫 |
| `EXTERNAL_CHAT2019_IMG_SERVER_URL` | `http://v1.chat2019.cn/asmx/method.asmx/getImgServer` | 图片服务器发现地址；返回主机不得含路径 |
| `EXTERNAL_WANFENG_BASE_URL` | `https://wanfengniming.store` | 合法 HTTP(S) 站点根地址 |
| `EXTERNAL_WANFENG_WS_URL` | 空 | 默认按 BaseURL 和协议 WSPath（默认 `/ws`）推导；显式值须为 ws/wss |
| `EXTERNAL_WANFENG_HTTP_PROXY` | 空 | 单一 http/https/socks5 代理 URL，须有主机，不含业务路径、query、fragment；留空直连 |
| `EXTERNAL_WANFENG_PROTOCOL_OVERRIDES` | 空 | Protocol 覆盖 JSON；语法由配置加载校验，字段类型由客户端装配校验 |
| `EXTERNAL_WANFENG_NEW_IDENTITY_PER_HOUR` | `10` | 正整数，全进程注册令牌桶容量及每小时补充量；账号落库复用不消耗 |
| `EXTERNAL_WANFENG_MEDIA_HOSTS` | BaseURL 主机 | 逗号分隔的媒体主机名，不填 scheme、端口或路径；总白名单合并 BaseURL 与显式 WSURL 主机 |
| `SETTINGS_ENC_KEY` | 空 | 复用已有 base64 32 字节密钥，通过项目 secretbox 辅助包（AES-256-GCM）加密晚风 token；缺失时晚风不可用，chat2019 不受影响 |

`MEDIA_ROOT` 仍为存储根目录（compose 中 `/data/media`），`MEDIA_MAX_UPLOAD_MB=100` 控制媒体库和外站落地文件上限，用户上限单独由 `USER_MEDIA_MAX_UPLOAD_MB` 控制。非媒体库媒体超过 24 小时且没有未焚毁消息引用时由清理任务删除。备份必须覆盖媒体卷及 `SETTINGS_ENC_KEY`；丢失密钥将无法复用已加密的晚风 token。

chat2019 **永远直连且禁止重定向**。晚风直连时解析并检查目标 IP，再直接拨号到已检查的 IP；代理模式改为主机名白名单，并检查 HTTP、WS、媒体及重定向的目标。**代理侧 DNS 解析不受本机控制**：即使主机名在白名单中，代理端解析被篡改仍有访问错误地址的风险；只填可信公网域名，维护可信代理及其 DNS，不使用宽泛或不受控白名单。

### 探测工具与启用顺序（人工执行）

生产 distroless 镜像只有 `/server`，没有 shell、Go 或 probe。在**部署机同一版本源码**的 `backend/` 中用 Go 1.27.3 编译独立工具（或在构建机按部署机 OS/架构交叉编译后传入），放在仓库外：

```sh
cd /path/to/AnonChat/backend
mkdir -p "$HOME/.local/bin"
CGO_ENABLED=0 go build -trimpath -o "$HOME/.local/bin/anonchat-probe-external" ./cmd/probe-external
```

1. 后台保持总开关关闭，先由人工在部署机以与服务相同的出口运行探测，分别确认连接、配对耗时、图片发送/接收表现及风控。工具不连数据库，默认 `-n 1 -match 30s`；`-n`、`-match` 必须为正数。工具输出脱敏后的 JSON Lines 帧及逐身份汇总：`connectMs`、`connected`、`matched`、`matchMs`、`errorType`。

   ```sh
   "$HOME/.local/bin/anonchat-probe-external" -provider chat2019 -n 1 -match 30s
   "$HOME/.local/bin/anonchat-probe-external" -provider wanfeng -n 1 -match 30s \
     -state "$HOME/.anonchat-probe/state.json" -send-image /path/to/test.png
   # 仅晚风支持代理；按实际服务出口选择，不要反复换身份测试风控。
   "$HOME/.local/bin/anonchat-probe-external" -provider wanfeng -n 1 -match 30s \
     -state "$HOME/.anonchat-probe/state.json" -proxy http://proxy.example.com:8080
   ```

   工具读取与服务相同的 `EXTERNAL_CHAT2019_*`、`EXTERNAL_WANFENG_BASE_URL`、`EXTERNAL_WANFENG_WS_URL`、`EXTERNAL_WANFENG_PROTOCOL_OVERRIDES`、`EXTERNAL_WANFENG_MEDIA_HOSTS` 环境变量；它不会自动读取 `deploy/.env`，代理须显式传 `-proxy`，连接及 HTTP 操作限时 10 秒，观察时长由 `-match` 决定，不读取服务端 `EXTERNAL_TIMEOUT_SECONDS`。`-send-image` 只接受 JPEG/PNG/GIF/WebP，配上后发一张；工具不会下载入站媒体，应结合对方实际收发和帧信息做人工验收。

   晚风使用 MemoryBindingStore，并即时保存到 `-state`（默认 `~/.anonchat-probe/state.json`），重跑复用 `probe-N` 身份，避免重复注册。**状态含明文 token，不是服务数据库密文**：文件必须为 `0600`、所在专用目录 `0700`，使用临时文件加 rename；拒绝仓库内路径、符号链接绕过、状态文件本身为符号链接、权限过宽或损坏 JSON。工具创建缺失的私有目录，不会替你修改既有共享目录权限。保留该文件、私下备份，不入库、不输出 token。
2. 探测结果经人工确认后，在后台「外站接入」只打开一个站点，先设低连接上限，再打开总开关。初始数据库值：在线阈值 5、本站等待 10 秒、外站匹配 10 秒、两站连接上限各 20；这些不是已验证的生产安全容量，人工应据出口探测结果调低起步。
3. 观察同页每 10 秒刷新的状态：在线/排队、启用/可用及原因、连接/匹配/会话/空闲数、冷却截止、最近错误及计数（包括媒体/文本失败）。也可由已登录管理员读取 `GET /api/admin/external/status`。
4. 状态稳定后逐步增加连接上限，再单独验证另一个站点；避免同时开两站且直接使用高容量。外站总开关开启后 Bot「匹配时间」不再控制兜底，兜底时机由本站等待与外站匹配阶段决定；关闭后恢复原 Bot 时机。

### 外站停用与 00010 回滚

- **优先停用而不回滚**：后台关闭总开关保存后立即生效，停止正在匹配的连接，结束所有进行中的外站会话并关闭连接；不是只影响新匹配。已有本站真人与 bot 会话不因此结束。
- 确需回到旧版本时，先停止新增写入并备份数据库、媒体卷及密钥，停 backend（避免自动 up），再由人工使用同版 SQL 和 goose 的目标版本能力回到 `00009`。例如已安装 goose CLI 时，在源码根目录使用 `goose -dir backend/migrations postgres "$DATABASE_URL" down-to 9`；先核实连接与版本，不在共享测试库或生产库试跑。
- `00010` 的 Down **删除所有 `source <> 'library'` 媒体行**，相关消息 `media_id` 因外键变为 NULL；保留媒体库行并恢复 folder 非空约束。四张外站表被删除，账号绑定和外站映射丢失；`identities` 中已有影子身份与既有会话/消息不会被这段 Down 一并删除。
- SQL 不删除磁盘文件。回滚完成后，在确认备份、实际 `MEDIA_ROOT` 与无新进程写入后，由人工清理 `MEDIA_ROOT/user` 和 `MEDIA_ROOT/external`（compose 对应媒体卷内 `/data/media/user`、`/data/media/external`）；不要删除媒体库目录。
- 后端与前端一并回滚，避免新前端继续向旧后端上传/发送用户媒体；保留数据库中的媒体 UUID 无法恢复已删除文件。不要执行 `/server migrate down` 或 `make migrate-down`，它们会回滚到空库。
