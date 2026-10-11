# norma-supply-chain：核实外部 Agent SDK 的供应链现状

- 状态：archived
- 类型：标准（依赖与构建约定）
- 关联能力：开发规范协作（现行规范 [development](../../../../specs/development/spec.md)）；依赖约束落在 [项目约束](../../../../PROJECT.md)

## 目标与范围

- 问题与当前行为：`github.com/Autumn-27/norma v0.4.3` 是硬依赖——88 个 Go 文件、11 个子包直接 import（`agent/` 36、`server/` 36，另有 `sidequestion/`、`guard/`、`llmrec/`、`llmpool/`、`mcphttp/`、`db/`、`traffic/`），`cmd/artex` 只能经由它构建。仓库既没有 `vendor/`，也没有 `replace`，更没有"上游不可用时怎么办"的约定。
- 预期结果：确认该依赖当前能否稳定获取与构建，并把结论与恢复路径写成可执行的约定，避免以后临时改依赖救火。
- 范围：依赖可用性核验、`spec/PROJECT.md` 的依赖约定、CHANGELOG 记录。
- 不做的内容：不 vendor 依赖树、不把 norma 源码复制进仓库、不新增 `replace`、不改 `go.mod`/`go.sum`、不升级 norma 版本、不改任何业务代码。
- 依据与待确认事项：模块自带 `go.mod`（Go 1.26.3，依赖仅 `creack/pty`、`hinshun/vt10x`、`golang.org/x/net`、`gopkg.in/yaml.v3`，无 cgo）；`go.sum` 中 norma 只有 v0.4.3 两行；模块内没有 LICENSE 文件，README 也未声明许可证——归属与授权需维护者确认，本次不擅自补声明。

## 需求增量

### ADDED：SUP-001 外部 Agent SDK 的获取与恢复约定

外部 Agent SDK norma 按"准 vendored 依赖"维护：版本只在 `go.mod` 变更，升级必须显式改动并跑通构建与 CI；不把它的源码复制进仓库；上游或代理不可用时，用自有 fork 加 `replace` 指向固定版本恢复，禁止静默换源。该依赖的取用能力必须可重复核验。

#### 场景：核验依赖仍可取用

- GIVEN：一个空的模块缓存目录。
- WHEN：把 `GOMODCACHE` 指向该目录并执行 `go mod download github.com/Autumn-27/norma`。
- THEN：从模块代理成功取得元数据、`go.mod` 与源码包，命令以 0 退出。

#### 场景：上游不可用时的恢复

- GIVEN：上游仓库或标签消失，代理也不再提供该模块。
- WHEN：按约定恢复。
- THEN：先把该模块 fork 到自有账号并按原版本打标签，再在 `go.mod` 用 `replace` 指向 fork 的同一版本，重新构建与运行测试；不通过就地修改依赖源码的方式绕过。

## 设计与影响

- 实现入口与方案：只改 `spec/PROJECT.md` 的依赖约定，并新建本变更记录承载核验证据；不触碰代码与依赖清单。
- 关键取舍：不 vendor——当前可取且 CI 已证明干净 runner 能拉取；vendor 是全树操作，会把 sqlite 这类大包一并复制进仓库，收益不抵仓库膨胀。不预先加 `replace`——`replace` 指向自有 fork 需要先有 fork，属应急手段；现在就换反而增加维护面。两份结论都以本次实测为依据。
- API / 数据 / 配置 / 兼容性影响：无接口、数据结构、配置项变化。
- 失败、并发与边界：`proxy.golang.org` 不可达（内网、断网）时构建依赖已预热的模块缓存或自定义 `GOPROXY`，README 已有说明，本次不重复。
- 迁移与恢复方式：无迁移。恢复路径写入约定，任何人可按场景执行。
- 适用规范及例外理由：按 WORKFLOW 标准流程记录；纯文档规则变更，无业务代码测试要求。

## 任务

- [x] 1. 核验依赖可达性与可构建性，确定是否 vendor/replace（SUP-001）。
- [x] 2. 在 `spec/PROJECT.md` 写入依赖获取与恢复约定（SUP-001 两个场景）。
- [x] 3. 同步 CHANGELOG，检查文档链接与差异。
- [x] 4. 归档变更记录并单独提交本次小项。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| SUP-001 依赖可取（空缓存） | 仓库根目录，`GOMODCACHE` 指向空目录后 `go mod download -x github.com/Autumn-27/norma` | `.info`/`.mod`/`.zip` 三次请求全部 200，退出码 0 | — |
| SUP-001 使用中子包可编译 | 任务专用缓存下 `go build` 项目实际 import 的 11 个子包 | 全部编译通过，退出码 0 | — |
| SUP-001 干净环境可取可编译 | GitHub Actions container 工作流运行 [#3](https://github.com/x0rz4/Artemis/actions/runs/38165644593)（提交 `1ee0664`，2026-10-11） | success | 仅覆盖镜像构建，不含运行时与发布 |
| 依赖面 | 子代理探查全部 import 点与 API 面 | 88 个文件、11 个子包，无统一门面，替换属大工程 | — |
| 模块版本与摘要 | `go list -m` 查询 norma | `v0.4.3`，`h1:DnmOhJPL6OZSMnCVYO5Ujh4yVINuDarEickVi9jx1hI=` | — |
| 许可证现状 | 读取模块目录清单与 README | 模块内无 LICENSE 文件，README 未声明许可证 | 需维护者确认，本次不补声明 |
| 文档 | 相对链接检查、`git diff --check` | 链接目标存在；`git diff --check` 退出码 0；`spec/PROJECT.md` 与 CHANGELOG 的差异仅本次内容 | — |

## 收尾

- 现行规范与通用文档更新位置：`spec/PROJECT.md` 的「构建与配置」段加入依赖获取与恢复约定；CHANGELOG 在 `[Unreleased]` 记录核验结论。本次不涉及能力行为，未新建 `specs/` 规范。
- 剩余限制或阻塞：`replace` 恢复路径需要维护者先创建自有 fork，本次未创建；norma 模块内没有 LICENSE 文件、README 未声明许可证，本次只记录事实，未补声明。未覆盖：未在真实断网环境验证恢复流程，未做运行时验证（CI 仅证明可编译）。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-norma-supply-chain/`。
