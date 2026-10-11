# ghcr-container-install：GHCR 镜像发布与一键安装

- 状态：implementing
- 类型：重点（发布与安装行为）
- 关联能力：容器交付（新增，验证完成后建立现行能力规范）

## 目标与范围

- 当前 Dockerfile 依赖宿主机预编译二进制，Compose 指向 `artemis:local`，没有镜像发布工作流和 install.sh。
- 用户要求参考 mhtsec/ARTEX 的 CI 和 install.sh，明确选择 GHCR。
- 增加源码镜像构建、双架构构建/发布、Docker 与本地安装、安装验证和使用文档。
- 不修改 Norma、业务执行器、数据库种子、自更新发布源，不自动提交、推送或触发远端发布。
- 工作期间发现仓库新增统一 SPEC 工作流，补建本记录并按其要求继续验证，保留并行编辑的规范文件。

## 需求增量

### ADDED：DEP-001 从源码构建正式镜像

镜像必须使用锁文件、Node.js 22 和符合 go.mod 的 Go 编译内嵌前端的 Artemis，支持 linux/amd64 与 linux/arm64；不得包含宿主机凭据、业务数据、抓包或备份，必须保留许可证。

- GIVEN：干净源码，无 dist 预编译文件。
- WHEN：执行 Docker 构建。
- THEN：镜像中的 /app/artemis 能启动并提供静态初始化页；数据保存在独立 PostgreSQL 与运行目录。

### ADDED：DEP-002 测试通过后发布 GHCR

PR 只构建验证；main 发布 edge，稳定版本标签发布版本标签和 latest，预发布不覆盖 latest；必须在两种架构分别通过隔离初始化、鉴权、空业务状态与持久化测试后发布多架构标签。

- GIVEN：main 推送或符合格式的版本标签，Actions 具备包写权限。
- WHEN：工作流运行。
- THEN：两种架构测试通过才更新多架构标签；任一失败不会更新正式标签。
- GIVEN：PR 或手动关闭 publish。
- WHEN：工作流运行。
- THEN：不登录 GHCR、不上传镜像。

### ADDED：DEP-003 可重复的一键安装

支持 Docker 与本地源码编译两种安装方式；必须保留已有配置，不执行 .env 内容；新数据库密码随机生成且不输出，密码特殊字符必须可用。Docker 模式必须在健康检查通过后才报告成功，拉取失败可选择源码构建。

- GIVEN：首次安装，未提供模型或管理员密码。
- WHEN：运行默认安装。
- THEN：生成数据库配置，启动空业务系统，通过 /setup 初始化真实管理员；不预置模型。
- GIVEN：已有 .env/config.json，或 ARTEX_PG_DSN/ARTEX_CONFIG。
- WHEN：再次安装。
- THEN：保留配置并遵循原有优先级；错误配置、构建失败或健康检查失败以非零状态退出。
- GIVEN：--no-start。
- WHEN：准备配置和构建/拉取。
- THEN：不得启动应用或数据库容器。

### ADDED：DEP-004 容器重建保留登录凭据

JWT 签名密钥必须持久化在可浏览工作区之外；重建应用容器后，未过期的原 token 必须仍可用，管理员初始化状态必须保留。已有本地安装默认密钥位置保持不变。

- GIVEN：已初始化并取得 token 的 Compose 部署。
- WHEN：保留持久卷并强制重建应用容器。
- THEN：原 token 访问任务列表返回 200，密钥不出现在 `data` 工作区。
- GIVEN：密钥目录无法创建、已有密钥无法读取或内容损坏。
- WHEN：启动服务。
- THEN：报告错误，不静默替换已有密钥。

## 设计与影响

- 后续 CI 已完成镜像构建，但重建容器后 `/api/tasks` 返回 401：密钥写入未挂载的 `/app/jwt.key`。新增 `ARTEX_JWT_KEY_DIR`（本地默认程序目录），镜像设为 `/app/auth`，Compose 将独立 `authdata` 卷挂载到该目录。目录按需创建，沿用旧 `data/jwt.key` 迁移；读写或内容错误阻止启动。
- 兼容与恢复：已有容器根目录中的密钥不会自动出现在新卷中；首次升级可重新登录，管理员密码仍在 PostgreSQL。如需保留已有会话，须在删除旧容器前将 `/app/jwt.key` 备份并放入新认证卷；后续升级保留该卷。原密钥丢失不能恢复原 token。原生部署仍使用程序旁的密钥。

- Dockerfile 采用前端、后端、运行层多阶段构建；.dockerignore 限制构建输入。容器启动仍走现有 start.sh。
- 新 container 工作流采用原生 amd64/arm64 runner，推送已测试的本地镜像，最终按 digest 合并标签；无需 Docker Hub 凭据。
- Compose 使用 PGUSER/PGPASSWORD/PGDATABASE 配合固定 DSN，避免密码 URI 转义问题；数据库默认不映射宿主机端口。保留 data/skills 挂载与 pgdata 卷。
- 本地安装复用 build.sh；Python 使用 JSON 序列化、独占创建 config.json。只在本地数据库模式添加回环端口映射。
- 兼容：ARTEX_* 保留；现有 PostgreSQL 卷和配置不迁移、不覆盖。Web 绑定默认改为回环，可用 ARTEX_BIND 显式开放。
- 失败与恢复：拉取失败可源码构建；构建/健康检查失败终止并提示日志；升级前备份，恢复需匹配数据库版本，脚本不删除运行数据或卷。
- GHCR 首次发布需确认包的 Public 可见性，已有包需授予仓库写权限。
- 首次 CI（run 38147665246）两种架构都在 Go 编译失败：技能源同步新增的 firstLine 与会话摘要同名函数冲突，以及 server_mgmt.go 的 skill import 未使用。将技能源辅助函数改为 gitOutputFirstLine、清理 import；validate 阶段增加独立 Go 编译，提前暴露编译错误。
- Compose 实际解析发现 config 输出会把美元符号转义为 $$；本地配置生成器需还原。首次安装将调用方提供的数据库参数、标签及端口正确转义后保存，避免后续脱离调用方环境时凭据发生变化。
- 相关 Go 测试编译发现 runtime_observation_test.go 的 runtimeProbe 与工具自检生产类型重名；仅将测试辅助类型改名为 runtimeObservationProbe，保持行为。
- 规范：[工作流](../../WORKFLOW.md)、[风格](../../STYLE.md)、[项目约束](../../PROJECT.md)。

