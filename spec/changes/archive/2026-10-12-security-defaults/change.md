# security-defaults：安全默认核查与收紧

- 状态：archived
- 类型：重点（安全边界、默认配置）
- 关联能力：观测之外的跨能力默认值；约束落在 [项目约束](../../../../PROJECT.md) 的「安全默认」段

## 目标与范围

- 问题与当前行为：安全默认散落在安装脚本、容器配置、拦截代码与前端默认值里，没有单一维护点，也没有一次完整核查。本次核查暴露面、鉴权覆盖、拦截失败分支、凭据与密钥权限，并对**失败时放行**的几处默认值做收紧。
- 核查结论（安全的部分）：所有 `/api/*` 走 JWT 中间件，仅 `/api/auth/*` 与 `/api/health` 免鉴权；初始化不可重复调用且读库失败返回 503（不伪装"未初始化"）；JWT 密钥随机生成、0600（Unix）、容器内独立卷；`.env` 与 `config.json` 由安装器 0600 独占创建；数据库密码随机生成且不打印；仓库无默认密码、API Key、演示账号与硬编码凭据；Compose 的 Web 端口默认回环、代理端口映射固定回环；内置拦截规则全部是 deny，没有"放行一切"的默认规则。
- 预期结果：默认只绑回环并显式告警；拦截链路的读库失败不再静默放行；模型兜底失败默认拒绝；备份包不会被误提交；以上默认值在 `spec/PROJECT.md` 有唯一维护点。
- 范围：`cmd/artex` 默认监听与告警、`intercept` 的两个读库失败分支与兜底默认动作、前端默认值对齐、忽略规则；`spec/PROJECT.md`、CHANGELOG、本变更记录。
- 不做的内容：不改鉴权机制（令牌存储、`?token=` 传参、CORS 维持现状）；不改拦截规则种子；不给代理端口加鉴权；不改 `data/`、录制树与 CA 的目录权限（需要单独变更与验证）；不为 Windows 引入 ACL 设置。

## 需求增量

### ADDED：SEC-001 默认只绑回环并告警

后端 `-addr` 默认必须是 `127.0.0.1:8787`；非回环（含空主机）绑定在启动时打印 `[security]` 告警；容器内不打印告警，因为可达性由端口映射决定。

#### 场景：裸跑发行包

- GIVEN：解压发行包后直接运行 `start.bat`/`start.sh`（不传 `-addr`）。
- WHEN：服务启动。
- THEN：只监听 127.0.0.1，局域网不可达，且不打印告警。

#### 场景：显式开放

- GIVEN：用 `-addr 0.0.0.0:8787` 启动。
- WHEN：服务启动。
- THEN：打印一条 `[security]` 告警说明对网络可达；容器内启动不打印。

### ADDED：SEC-002 拦截读库失败不得静默放行

规则读取失败时必须转为人工确认（有限等待、超时拒绝），而不是沿用"未命中"；管控工具集读取失败时必须回落到内置默认集，让受管工具继续进入规则与审批路径。

#### 场景：规则库不可读

- GIVEN：拦截规则读取返回错误。
- WHEN：受管控工具发起调用。
- THEN：产生一条人工审批请求（60 秒超时、超时拒绝），不直接执行。

### ADDED：SEC-003 模型兜底审批失败默认拒绝

模型兜底审批在模型报错、超时或输出不可解析时的默认动作必须是拒绝；需要放宽的实例由用户在「系统 → 命令拦截」显式改为 allow 或 ask。前后端默认值必须一致。

### ADDED：SEC-004 备份包不被误提交

仓库忽略规则必须覆盖备份包的扩展名，避免加密备份被提交。

### ADDED：SEC-005 安全默认有唯一维护点

`spec/PROJECT.md` 必须有一节集中记录安全默认（绑定、鉴权覆盖、拦截 fail-closed、凭据与密钥、备份），改动默认值前先看对应能力规范。

## 设计与影响

