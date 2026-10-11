# logging-denoise：高频日志源结构化与降噪

- 状态：archived
- 类型：标准（跨模块改造）
- 关联能力：观测（现行规范 [observability](../../../../specs/observability/spec.md)）；日志契约见 [项目约束](../../../../PROJECT.md)

## 目标与范围

- 问题与当前行为：观测首批只迁移了 task/intent/tool/decision 的关键站点，高频噪声源仍走标准库 `log`：`traffic` 有 11 处「记录 <url> 失败（阶段）」同形诊断（每次写入失败逐条刷、级别靠关键词猜、URL 带查询串）、`llmpool` 的熔断/故障转移/链耗尽（每次 LLM 失败都打，无 profile/model 字段）、`llmrec` 的两条落库失败、`enrich` 的队列丢弃与 DNS 初始化失败。
- 预期结果：这些源改走 `logging` 包——级别显式（失败路径 warn/error）、字段可检索（stage/profile/model/kind/id）、URL 进日志前去掉查询串与片段，同一事件不再刷多条同形行。
- 范围：`traffic`、`llmpool`、`llmrec`、`enrich` 四个包；CHANGELOG 与变更记录。
- 不做的内容：不改行为逻辑，也不动熔断/故障转移/重试策略；不迁移 `traffic` 其余 12 处稀有路径诊断（索引 schema、blob 落盘、空间回收、目录清理）；不迁移 `selfupdate`、`server/notifier.go` 与其余 `server/` 调用点；不引入级别的运行时开关；不改日志页与 `server_logs` 结构。

## 需求增量

### ADDED：OBS-003 高频日志源的分级与字段

高频失败路径的日志源必须走结构化入口，一次给出阶段与关联字段，级别按严重度显式指定；URL 不得带查询串与片段进日志。

#### 场景：流量写入失败

- GIVEN：索引库写入在任一阶段失败（开启事务、写索引、取 rowid、写正文、登记 blob 引用、写全文索引、提交）。
- WHEN：记录一条流量。
- THEN：产生一条 warn 级日志，带 `stage`、`url`（已去查询串与片段）与 `err`；不再出现 `记录 <url> 失败（阶段）` 形式的同形诊断。

#### 场景：LLM 轮询链耗尽

- GIVEN：轮询链上的配置全部失败。
- WHEN：流式与非流式路径分别走到耗尽。
- THEN：各产生一条 error 级日志，带失败配置数量与最后错误；熔断与故障转移各自带 `profile`/`model` 与下一跳字段。

## 设计与影响

- 实现入口与方案：`traffic/traffic.go`（`record` 的七处诊断统一为一条结构化行 + 新增 `logSafeURL`）、`llmpool/pool.go`（流式与非流式两处对称迁移）、`llmrec/llmrec.go`、`enrich/enrich.go`。
- 关键取舍：只改日志不改判定——熔断、故障转移、重试与丢弃的语义逐字未动；`traffic` 只迁移失败诊断，稀有路径留在标准库 `log`，不为风格统一扩大改动面。
- API / 数据 / 配置 / 兼容性影响：日志文本变化（如 `[traffic] 记录 … 失败（写索引）` → `[traffic] 记录流量失败 stage=index …`），日志页与 `server_logs` 三列契约不变；无接口、数据、配置项变化。
- 失败、并发与边界：无新增 goroutine、锁或 IO；`logSafeURL` 是纯字符串处理；`llmpool` 的日志仍在原重试循环内，不会改变失败计数。
- 迁移与恢复方式：无数据迁移，回滚即恢复旧 `log.Printf` 调用。
- 安全影响：日志不再回显完整 URL（查询串与片段被裁掉）——凭据常出现在 `?token=` 这类参数里，而日志会落库并在日志页长期展示；流量库本身仍保存完整 URL，搜索与导出不受影响。
- 适用规范及例外理由：按 WORKFLOW 标准流程记录；字段命名沿用 `spec/PROJECT.md` 的观测约定。

## 任务

- [x] 1. 定位高频噪声源并确认现有日志形态（traffic 19、llmpool 6、llmrec 2、enrich 2）。
- [x] 2. 迁移四个包的高频站点，合并 traffic 的同形诊断并给 URL 脱敏。
- [x] 3. 同步 CHANGELOG 与观测规范（如需要）。
- [x] 4. 验证后归档并单独提交。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| OBS-003 编译 | 仓库根目录 `go build` 全部业务包 | 退出码 0 | — |
| OBS-003 静态检查 | `go vet ./traffic ./llmpool ./llmrec ./enrich` | 通过 | — |
| OBS-003 测试 | `go test ./traffic ./llmpool ./llmrec ./enrich -count=1` | 全部 ok（`enrich` 无测试文件） | — |
| 同形诊断合并 | `traffic/traffic.go` 七处按阶段一次替换（脚本计数 `replaced=7`），`git diff` 复核 | 8 insertions / 7 deletions，仅预期行变化，无整文件重写或行尾破坏 | — |
| 迁移残留 | 检索四个包剩余 `log.Print*` | `llmpool`、`llmrec`、`enrich` 已无；`traffic` 剩 12 处稀有路径 | 有意保留，见「不做的内容」 |
| 运行观测 | 真实流量写入失败 / 真实 LLM 失败下的日志观感 | 未执行 | 需要隔离环境与真实目标；本次只到编译、静态检查与单测 |

## 收尾

- 现行规范与通用文档更新位置：`spec/specs/observability/spec.md` 增补 OBS-003（高频源分级、字段与 URL 脱敏）；CHANGELOG 在 `[Unreleased]` 记录。未改 `spec/PROJECT.md`：字段与契约约定未变。
- 剩余限制或阻塞：迁移残留 `traffic` 12、`selfupdate` 12、`db` 5、`cmd/artex` 5、`agent` 7、`server` 173，级别仍由关键词猜；未做真实流量写入失败与真实 LLM 失败下的日志观感验证。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-logging-denoise/`。
