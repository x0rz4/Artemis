# Artemis 开发指南

Go 后端、PostgreSQL、Next.js 静态前端。产品名 **Artemis**（可执行文件 `artemis.exe` / `artemis`，页面品牌、日志、导出文件名统一用它）；基于 ARTEX，Go module/import（`github.com/Autumn-27/artex`）和 `ARTEX_*` 环境变量是保留的内部兼容名称，不是产品名。自更新发布源是 `selfupdate/github.go` 里的 `Repo` 常量，当前为占位值 `TO_BE_SET`——未填写时检查更新会直接返回"更新源未配置"并拒绝联网。

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
| 发布与更新 | `build.sh`、`scripts/package-release.ps1`、`.github/workflows/release.yml`、`selfupdate/github.go`（`Repo` 占位值） |
| 产品命名 | 品牌串只在 `web/src/config/app-config.ts`、登录/初始化页、侧边栏、`cmd/artex/main.go` 的 banner 与日志；二进制与包名在 `build.bat`、`build.sh`、`start.bat`、`start.sh`、`Dockerfile`、`scripts/package-release.ps1` |
| 工具库 | `server/toollib.go`（清单与导入）、`server/toollib_upload.go`（上传）、`server/toollib_check.go`（自检）、`server/toollib_command.go`（审计用命令渲染）、`web/src/app/(main)/system/tool-lib/page.tsx` |

提示词种子只初始化空版本；更新默认提示词需对应迁移标记，保留用户定制。外部 Agent SDK norma 版本见 `go.mod`。

## 工具库（data/tools）

运行时武器库的**真源是磁盘**：`data/tools/` 下的文件 + `manifest.json`。约定不要破坏：

- **单向同步**：导入（页面按钮 / 上传）把清单条目 upsert 成 `tools` 表里 `kind=command` 的自定义工具行；在「系统 → 工具」编辑工具**不回写**清单。反向写会让两边都能当权威，早晚漂移。
- **只告警不阻断**：文件缺失、sha256 不符、runtime 未安装只在自检结果与启动日志里标出，不阻止启动、也不阻止其它条目导入（武器库不全时平台仍要能跑）。
- **执行层零改动**：`kind=command` 复用 Bash 底层 run，所以脚本（python3/node/php）、JAR、原生二进制都靠命令模板包装，不需要给执行器新增 runtime 分支。模板里的路径是**导入时渲染的绝对路径**——移动 data 目录后重新导入即可修复。
- **清单条目不写 agents 时默认绑全部 agent**：没有任何绑定的自定义工具对每个 agent 都不可见（见 `server/assembly.go` 的 `ToolResolve`）。
- **审计可见性**：自定义 command 工具以自身 key 被调用，guard 从输入里读不到命令行，靠 `intercept.CommandRenderer`（`server/toollib_command.go` 接线）渲染出真实命令写进审计，并以 `_command` 合并进规则匹配的输入。新增模板型工具类型时别绕开这条链路。
- 清单位于 `data/`（运行数据），不进发布包、不进 git；`manifest.json.bak` 是上一版备份。

## 验证与发布

按改动运行类型检查、相关 Go 测试、构建及 vet。数据库测试使用隔离 PostgreSQL，禁止连接开发者业务数据库。正式包验证首次初始化、鉴权和空业务列表。

排除凭据、运行数据、日志、抓包、备份、内部交接记录和个人机器路径。README 可保留明确标注的模拟截图，截图数据不得导入应用。保留 LICENSE、字体许可与来源说明；不强制推送或改写已有远端历史。