- 实现入口与方案：`cmd/artex/main.go`（`-addr` 默认值与 `isLoopbackAddr` 告警，容器内跳过）；`intercept/intercept.go`（`Match` 读规则失败转人工确认、`IsToolEnabled` 回落内置默认集、`defaultJudgeFailAction` 改 deny）；`web/.../system/intercept/page.tsx` 默认值对齐；`.gitignore` 增加 `*.dbak`。
- 关键取舍：规则读取失败选择"转人工确认"而不是"直接拒绝"——直接拒绝会把数据库抖动放大成全面停工，转人工既不静默放行、又保留人工放行的可能；审批请求自带 60 秒超时与拒绝动作，避免无人处理时长期挂起。
- API / 数据 / 配置 / 兼容性影响：无接口与数据结构变化。默认绑定变化不影响容器与安装脚本——`Dockerfile`、`install.sh` 与 Compose 都显式传 `-addr`；仅"裸跑不传参数"的路径从全网卡变为回环。模型兜底默认动作变化只影响**已开启**兜底审批但未显式设置该值的实例。
- 失败、并发与边界：`Match` 的新分支返回 RuleID 0（历史里为 NULL），审批记录与日志照常写入；`IsToolEnabled` 的回落不写缓存，读库恢复后自然回到真实设置。
- 迁移与恢复方式：无数据迁移；想恢复旧行为的实例显式设置 `llm_judge_fail_action` 或用 `-addr` 指定监听地址。
- 适用规范及例外理由：按 WORKFLOW 重点流程记录；默认值集中写入 PROJECT.md，未新建能力规范（拦截与备份的既有规范分别在 tool-library、backup-restore 与 PROJECT.md 中）。

## 任务

- [x] 1. 核查暴露面、鉴权覆盖、拦截失败分支、凭据与密钥权限（两个并行子代理）。
- [x] 2. 收紧默认绑定并加非回环告警（SEC-001）。
- [x] 3. 拦截两个读库失败分支与兜底默认动作改为 fail-closed（SEC-002、SEC-003）。
- [x] 4. 忽略规则补备份包扩展名（SEC-004）。
- [x] 5. 写入 PROJECT.md 安全默认段与 CHANGELOG，归档并单独提交（SEC-005）。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| SEC-001 默认绑定 | 阅读 `cmd/artex/main.go`、`start.sh`、`start.bat`、`Dockerfile`、`docker-compose*.yml`、`install.sh` | `start.sh`/`start.bat` 透传参数，裸跑此前落到 `:8787` 全网卡；Dockerfile 与 install.sh 均显式传 `-addr`，改默认值不影响容器与安装 | — |
| SEC-002 / SEC-003 编译与测试 | `go build ./cmd/... ./intercept/... ./guard/... ./server/...`、`go vet ./intercept ./guard ./cmd/artex`、`go test ./intercept ./guard -count=1` | 全部通过；无测试断言旧默认动作 | 新分支需要数据库夹具，未加自动化测试 |
| SEC-003 默认值口径 | `tsc --noEmit`（`web` 目录，依赖已安装） | 退出码 0 | — |
| SEC-004 忽略规则 | 阅读 `.gitignore` 与 `scripts/backup.ps1`、`backup-panel.tsx` 的包名示例 | 补 `*.dbak` 前不匹配任何规则 | — |
| 鉴权覆盖核查 | 阅读 `server/server.go` 的路由注册与 `server/auth.go` 的豁免名单 | 所有 `/api/*` 过鉴权；豁免仅 `/api/auth/*` 与 `/api/health` | — |
| 凭据与密钥核查 | 阅读 `install.sh`、`scripts/install_config.py`、`server/auth.go`、`config.example.json`、`.env.example` | 无预置凭据；密钥随机、0600（Unix） | — |
| 未修复项 | 见下方「剩余限制或阻塞」 | 如实登记 | — |
| 文档 | 相对链接检查、`git diff --check` | 退出码 0 | — |

## 收尾

- 现行规范与通用文档更新位置：`spec/PROJECT.md` 新增「安全默认」段（绑定、鉴权覆盖、拦截 fail-closed、凭据与密钥、备份、已知未覆盖）；CHANGELOG 在 `[Unreleased]` 记录。
- 剩余限制或阻塞（核查发现、本次未改）：
  - 拦截器未接线时按"规则禁用"放行，是刻意行为；接线点在 `server/manager.go`。
  - Windows 发行包无 0600 语义，密钥与配置权限依赖用户目录 ACL。
  - `data/`、流量录制树与其中的 MITM CA 私钥按 0755 创建，多用户主机上同机其他用户可读——需要单独变更与验证。
  - 流量代理端口自身无鉴权，只靠默认回环绑定与容器端口映射约束。
  - SSE 允许 `?token=` 传令牌（会进访问日志），前端令牌存 localStorage（无 HttpOnly/Secure cookie）。
  - `/api/auth/init` 在未初始化时对任何可达网络开放，首占即管理员——默认回环绑定是主要缓解。
  - 模型兜底审批的失败动作仍可被配置放宽为 allow/ask，属用户显式选择。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-security-defaults/`。
