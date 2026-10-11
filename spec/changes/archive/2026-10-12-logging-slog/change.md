# logging-slog：结构化日志底座与四类事件首批迁移

- 状态：archived
- 类型：重点（安全边界与跨模块改造）
- 关联能力：观测（新增，验证完成后建立 `spec/specs/observability/spec.md`）；日志格式契约见 [项目约束](../../../../PROJECT.md)

## 目标与范围

- 问题与当前行为：后端全部日志走标准库 `log`，全仓 263 个调用点分布在 41 个文件（`server/` 占 201）。日志经 `server/logsink.go` 汇入 stderr、内存环、`server_logs` 表与 SSE，但**级别与标签靠消息文本猜**（`levelOf` 命中"失败/丢弃/拒绝"等中文关键词即 error）——写错一个词就把 error 记成 info。此外 task/intent 的多数日志缺 task 关联字段，`tool`/`decision` 两类事件基本没有日志（决策只落 DB 与内存审计环）。
- 预期结果：建立结构化日志底座，级别与标签由调用方显式给出；先迁移 task/intent/tool/decision 四类事件中关联字段最缺、调参最依赖的站点；保持现有日志页与入库契约不变。
- 范围：新增 `logging` 包（slog 适配现有文本契约）、`server/logsink.go` 增加结构化入口、`cmd/artex` 接线、四类事件首批站点迁移、项目约束与变更记录。
- 不做的内容：不引入日志级别开关或配置项，不做全量 263 点迁移（其余调用点仍走标准库 `log`，级别继续靠关键词猜），不改日志页与 `server_logs` 表结构，不改 `guard`/`intercept` 的任何判定逻辑，不改 `traffic`/`llmrec`/`llmpool` 等噪声源。
- 依据与待确认事项：前端日志页与 `server_logs` 按「一行一条文本 + tag + level」消费（`web/src/app/(main)/system/logs/page.tsx`、`db/logs.go`），因此**不能**改成 JSON 输出；全仓 `log/slog` 零使用、无自研 logger、无级别配置，可作为纯新增。

## 需求增量

### ADDED：OBS-001 结构化日志入口与文本契约

后端必须具备结构化日志入口：级别与标签由调用方显式给出，不再依赖消息文本猜测；输出必须仍是单行 `[tag] 消息 key=value …` 文本，并进入现有日志汇（stderr、内存环、`server_logs`、SSE）。字段值含空白时必须加引号，保证一条日志的字段边界可辨认。

#### 场景：显式级别不再被文本影响

- GIVEN：一条名为"收尾完成"的 info 日志，消息里带"失败"字样。
- WHEN：写入日志汇。
- THEN：级别为 info；换用 Warn/Error 调用时才分别为 warn/error。

#### 场景：字段与标签

- GIVEN：携带 `task`、`intent`、`tool`、`decision` 字段的日志。
- WHEN：写入日志汇。
- THEN：文本形如 `[worker] 领取意图 task=t-7 intent=12`，标签不再出现在正文中重复出现。

#### 场景：未安装日志汇

- GIVEN：进程尚未接线日志汇（例如单元测试）。
- WHEN：写日志。
- THEN：不 panic、不丢日志，输出落到 stderr。

### ADDED：OBS-002 四类事件的关键站点

task、intent、tool、decision 四类事件的关键站点必须使用结构化入口，并携带可关联的标识：任务超时收尾、任务控制、意图领取与收场、活动写入重试与丢弃、工具 panic、拦截与审批决策。决策类只记非放行结果（拦截、拒绝、审批请求、超时、模型兜底）与决策记录落库失败，放行不逐条刷日志。日志不得写入工具命令行或输入原文，避免凭据与业务内容随日志落库。

#### 场景：意图终态

- GIVEN：worker 结束一条意图且判定为 exhausted。
- WHEN：写终态日志。
- THEN：日志同时带 `task`、`intent`、`worker`、`state` 字段。

#### 场景：人工审批

- GIVEN：拦截规则命中 ask。
- WHEN：创建 pending 并等待用户。
- THEN：产生一条 warn 级决策日志，带 `decision=ask`、`tool`、`pending`；用户放行时不额外刷日志。

#### 场景：工具 panic

- GIVEN：工具处理函数 panic。
- WHEN：守卫恢复该 panic。
- THEN：产生一条 error 级日志，堆栈截断为单行字段并带上工具名。

## 设计与影响

