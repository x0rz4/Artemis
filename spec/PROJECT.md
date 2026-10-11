# Artemis 项目约束

Go 后端、PostgreSQL、Next.js 静态前端。产品命名与兼容标识见 [统一风格规范](STYLE.md)。自更新发布源是 `selfupdate/github.go` 里的 `Repo` 常量，写死为 `x0rz4/Artemis`，不得做成配置项；置空或保留占位值时检查更新只返回"更新源未配置"并拒绝联网。换源必须同时改 `Repo`、`allowedHosts` 域名名单，并确认该源的资产命名与校验和口径一致。

## 构建与配置

- Windows：`build.bat` 安装依赖、导出静态资源、内嵌生成 `artemis.exe`。发布包使用 `scripts/package-release.ps1 -Version 2.3.0`（产物 `dist/artemis-<版本>-windows-amd64.zip`）。
- Bash：`./build.sh --target linux/amd64`。使用 `ARTEX_BUILD_VERSION` 指定内嵌版本。
- Go 最低版本见 `go.mod`；前端使用 Node.js 22、npm 锁文件。
- 正式构建必须使用 `-tags embedui` 和已生成的静态资源。
- `config.example.json` 复制为 `config.json`，填写自己的 PostgreSQL；没有默认数据库密码。
- `ARTEX_PG_DSN` 优先于文件数据库配置；`ARTEX_CONFIG` 指定配置文件，否则先查工作目录，再查程序目录。
- `ARTEX_SKILL_DIR` 优先于文件 `skill_dir`，留空使用程序旁的 `skills/`。
- 模型在 UI 配置；API Key 与 `ARTEX_LLM_*` 环境变量是可选项。
- 开发后端 `go run ./cmd/artex`；前端 `cd web && npm run dev -- --port 5173`，API 默认反代 8787。
- 新数据库只初始化内置工具、Agent、提示词、拦截规则和系统设置；禁止初始化示例业务、预置模型或模拟认证。

## 修改入口

| 内容 | 位置 |
| --- | --- |
| 产品元数据 | `web/src/config/app-config.ts` |
| API 与类型 | `web/src/lib/api.ts`、`web/src/lib/types.ts` |
| 后端路由 | `server/server.go` 和对应 handler |
| 幂等迁移 | `db/schema.sql`、`db/db.go` |
| 提示词 | `agent/promptcatalog.go`、`agent/worker.go`、`server/orchestration.go` |
| 工具定义与绑定 | `agent/toolcatalog.go`、`agent/tools*.go`、`server/orchestration.go` |
| 发布与更新 | `build.sh`、`scripts/package-release.ps1`、`.github/workflows/release.yml`、`selfupdate/github.go`（`Repo` 发布源） |
| 工具库 | `server/toollib.go`（清单与导入）、`server/toollib_upload.go`（上传/安装器）、`server/toollib_check.go`（自检）、`server/toollib_command.go`（审计用命令渲染）、`server/toollib_sync.go`（多源同步）、`web/src/app/(main)/system/tool-lib/page.tsx` |
| 库源同步底座 | `server/synclib.go`（按源派生白名单、令牌、下载校验原子落盘）——工具库与技能库共用，新增源类型只改这里 |
| 官方云更新 | `server/registry.go`（固定索引、清单、后台检查）、`server/registry_install.go`（选择安装、活动门禁）、`web/src/components/library-updates.tsx`；发布源码在独立公开仓库 `x0rz4/Artemis-registry` |
| 技能库 | `server/skillsrc.go`（多扫描根 + 源同步）、`server/server_mgmt.go` 的 `fs*Skill*`、`web/src/app/(main)/system/skills/_components/skill-sources.tsx` |
| 观测与日志 | `logging/logging.go`（结构化入口与字段约定）、`server/logsink.go`（文本契约、入库与 SSE）、`web/src/app/(main)/system/logs/page.tsx` |

提示词种子只初始化空版本；更新默认提示词需对应迁移标记，保留用户定制。外部 Agent SDK norma 版本见 `go.mod`，按准 vendored 依赖维护：升级必须显式改 `go.mod` 并跑通构建与 CI，不假设上游同步；不把它的源码复制进树（模块不带许可证文件）；上游或代理不可用时用自有 fork 加 `replace` 指向同一版本恢复，不就地改依赖源码。核验仍可取用：把 `GOMODCACHE` 指向空目录后执行 `go mod download github.com/Autumn-27/norma`。

