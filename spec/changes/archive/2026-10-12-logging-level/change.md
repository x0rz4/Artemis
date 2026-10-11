# logging-level：日志级别开关

- 状态：archived
- 类型：标准（可观察行为变化）
- 关联能力：观测（现行规范 [observability](../../../../specs/observability/spec.md)）；日志契约见 [项目约束](../../../../PROJECT.md)

## 目标与范围

- 问题与当前行为：日志汇把标准库 `log` 的每一行与结构化记录一起送往 stderr、内存环、`server_logs` 表与 SSE，级别却被丢弃不用——除环境变量里零散的调试开关外没有任何级别控制，唯一的"降噪"手段是删日志或改文案；结构化日志此前硬编码"只放过 info 及以上"，无法调整。
- 预期结果：由一个环境变量决定最低输出级别，结构化日志与标准库 `log` 的行**共用同一阈值**；默认行为与现在完全一致（info）。
- 范围：`logging` 包的级别解析与阈值、`server/logsink.go` 的过滤、启动提示；同时把 `planner` 每轮规划与任务 LLM 重试这类高频站点迁到结构化入口（它们的级别才有意义）。
- 不做的内容：不做运行期（页面/接口/settings 表）切换；不改变默认日志量；不迁移 `server` 其余调用点；不引入日志轮转或分级文件输出。

## 需求增量

### ADDED：OBS-004 日志级别开关

最低输出级别必须由 `ARTEX_LOG_LEVEL`（`debug|info|warn|error`，缺省 `info`，大小写不敏感）决定，并且对结构化日志与标准库 `log` 的行使用同一阈值，过滤效果在 stderr、内存环、`server_logs` 与 SSE 上一致。无效取值必须回落 `info` 并给出可读提示；启动时必须说明当前生效的阈值。

#### 场景：调高阈值

- GIVEN：`ARTEX_LOG_LEVEL=warn`。
- WHEN：分别产生一条 info 与一条 warn 日志（结构化调用与标准库 `log` 各来一遍）。
- THEN：只有 warn 及以上的行出现在 stderr、内存环、`server_logs` 与 SSE；info 行全部被丢弃。

#### 场景：写错取值

- GIVEN：`ARTEX_LOG_LEVEL=verbose`。
- WHEN：进程启动。
- THEN：阈值回落 `info`，stderr 上出现一行提示，日志量不因笔误而变化。

#### 场景：保持默认

- GIVEN：未设置该变量。
- WHEN：进程启动。
- THEN：阈值为 `info`，info 及以上照常输出；不打印多余的阈值提示行。

## 设计与影响

- 实现入口与方案：`logging` 包持有阈值（`atomic.Int32`）并给出 `Allows`／`MinLevel`；`Setup` 解析环境变量；`textHandler.Enabled` 过滤结构化日志；`server/logSinkT.Write`（标准库路径）与 `Emit` 各自调用 `Allows`，让两条路径共用一个判定。
- 关键取舍：过滤放在日志汇而不是 handler 单点，是因为标准库 `log` 的行绕过 slog，只有汇看得见它们——若只在 handler 过滤，调高阈值会让结构化日志安静、老日志照旧刷屏，行为自相矛盾。`Emit` 再挡一次是给"绕过 logger 的调用者"兜底，不是重复过滤。
- API / 数据 / 配置 / 兼容性影响：新增环境变量 `ARTEX_LOG_LEVEL`（未设置时行为与之前完全一致）；日志页与 `server_logs` 三列不变；无接口与数据变化。
- 失败、并发与边界：阈值用原子变量读取，运行期无锁；无效值回落 `info` 并在 stderr 提示（直接写 stderr，不经过被过滤的通道）；认不出的级别名在 `Allows` 中放行，避免把历史行静默吃掉。
- 迁移与恢复方式：无数据迁移；不设变量即恢复原有行为。
- 适用规范及例外理由：按 WORKFLOW 标准流程记录；`spec/PROJECT.md` 的观测段同步更新。

## 任务

- [x] 1. 在 `logging` 实现级别解析、阈值与 `Allows`／`MinLevel`（OBS-004）。
- [x] 2. `server/logsink.go` 对标准库行与结构化行应用同一阈值，并在启动时提示当前阈值。
- [x] 3. 迁移 `planner` 每轮规划与任务 LLM 重试等高频站点。
- [x] 4. 同步 `spec/PROJECT.md` 与观测规范，归档并单独提交。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| OBS-004 单元测试 | 仓库根目录 `go test ./logging -count=1` | 通过，含 warn 阈值只留 warn、级别名大小写不敏感、无效值回落 info、未知级别名放行 | — |
| 编译（隔离副本） | HEAD 副本仅覆盖本次改动文件后 `go build ./server/... ./logging/... ./cmd/... ./traffic/... ./llmpool/... ./llmrec/... ./enrich/...` | 退出码 0 | 主工作树当时因并行变更（`cloud-library-updates` 的 `registry*` 符号缺文件）编译不过，与本次改动无关 |
| 静态检查（隔离副本） | 同上副本 `go vet ./logging ./server` | 通过 | — |
| server 测试编译（隔离副本） | 同上副本 `go test -c -o server.test.exe ./server` | 通过 | 未执行测试运行（需要隔离 PostgreSQL） |
| 端到端观感 | 设 `ARTEX_LOG_LEVEL=warn` 启动后查看 stderr 与日志页 | 未执行 | 需要可运行的库与真实任务；本次只到单测与编译 |
| 文档 | 相对链接检查（`spec/PROJECT.md`、观测规范、代码入口均存在）、`git diff --check` | 退出码 0 | — |

## 收尾

- 现行规范与通用文档更新位置：`spec/PROJECT.md` 观测段把"尚无级别开关"改为 `ARTEX_LOG_LEVEL` 约定；`spec/specs/observability/spec.md` 增补 OBS-004 并更新边界段；CHANGELOG 在 `[Unreleased]` 记录。
- 剩余限制或阻塞：没有运行期切换入口；编译与 vet 是在 HEAD 隔离副本上完成的（当时主工作树被并行变更破坏），未在主树复跑；未做端到端观感验证；`server` 仍有 171 处、`traffic` 12 处等标准库 `log` 调用点，其级别继续由关键词推断后参与过滤。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-logging-level/`。