- 实现入口与方案：新增 `logging` 包（`Sink` 接口 + slog `textHandler`），`server/logSinkT` 实现 `Emit(level, tag, text)`，`StartLogCapture` 同时调用 `logging.Setup(logSink)`；四类事件提供 `Task`/`Intent`/`Tool`/`Decision` 构造器固化字段约定。
- 关键取舍：输出保持文本而非 JSON——日志页、`server_logs` 三列与 `levelOf` 的既有消费方都按文本解析，换 JSON 会让页面分级与标签整体退化；级别开关暂不引入（无历史配置、也无降噪需求支撑），先把"显式级别"这一步做掉。
- API / 数据 / 配置 / 兼容性影响：无接口、数据库、配置项变化；`server_logs` 仍写 `level`/`tag`/`text` 三列。旧调用点与迁移后的调用点可在日志页共存：旧行的 tag 取自 `[...]` 全部内容，新行的 tag 是事件来源短名。
- 失败、并发与边界：`logging` 包只在进程启动时安装一次日志汇，读路径用 `RWMutex`；`Setup(nil)` 不生效也不清空已有日志汇；值含空白时加引号，多行堆栈先截断再入字段，避免一行日志撕裂成多条。
- 迁移与恢复方式：无数据迁移。回滚即恢复旧的 `log.Printf` 调用，日志表与页面不受影响。
- 验证时发现的既有缺陷：`intercept.SetReviewer` 缺空接收者保护，`TestRenderCommandWithoutRenderer`（用例要求 nil 拦截器不 panic）在本变更前即失败；补上与 `SetCommandRenderer` 同款的 nil 保护，属顺带修复，与日志改造无关。
- 适用规范及例外理由：按 WORKFLOW 重点流程记录；日志文案与字段命名遵循 STYLE.md，不新增风格规则。

## 任务

- [x] 1. 勘察日志现状与契约（263 调用点分布、级别猜测、输出去向、测试断言面）。
- [x] 2. 建立 `logging` 包与 `server/logsink.go` 结构化入口并接线。
- [x] 3. 迁移四类事件首批站点（task/intent/tool/decision）。
- [x] 4. 同步 `spec/PROJECT.md` 观测约定与 CHANGELOG。
- [x] 5. 验证（构建、vet、受影响包测试、server 测试编译）后归档并单独提交。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| OBS-001 结构与契约 | `go test ./logging -count=1 -v` | 5 项全部通过（显式级别、四类构造器、tag 不重复、引号边界、Setup(nil) 安全） | — |
| OBS-002 受影响包 | `go test ./logging ./guard ./intercept -count=1` | 全部 ok | — |
| 编译 | 仓库根目录 `go build` 全部业务包（排除遗留 `release-work/` 目录） | 退出码 0 | `go build ./...` 仍被 `release-work/` 与 C 盘空间干扰，属既有环境问题 |
| 静态检查 | `go vet ./logging ./guard ./intercept ./server` | 通过 | — |
| server 测试编译 | `go test -c -o release-work/ci-check/tmp/server.test.exe ./server` | 通过 | 未执行测试运行（需要隔离 PostgreSQL） |
| 既有缺陷 | `go test ./intercept`（本变更前） | `TestRenderCommandWithoutRenderer` panic；补 nil 保护后 ok | — |
| 平台/后续项 | Go 全部业务包编译 | — | traffic/llmrec/llmpool/selfupdate 等噪声源与其余调用点留待后续批次；未做真实任务运行验证 |
| 文档 | 相对链接检查（`spec/WORKFLOW.md`、`spec/STYLE.md`、`spec/PROJECT.md`、代码入口均存在）、`git diff --check` | 通过，退出码 0 | — |

## 收尾

- 现行规范与通用文档更新位置：新建 `spec/specs/observability/spec.md`（OBS-001、OBS-002）并登记到 `spec/README.md` 能力索引；`spec/PROJECT.md` 新增「观测与日志」段与修改入口表行；CHANGELOG 在 `[Unreleased]` 记录。
- 剩余限制或阻塞：263 个调用点中仍有大量走标准库 `log`（级别靠关键词猜）；本变更只覆盖四类事件的关键站点，未迁移 `traffic/`、`llmrec/`、`llmpool/`、`selfupdate/`、`server/notifier.go` 等高频噪声源；未引入级别开关，也未做真实任务运行下的日志观测验证（只到单元测试与编译层面）。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-logging-slog/`。
