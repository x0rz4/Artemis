# tool-usage-skills：锁定工具的使用指导与 Skill 维护

- 状态：draft
- 类型：标准；后续 Registry 接入涉及发布校验，实施时按重点验证
- 关联能力：[基线工具安装](../../specs/baseline-tool-install/spec.md)、[云更新](../../specs/cloud-library-updates/spec.md)

## 目标与范围

用户明确要求独立建立 `x0rz4/Artemis-tools-skill`，主要指导 `tools-lock.json` 中工具的使用；`Artemis-registry` 继续负责发布。本次交付 Git 提交、独立仓库的规划文档和 GitHub 来源调查，不将规划中的 Skill 或同步自动化标记为已实现。

当前八个工具已可安装，但三个既有 Skill（api-recon、playwright-cli、scopesentry）并未逐项覆盖这八个工具的调用。新仓库围绕使用条件、输入、参数、输出、错误处理与组合流程维护知识，首批不扩充二进制工具清单。

发布端 `Artemis-registry/packaging/tools-lock.json` 是指导内容的版本依据；主仓库同路径锁文件服务离线安装。新仓库只保存注明来源提交与摘要的覆盖表，不独立决定工具下载版本。两个锁文件出现版本差异时，明确列出兼容版本或分别验证，不能默认等价。

## 需求增量

### ADDED TUS-001 工具与 Skill 对照

每个工具具有独立 `skills/<tool-name>/SKILL.md`，使用唯一名称和有效元数据，声明经过验证的工具版本、平台、依赖及对应参考文档。覆盖表区分 planned、reviewed、verified、published；无实际文件和验证证据不能标记 verified。

- GIVEN 锁文件中的八项工具；WHEN 完成首批指导；THEN 八项都有可加载 Skill，kscan 明确仅支持锁文件内的 linux/amd64，不能暗示 arm64 可安装。
- GIVEN 锁文件版本变化；WHEN 校验覆盖表；THEN 受影响指导标记待复核，不能沿用旧验证结论。

### ADDED TUS-002 可执行条件与结果理解

每份指导应覆盖适用场景、输入格式、已注册工具调用、参数选择、输出读取、常见错误、有限重试和停止条件。优先使用工具原生结构化输出，保留原始证据；不将空结果直接解释为目标安全。缺失工具、未启用、平台不支持或缺少模板/字典时，提示通过现有工具库解决，不自行安装 latest 或绕过审批。

- GIVEN 工具已注册且匹配验证版本；WHEN 依据 Skill 生成调用；THEN 参数可被固定版本识别，输出可按文档解释。
- GIVEN 连续超时或缺少依赖；WHEN 阅读指导；THEN 有明确错误分类与停止条件，不无限重试或扩大目标范围。

### ADDED TUS-003 有来源的适配维护

先读取工具上游对应版本的帮助和官方文档，再搜索社区 `SKILL.md` 作为参考。候选记录仓库、路径、提交、许可、适用工具与差异；复用文件前核对文件级许可与署名。搜索结果只是候选，不直接进入运行时。

- GIVEN 社区 Skill 依赖锁文件之外的工具或其他平台 Hook；WHEN 评审；THEN 记录依赖差异并改写为 Artemis 调用方式或拒绝纳入。
- GIVEN 没有合适的社区 Skill；WHEN 编写指导；THEN 依据官方固定版本资料编写，不虚构已复用的来源。

### ADDED TUS-004 源码与发布分工

新仓库维护 Skill 源码和评审证据；Registry 未来按明确提交、源码摘要和 Skill 版本引入审核通过的内容，继续沿用 ZIP、清单、稳定索引和用户手动更新机制。主程序不新增运行时直拉新仓库的路径。现有三个 Skill 保留，不因新增八项而迁移或删除。

- GIVEN 新仓库内容变化；WHEN 未经 Registry 新版本发布；THEN Artemis 的稳定更新源不变。
- GIVEN 发布验证失败；WHEN 构建结束；THEN 不推进稳定索引，已发布版本可回滚。

## 设计与影响

完整维护方案、工具矩阵与候选来源放在新仓库的 `MAINTENANCE.md`、`catalog/tool-coverage.json`、`catalog/sources.json`。当前只初始化文档和调查记录；八份 Skill、检查脚本、定时搜索及 Registry 源码导入属于后续实施任务。

- 内容顺序：先完成八个单工具 Skill，再评估一个串联既有工具的流程 Skill。组合以实际前序结果驱动，不把所有工具无条件执行一遍。
- 验证：固定版本 help/version、匿名离线输出样本、元数据解析、参数存在性、结构化输出语义；目标扫描不作为普通 CI 项。真实调用验收使用隔离本地靶场并单独记录。
- 维护：建议每周检查既有来源变化，每月补搜社区参考；工具锁变化立即复核对应指导。自动化仅生成候选差异或 PR，发布仍需评审和现有 CI。
- API / 数据 / 配置：本次无变化。未来兼容版本映射先由打包 CI 校验；现有 Artemis 运行时尚不会自动强制 Skill 与工具版本匹配，不宣称已有该能力。
- 失败路径：网络失败保留上次来源快照；没有许可或固定提交只保留候选；版本不匹配阻止纳入发布。多个维护 PR 在合并前重新校验锁文件基准。
- 迁移与恢复：不迁移现有 Skill。后续新增 Skill 通过 Registry 发布，恢复沿用现有快照与固定版本机制。

## 任务

- [x] 明确独立仓库分工和现有八项范围。
- [x] GitHub 搜索官方工具与社区 Skill，记录初始候选和核查边界。
- [ ] TUS-001：完成八份 Skill，核对加载、命名、版本和平台矩阵。
- [ ] TUS-002：核验参数、输出与故障路径，整理脱敏样本与验证记录。
- [ ] TUS-003：实现来源、许可、锁文件漂移和内容检查；定时搜索只产出候选。
- [ ] TUS-004：在 Registry 增加固定源码导入与发布验收，验证更新及回滚。
- [ ] 实现通过后同步现行规范与发布文档，再归档本变更。

## 验证记录

| 检查项 | 实际操作 | 结果 / 边界 |
| --- | --- | --- |
| 工具范围 | 读取主仓库与 Registry 的 tools-lock.json | 八项名称、版本和平台一致；Registry 还维护许可证信息 |
| 社区来源 | GitHub 搜索并读取 PAIPlugin 的 ffuf Skill、hack-skills 的 recon-and-methodology Skill 与仓库元数据 | 找到直接相关参考；记录固定提交和仓库许可，尚未完成逐文件许可与命令验证 |
| 交付范围 | 文档和覆盖表标记为规划状态 | 未生成可发布 Skill，未改变稳定索引，未运行扫描 |
| 文档检查 | 校验本次文档的 25 个本地链接、JSON、来源提交格式、版本和平台矩阵；两个仓库执行 `git diff --check` | 通过；八项均为 planned，七项支持 arm64，来源未复制 |

## 收尾

本变更按用户“规划”请求保留 draft，不提前合并进现行能力规范。主仓库已实现的工具安装与云更新由独立提交记录；本规划另行提交。详细来源和后续任务以新仓库维护文档为入口。

独立公开仓库：[Artemis-tools-skill](https://github.com/x0rz4/Artemis-tools-skill)，初始文档提交 `e0d9356`；维护入口为 [MAINTENANCE.md](https://github.com/x0rz4/Artemis-tools-skill/blob/main/MAINTENANCE.md)。工具安装与云更新实现记录于 Artemis 提交 `762eda4`。
