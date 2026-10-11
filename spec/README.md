# Artemis SPEC

这是 Artemis 的定制规范体系。借鉴 [OpenSpec 的现行规范、变更增量与归档概念](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md)，结合本仓库维护方式，以 Markdown 管理，不依赖 OpenSpec CLI 或特定助手命令。

## 文档职责

| 文档 | 维护内容 |
| --- | --- |
| [WORKFLOW.md](WORKFLOW.md) | 所有助手共同遵循的开发流程与完成条件 |
| [STYLE.md](STYLE.md) | 全局代码、UI、命名、文案与文档风格 |
| [PROJECT.md](PROJECT.md) | 技术边界、构建配置、代码入口与架构约束 |
| `specs/<capability>/spec.md` | 已落地的能力行为与验收场景 |
| `changes/<change-id>/change.md` | 本次变更的意图、需求增量、设计、任务和验证证据 |
| `changes/archive/<YYYY-MM-DD>-<change-id>/change.md` | 完成后的变更记录，不作为现行规则入口 |
| [templates/change.md](templates/change.md)、[templates/spec.md](templates/spec.md) | 新变更与能力规范模板 |

相对于 OpenSpec，本项目将 proposal、需求增量、design、tasks 和验证合并在一份 `change.md` 中，减少维护成本；复杂设计可拆成同目录附件并从变更记录链接。本文档结构和状态不声明兼容 OpenSpec CLI。

## 开始使用

1. 阅读工作流、风格和项目约束，查找相关能力规范与未完成变更。
2. 按工作流判断是否需要独立变更记录；需要时复制变更模板到 `changes/<change-id>/change.md`。
3. 先写可验收的目标，再实现和验证；完成后同步现行规范并归档。

命名遵循 [STYLE.md](STYLE.md)。日期只用于归档前缀。同一 ID 不重复创建，归档重名时增加明确后缀，禁止覆盖历史。

## 现行能力索引

- [开发规范协作](specs/development/spec.md)：统一入口、规则归属与变更完成条件。
- [自更新](specs/self-update/spec.md)：固定发布源、检查更新、下载校验与换装回滚。
- [观测](specs/observability/spec.md)：日志文本契约与 task/intent/tool/decision 事件字段。

其余业务能力尚未逐项建立 SPEC。现有约束先以 PROJECT.md 为准；首次改动某能力时，阅读代码与测试补充涉及的现行行为，并区分已验证事实与待确认问题，不虚构完整覆盖。

`spec/` 是公开、可版本控制的项目文档；不要放入凭据、真实业务样本、个人路径或内部交接记录。根目录 `docs/` 已被忽略，本体系使用独立目录，不改变该忽略规则。