## 观测与日志

后端日志是「一行一条文本」的契约：`server/logsink.go` 把标准库 `log` 的每一行与 `logging` 包的结构化记录一起汇入 stderr、内存环、`server_logs` 表并推 SSE；「系统 → 日志」页面按 `level`/`tag`/`text` 三列消费，**不要**改成 JSON，也不要改动这三列的含义。

- 新代码用 `logging` 包：`logging.Task/Intent/Tool/Decision` 派生 logger，级别与标签由调用方显式给出；输出仍是 `[tag] 消息 key=value …`，含空白的值加引号。
- 字段约定：`tag` 是事件来源（task/deadline/worker/planner/activity/tool/decision/mcp 等），`task`、`intent`、`tool`、`decision` 是关联标识，错误用 `err`；`decision` 取 allow/deny/ask/timeout/model_fallback/block。
- 决策日志只记非放行结果（拦截、拒绝、审批请求、超时、模型兜底）与决策记录落库失败；放行是常态，不逐条刷日志。
- 不把工具命令行与输入原文写进日志（可能含凭据与业务内容）；多行内容先截断成单行字段再入库。
- 级别开关是环境变量 `ARTEX_LOG_LEVEL`（`debug|info|warn|error`，缺省 `info`）：结构化日志在 `logging` 里过滤，标准库 `log` 的行由 `server/logsink.go` 用同一阈值过滤——两条路径共用一个阈值，否则调级别只会让一半日志安静下来。写错的值回落 `info` 并在 stderr 提示，启动时也会报一句当前阈值。
- 历史调用点仍走标准库 `log`（级别由 `levelOf` 猜），属过渡状态；迁移时不要顺手改无关文案。

## 工具库（data/tools）

运行时武器库的**真源是磁盘**：`data/tools/` 下的文件 + `manifest.json`。约定不要破坏：

- **单向同步**：导入（页面按钮 / 上传）把清单条目 upsert 成 `tools` 表里 `kind=command` 的自定义工具行；在「系统 → 工具」编辑工具**不回写**清单。反向写会让两边都能当权威，早晚漂移。
- **只告警不阻断**：文件缺失、sha256 不符、runtime 未安装只在自检结果与启动日志里标出，不阻止启动、也不阻止其它条目导入（武器库不全时平台仍要能跑）。
- **执行层零改动**：`kind=command` 复用 Bash 底层 run，所以脚本（python3/node/php）、JAR、原生二进制都靠命令模板包装，不需要给执行器新增 runtime 分支。模板里的路径是**导入时渲染的绝对路径**——移动 data 目录后重新导入即可修复。
- **清单条目不写 agents 时默认绑全部 agent**：没有任何绑定的自定义工具对每个 agent 都不可见（见 `server/assembly.go` 的 `ToolResolve`）。
- **审计可见性**：自定义 command 工具以自身 key 被调用，guard 从输入里读不到命令行，靠 `intercept.CommandRenderer`（`server/toollib_command.go` 接线）渲染出真实命令写进审计，并以 `_command` 合并进规则匹配的输入。新增模板型工具类型时别绕开这条链路。
- 清单位于 `data/`（运行数据），不进发布包、不进 git；`manifest.json.bak` 是上一版备份。
- **多源同步是并集**：本地独有的条目不会被源抹掉，源里删掉的条目也不删本地文件。同名条目按源刷新 version/sha256/path/kind/runtime/entry，**保留**本地的 key/agents/enabled/deferred（平台侧状态不是源能决定的）。
- **落地顺序是文件先、清单后**：先原子替换文件再写清单，反过来会出现清单声称的版本在磁盘上不存在。覆盖安装走 `swapDir`（旧目录先改名，失败可退回）。
- **摘要必须钉**：源条目 sha256 为空时默认拒绝安装，除非该源显式开启 `allow_unpinned`；未钉摘要/版本的条目每次同步都会重装。

## 技能库（data/skills + 主 skills/）