## 任务

- [x] DEP-001：源码 Dockerfile、Compose、构建上下文与许可文件。
- [x] DEP-002：GHCR CI、原生双架构构建及隔离容器冒烟脚本。
- [x] DEP-003：安装脚本和配置生成器。
- [x] 修复首轮 CI 暴露的同包函数重名和未使用 import，增加镜像构建前 Go 编译检查。
- [x] 完成本地脚本、Actions、Compose 检查和 13 项安装回归测试。
- [x] DEP-004：独立认证持久卷、密钥配置与失败回归测试；保留容器重建后的原 token 验证。
- [ ] 完成真实双架构构建与隔离容器测试。
- [x] 更新 README 和来源说明。
- [ ] 全部验收通过后同步现行能力规范。
- [ ] 检查完成条件并归档。

## 验证记录

| 检查项 | 执行位置与命令 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| 安装回归 | 仓库根目录 `python -m unittest discover -s scripts -p test_install.py -v`，ARTEMIS_TEST_COMPOSE 指向临时安装的官方 Compose CLI | 13 项通过，含真实 Compose 配置解析和特殊字符密码往返 | — |
| Shell | 仓库根目录 `shellcheck install.sh start.sh`、`bash -n install.sh` | 通过 | — |
| Actions | 仓库根目录 `actionlint -shellcheck= -pyflakes= .github/workflows/docker.yml .github/workflows/release.yml` | 最终编译预检及定向测试步骤均通过语法检查 | — |
| Go 交叉编译 | 仓库根目录，分别设置 CGO_ENABLED=0、GOOS=linux、GOARCH=amd64/arm64，执行 `go build -trimpath -o <临时产物> ./cmd/artex` | 两种架构均通过；本地 Go 1.27.1，无 embedui，产物仅作编译检查 | 正式 embedui 镜像仍以 CI 为准 |
| Go 测试编译 | `go test -c -o <临时产物>/server.test.exe ./server` | 通过；另修复测试辅助类型重名 | — |
| Go 定向测试 | `go test ./server -run '^(TestSkillRootsAndWriteTarget\|TestLoadAllSkillsPrefersEarlierRoot\|TestLoadSkillRegistryFiltersByVisibility\|TestLoadAllSkillsSkipsMissingRoots\|TestValidSkillName\|TestSkillRelPath\|TestRuntimeProviderTracksOnlyActualCall)$' -count=1`，ARTEX_CONFIG 指向临时空配置、ARTEX_PG_DSN 清空 | 测试程序编译成功，但系统拒绝启动：Access is denied | 未执行测试用例；同一定向测试已接入 Linux CI，无业务数据库连接 |
| JWT 回归（2026-10-12） | `go test ./config -run '^TestJWTKeyDir$' -count=1`；`go test ./server -run '^TestJWTKey(Persistence\|Migration\|Failure)$' -count=1`；临时空 ARTEX_CONFIG、清空 ARTEX_PG_DSN | 本次均实际执行并通过，覆盖默认/指定目录、密钥复用、原 token、旧路径迁移、损坏/不可读密钥及目录创建失败 | 容器场景仍需 CI |
| 认证卷配置（2026-10-12） | 官方 Compose CLI 分别解析默认配置和 local override 为 JSON；检查 ARTEX_JWT_KEY_DIR 和 authdata 挂载；Python AST 解析 smoke_container.py | 通过；13 项安装回归和 actionlint 再次通过 | — |
| Go vet | `go vet ./server ./config ./cmd/artex` | 通过 | — |
| 文档/差异 | README、变更记录相对链接检查；`git diff --check` | 通过 | — |
| 容器构建/运行 | [首次 CI](https://github.com/x0rz4/Artemis/actions/runs/38147665246)；用户后续提供的 sha-da98b1a76521 冒烟日志 | 首次编译错误已修复；后续日志确认镜像可启动，冒烟在重建后原 token 返回 401；已增加独立认证卷修复 | 本机无 Docker daemon，本次密钥持久化修复仍待新提交 CI 验证 |

## 收尾

- 待完成真实容器验收后建立容器交付能力规范；当前不标记 verified/archived。
- 本次持久化修复尚未推送；不将未运行的容器检查或 GHCR 发布记录为通过。需要针对包含修复的新提交运行 CI，仅重跑旧提交仍会遇到原 token 失效。
- 本机系统盘空间不足曾阻止缓存写入，后续使用 release-work/ci-check 下的任务专用缓存及临时目录完成编译；未删除开发者缓存或业务数据。
