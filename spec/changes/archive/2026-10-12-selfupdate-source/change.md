# selfupdate-source：固定一键更新的发布源

- 状态：archived
- 类型：重点（发布更新行为）
- 关联能力：自更新（新增，验证完成后建立 `spec/specs/self-update/spec.md`）

## 目标与范围

- 问题与当前行为：`selfupdate/github.go` 的 `Repo` 是占位值 `TO_BE_SET`，`repoConfigured()` 为假，`FetchLatest` 直接返回"更新源未配置"且不发起任何请求。因此正式发行版的页面一键更新、顶栏版本提示与回滚入口全部不可用——`server/update.go` 的 `/api/update/check` 会把这个错误原样交给前端。
- 预期结果：发布源指向本仓库 `x0rz4/Artemis`，检查更新走 GitHub 正式 Release；发布源为空或仍是占位值时保持"明确报错、不联网"的兜底，供 fork 维护者使用。
- 范围：发布源常量与维护者注释、发布源可用性测试、`spec/PROJECT.md` 的约束描述、README 发行包更新说明、能力规范与索引。
- 不做的内容：不改 `allowedHosts` 白名单，不把发布源做成运行时配置项，不改下载/校验/暂存/换装逻辑，不改「版本与更新」卡片的错误文案，不推送标签、不触发 release 或容器工作流。
- 依据与待确认事项：
  - 资产命名与校验和已核对代码：`build.sh:101` 去掉 `v` 前缀、`build.sh:168` 产出 `artemis-<版本>-<os>-<arch>.zip`，`.github/workflows/release.yml:94` 生成 `SHA256SUMS` 并随 zip 一起发布，与 `AssetName`（`github.go:142`）和 `stage.go` 的取用口径一致。
  - API 实测（2026-10-12）：`/repos/x0rz4/Artemis` 返回 200（公开、未归档），`/releases/latest` 返回 404，即仓库尚无正式 Release；远端也没有 `v2.3.0` 标签（本地有标签但未推送）。填好发布源后，"仓库尚未发布任何正式版本"将成为最常见状态，需要维护者先发布正式 Release。

## 需求增量

### ADDED：SU-001 发布源固定且可校验

一键更新的发布源必须写死在本仓库，指向 `x0rz4/Artemis` 的 GitHub 正式 Release，不得做成运行时配置项。发布源地址必须同时满足：HTTPS、主机在升级链路白名单内、Release 资产命名与打包脚本一致、随包提供 `SHA256SUMS`。换源（例如自建 fork）必须改代码并同步白名单，改错时由测试直接失败而不是等到用户点更新。

#### 场景：正式版本检查更新

- GIVEN：正式发行版二进制，发布源为本仓库。
- WHEN：请求 `/api/update/check`。
- THEN：请求发往 `https://api.github.com/repos/x0rz4/Artemis/releases/latest`，响应中 `repo` 为 `x0rz4/Artemis`；发布源存在正式 Release 时返回最新版本、资产名与资产是否可用。

#### 场景：发布源写错

- GIVEN：`Repo` 被改成不符合约定形式的值（缺斜杠、带路径、指向非白名单域名）。
- WHEN：运行 `selfupdate` 包测试。
- THEN：针对发布源的测试失败并指出问题，不需要联网。

### ADDED：SU-002 未配置发布源时拒绝联网

发布源为空或仍是占位值 `TO_BE_SET` 时，检查更新必须返回可读错误并停留在本地，不得向任何地址发起请求；该分支保留给 fork 与自建发布源维护者。

#### 场景：占位值未替换

- GIVEN：`Repo` 为空或为 `TO_BE_SET`。
- WHEN：调用 `FetchLatest`。
- THEN：立即返回"更新源未配置"错误，不访问网络。

### ADDED：SU-003 检查更新与版本比较

`/api/update/check` 以服务端为准查询发布源：默认命中 30 分钟缓存，失败结果缓存 2 分钟，显式检查可绕过缓存；返回当前版本、运行模式（二进制或容器）、当前平台对应的资产名、资产是否可用与是否有更新。当前版本不是纯净三段式版本号（开发构建）时不提供一键更新，并给出原因。

#### 场景：容器部署

- GIVEN：进程运行在容器内。
- WHEN：请求 `/api/update/check`。
- THEN：`mode` 为 `docker`，前端据此说明容器更新走镜像重建。

### ADDED：SU-004 下载校验、暂存与换装回滚

一键更新只接受同时提供本平台 zip 与 `SHA256SUMS` 的正式 Release：下载后比对 SHA256，解出可执行文件并冒烟测试，通过后暂存为新版本；下次启动时校验暂存件并换装，旧版本备份为可回滚文件，连续 3 次启动未能确认稳定则自动回滚，失败的版本保留供排查；也支持主动退回上一版本。

#### 场景：校验和缺失或摘要不符

- GIVEN：Release 缺少 `SHA256SUMS`，或下载内容与清单摘要不一致。
- WHEN：执行更新。
- THEN：拒绝安装并报告原因，当前版本不被替换。

## 设计与影响