- 扫描根按优先级：显式配置的 `ARTEX_SKILL_DIR` / `skill_dir` → 清单激活的官方 `data/skills/managed/` 版本 → 程序旁的内置 `skills/` → `data/skills/uploaded` → `data/skills/git/<源名>`。**同名以靠前的根为准**。官方更新经用户确认可优先于内置；显式本地根始终优先。每轮 Agent 装配动态解析扫描根，不能在启动时缓存根列表。
- 写入落在该 skill 当前的根；**新建与上传默认进 `data/skills/uploaded`**，不污染程序目录。`s.skillPath(name)` 是唯一入口，不要在 handler 里手拼 `filepath.Join(s.skillDir, ...)`。
- 源类型：`git`（`git clone --depth 1` / `fetch` + `reset --hard`，目录是远程镜像，本地改动会被覆盖，需要本机有 git）、`github`/`http`（拉一份 zip 清单，逐个下载校验后走 `installSkillZipBytes`）。
- 令牌同样存 settings（`skill_source_token:<源名>`），不进 `data/skills/manifest.json`。
- 侧边栏「工具库」页管工具，「Skill」页头部的同步控件管技能；两者共用 `synclib.go` 的白名单与校验口径。

## 官方工具与 Skill 云更新

- 固定公开源 `x0rz4/Artemis-registry`；启动后和每六小时只检查索引与清单，可关闭。更新须用户选择条目并确认具体 release；不追踪上游 latest，不自动安装。
- 两类库的 `registry` 元数据分别保存发布版本、条目版本、安装包与内容摘要。工具原 `sha256` 仍是入口文件摘要。新目录完成校验后再切换清单，失败保留旧文件。
- 官方同步沿用 `/sync`，`names` 始终指源，`items` 指条目，`release` 固定检查过的版本，`replace` 为逐项冲突替换授权。自定义源和离线脚本继续可用。
- 同名本地内容和本地修改默认跳过，官方工具更新保留数据库的绑定、启用和 deferred，新工具默认禁用。官方 Skill 持久化目录独立于内置和上传目录，回滚清单恢复生效优先级。
- 应用与回滚在持有库写锁后获得任务/聊天空闲门禁；下载不持有活动门禁。锁顺序为库写锁 → `concMu` → `chatMu` → engine `deleteMu`，任何门禁失败返回 409。不得让任务或聊天在检查空闲后、切换完成前启动。

## 库快照与写锁（`server/snapshot.go`）

- 同步、覆盖上传、清除文件、清源、回滚**之前**都会自动生成一份快照到 `<库根>/.snapshots/<id>/`，内容 = 将被改动的目标 + `manifest.json`（清单必须一起存，否则文件与清单会对不上）。最多保留 5 份。
- 新快照保存各文件 SHA-256。回滚先复制到临时目录并校验，再逐项切换，清单最后生效；切换失败恢复旧文件。兼容未含摘要的历史快照；创建回滚前快照时不得清理用户选中的目标快照。
- 快照按库条目恢复：被恢复目录内部以快照内容为准，快照未包含的其它库条目不删。回滚前会再存一份 `pre-rollback:` 快照，避免"回滚错了"不可逆。
- 接口：`GET /api/tool-lib/snapshots`、`POST /api/tool-lib/rollback`、`GET /api/skills/snapshots`、`POST /api/skills/rollback`（`id` 缺省用最近一份）。工具回滚后会按清单重新注册工具行。
- 每个库一把写锁（`Server.toolLibMu` / `skillLibMu`，用 `TryLock`）：同步、上传、导入、删除、回滚互斥，第二个请求拿 409 而不是排队。**新增会改磁盘或清单的接口时记得走 `libTryLock`**，但不要在 `installSkillZipBytes`/`installToolZip`/`takeSnapshot` 内部再加锁（它们总是被已持锁的调用方调用）。

## 验证与发布

按改动运行类型检查、相关 Go 测试、构建及 vet。数据库测试使用隔离 PostgreSQL，禁止连接开发者业务数据库。正式包验证首次初始化、鉴权和空业务列表。

排除凭据、运行数据、日志、抓包、备份、内部交接记录和个人机器路径。README 可保留明确标注的模拟截图，截图数据不得导入应用。保留 LICENSE、字体许可与来源说明；不强制推送或改写已有远端历史。
