# baseline-capability-specs：为三块已落地能力补现行规范

- 状态：archived
- 类型：标准（纯文档规则变更，影响后续开发方式）
- 关联能力：工具库与技能库（[tool-library](../../../../specs/tool-library/spec.md)）、任务预算与收尾（[task-budget](../../../../specs/task-budget/spec.md)）、备份与恢复（[backup-restore](../../../../specs/backup-restore/spec.md)）；三份均为新增现行规范

## 目标与范围

- 问题与当前行为：`spec/specs/` 此前只有开发规范协作与观测两份，工具库、任务预算、备份与恢复三块能力只有 `spec/PROJECT.md` 的局部约束和 README 的功能描述，改动它们时没有可验收的现行基线。
- 预期结果：三块能力各有一份现行规范，包含需求 ID、可观察行为的验收场景，以及"边界与待确认"——把代码实际行为与文档口径不一致的地方如实列出，而不是让规范写成理想状态。
- 范围：新增三份 `specs/<capability>/spec.md`、登记 `spec/README.md` 能力索引、CHANGELOG、本变更记录。
- 不做的内容：不改任何代码或行为；不改 `spec/PROJECT.md` 的既有约束（规范只引用）；不修规范里列出的待确认项（各自需要独立变更）；不为尚在推进的库更新与基线安装写规范。
- 依据与待确认事项：内容来自已提交代码与既有约束的核对。工具库部分的勘察一度读到工作树——那里有他人未提交的库更新改动，因此关键差异点用 `git grep HEAD` 与 `git show HEAD:` 复核过（例如 HEAD 上尚无 registry 代码、`skillRoots()` 的顺序与 PROJECT.md 一致、命令模板固定 `python3`），未提交内容不写进现行规范。

## 需求增量

### ADDED：SPEC-TOOL 工具库与技能库现行规范

规范必须覆盖：磁盘为真源与清单原子写、清单到工具的**单向**导入、缺失与摘要不符只告警不阻断、执行层零改动（命令模板与不可导入情形）、审计命令渲染链、多源同步的并集语义与"先文件后清单"、摘要必钉与 `allow_unpinned`、技能库扫描根优先级与写入落点、快照与并集回滚、写锁与冲突响应。

- GIVEN：开发者要改动工具库导入、同步或回滚。
- WHEN：按 [tool-library 规范](../../../../specs/tool-library/spec.md) 查找现行行为与验收场景。
- THEN：能找到对应需求（TL-001~TL-009）与场景，以及该能力的已知边界。

### ADDED：SPEC-BUDGET 任务预算与收尾现行规范

规范必须覆盖：预算按任务配置且缺省不限、保守预留与结算、额度耗尽后的失败与收尾时序、工具次数预留与收尾豁免、无进展（空转）保护、步数与运行时长上限对意图终态的影响。

- GIVEN：开发者要改动预算、收尾或空转保护。
- WHEN：按 [task-budget 规范](../../../../specs/task-budget/spec.md) 查找现行行为。
- THEN：能找到 TB-001~TB-006 与场景，并看到 README 口径与实际默认值的差异记录。

### ADDED：SPEC-BACKUP 备份与恢复现行规范

规范必须覆盖：备份内容与凭据边界、加密与完整性校验、隔离恢复的前置条件与拒绝路径、一致性由操作者声明、迁移状态的可见性与幂等性。

- GIVEN：开发者或维护者要改动备份脚本、恢复流程或迁移状态接口。
- WHEN：按 [backup-restore 规范](../../../../specs/backup-restore/spec.md) 查找现行行为。
- THEN：能找到 BR-001~BR-005 与场景，以及已知缺口（密钥未进包、目录不入清单、schema 版本不比对等）。

## 设计与影响

- 实现入口与方案：三份新增规范 + `spec/README.md` 索引；不产生代码改动。
- 关键取舍：只写已提交行为，并且把"代码与文档不一致"写成规范里的"边界与待确认"而不是直接改写成某一种说法——规范不该在两侧都失真；待确认项留给独立变更解决。
- API / 数据 / 配置 / 兼容性影响：无。
- 失败、并发与边界：文档无法自动校验；规范里的实现级断言依赖后续变更逐一复核。
- 迁移与恢复方式：无。
- 适用规范及例外理由：按 WORKFLOW 标准流程记录；`spec/README.md` 的能力索引同步更新。

## 任务

- [x] 1. 勘察三块能力（两个并行子代理 + 工具库部分用 HEAD 复核）。
- [x] 2. 撰写三份规范并登记能力索引与 CHANGELOG。
- [x] 3. 检查文档链接与差异，归档并单独提交。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| 代码入口存在性 | 仓库根目录检查三份规范列出的 22 个路径 | 全部存在，无缺项 | — |
| 工具库口径核对 | `git grep -l "registryInstall\|registryState" HEAD -- server`、`git show HEAD:server/skillsrc.go`、`git grep -n "python3" HEAD -- server/toollib.go`、`git grep -n libTryLock HEAD -- server` | HEAD 无 registry 代码；`skillRoots()` 顺序为 `skills/`→`uploaded`→git 镜像，与 PROJECT.md 一致；模板固定 `python3`；写锁覆盖上传/删除/存源/删源/同步/回滚但**不含**导入 | — |
| 与既有约束一致性 | 逐条对照 `spec/PROJECT.md` 的工具库、技能库、快照与写锁段落 | 规范与约束一致；两处张力（源显式绑定覆盖本地、自检与模板的 Python 口径）写入待确认 | — |
| 文档 | 相对链接与路径检查（`spec/WORKFLOW.md`、`spec/STYLE.md`、`spec/PROJECT.md` 与三份规范均存在）、`git diff --check` | 退出码 0 | — |
| 规范断言的实现级复核 | 三份规范中的行为断言 | 部分来自子代理勘察（预算、备份为工作树只读；工具库关键差异已用 HEAD 复核） | 逐条实现级复核留待各能力的后续变更；规范已标注待确认项 |
| 未覆盖 | 三块能力的自动化测试缺口 | 已写入各自规范的"边界与待确认"（工具库与技能库、备份脚本、预算收尾流程等均缺测试） | — |

## 收尾

- 现行规范与通用文档更新位置：新增 `spec/specs/tool-library/spec.md`、`spec/specs/task-budget/spec.md`、`spec/specs/backup-restore/spec.md`；`spec/README.md` 能力索引登记三条；CHANGELOG 在 `[Unreleased]` 记录。未改 `spec/PROJECT.md`。
- 剩余限制或阻塞：规范里的实现级断言未逐条复核；三块能力各自的"边界与待确认"需要独立变更处理（文档口径统一、异常路径语义、缺失测试）；库更新与基线安装落地后需再补规范。
- 归档日期与目录：2026-10-12，`spec/changes/archive/2026-10-12-baseline-capability-specs/`。