- 实现入口与方案：改 `selfupdate/github.go` 的 `Repo` 常量并重写其维护者注释（换源需要同时改哪些东西）；新增 `selfupdate` 包内测试，钉住发布源可用性——已配置、`owner/repo` 形式、地址符合 GitHub API 形式、通过白名单校验。
- 关键取舍：继续保持发布源写死（`spec/PROJECT.md` 已定：可配置更新源等于给能改配置的人一条远程代码执行通道）；保留 `repoConfigured()` 兜底而非删除，使 fork 置空时仍是明确失败。
- API / 数据 / 配置 / 兼容性影响：`/api/update/check` 返回的 `repo` 字段由 `TO_BE_SET` 变为 `x0rz4/Artemis`；无新增配置项、无数据库与接口结构变化。SU-003、SU-004 是本次一并建立的既有行为基线，不修改代码。
- 失败、并发与边界：发布源可达但没有正式 Release 时返回明确错误（现有 404 分支）；限流（403/429）与非白名单域名按现有逻辑失败；升级过程的互斥与失败回滚不变。
- 迁移与恢复方式：无数据迁移。本地升级文件与回滚备份与本次改动无关；容器部署不走该路径。
- 适用规范及例外理由：遵循 `spec/WORKFLOW.md` 重点变更流程；不新增风格规则，界面文案沿用 `spec/STYLE.md`。
- 已知限制（记录不修）：仓库尚无正式 Release 时，检查更新会显示"无法连接发布源：仓库 x0rz4/Artemis 尚未发布任何正式版本"并附带"可在上方配置全局代理后重试"，该代理建议对这种情况不适用。这是既有提示文案问题，改为按错误类型区分属于独立改动。

## 任务

- [x] 1. 填写 `Repo` 并重写维护者注释（SU-001）。
- [x] 2. 增加发布源可用性测试，并做负向验证（SU-001、SU-002 场景）。
- [x] 3. 同步 `spec/PROJECT.md`、README 发行包更新说明与 CHANGELOG。
- [x] 4. 验证：gofmt、`go vet ./selfupdate`、`go test ./selfupdate`、相关构建与发布源实测。
- [x] 5. 建立 `spec/specs/self-update/spec.md`、更新能力索引、归档变更记录。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| SU-001 发布源实测 | 只读请求 `/repos/x0rz4/Artemis` 与 `/releases/latest`（2026-10-12） | 仓库 200（公开、未归档），`releases/latest` 404（尚无正式 Release） | — |
| SU-001 资产命名一致性 | 阅读 `build.sh:101,168`、`.github/workflows/release.yml:94`、`github.go`、`stage.go` | 去 `v` 前缀的 `artemis-<版本>-<os>-<arch>.zip` 与 `SHA256SUMS` 口径一致 | — |
| SU-001 发布源测试 | 仓库根目录 `go test ./selfupdate -count=1 -run '^TestReleaseSourceIsUsable$' -v` | 通过 | — |
| SU-001 / SU-002 负向验证 | 临时将 `Repo` 改回 `TO_BE_SET` 后重跑该用例，再复位 | 失败并提示"发布源未配置：Repo 不能为空或占位值"；复位后通过 | — |
| SU-002 不联网分支 | 阅读 `FetchLatest` 分支与 `repoConfigured` | 占位值直接返回错误，无网络请求 | — |
| Go 格式 | LF 归一化副本上 `gofmt -l` | 无输出 | 工作树为 CRLF（`core.autocrlf=true`），直接 `gofmt -l` 对全仓库文件都报警，属既有现象 |
| Go 静态检查 | `go vet ./selfupdate` | 通过 | — |
| Go 测试 | `go test ./selfupdate -count=1` | 通过；9 项因假二进制脚本仅 Unix 可跑而跳过 | 平台限制，非本次引入 |
| Go 编译 | `go build ./selfupdate`；`go build ./server ./cmd/artex`（复用 `release-work/ci-check` 的模块与构建缓存） | 均通过 | C: 仅剩 0.2 GB，默认模块缓存下 sqlite 解压失败；`go build ./...` 另被未清理的 `release-work/` 目录干扰，均属既有环境问题 |
| 文档与差异 | 相对链接人工检查、`git diff --stat` | 5 个文件改动，新增文档链接指向存在 | — |

## 收尾

- 现行规范与通用文档更新位置：新建 `spec/specs/self-update/spec.md`（SU-001~SU-004）并登记到 `spec/README.md` 能力索引；`spec/PROJECT.md` 更新发布源取值与换源要求；README 补充发行包一键更新说明；CHANGELOG 在 `[Unreleased]` 记录发布源确定。
- 剩余限制或阻塞：仓库尚无正式 Release（远端无标签，`/releases/latest` 实测 404），需要维护者推送 `v2.3.0` 标签并手动运行 release 工作流后，一键更新才真正可用；本次未推送标签、未触发任何工作流。发布源为空时的提示文案与「版本与更新」卡片"无法连接发布源…可配置全局代理"的措辞不匹配（既有文案问题），本次记录不修。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-selfupdate-source/`。
