# Artemis 开发入口

所有开发任务必须遵循 [SPEC 工作流](spec/WORKFLOW.md)。开始工作前按顺序阅读：

1. [SPEC 索引](spec/README.md)：规范位置与使用方式。
2. [统一工作流](spec/WORKFLOW.md)：变更分级、实现、验证与归档。
3. [统一风格规范](spec/STYLE.md)：代码、界面、命名、文案与文档风格的唯一维护入口。
4. [项目约束](spec/PROJECT.md)：构建配置、代码入口及不可破坏的架构约定。
5. 本次涉及的 `spec/specs/<capability>/spec.md` 和已有 `spec/changes/<change-id>/change.md`。

CLAUDE.md 与 AGENTS.md 使用同一套规则，不在入口文件维护独立工作流或风格副本。新增通用规则应修改上述对应文档；目录级约束只补充局部要求，冲突时明确记录并解决，不静默绕过。

用户当前明确指示优先于仓库默认约定。需求已明确授权时按工作流持续完成，不为文档阶段重复索要确认；缺少关键需求时只询问阻塞项。
