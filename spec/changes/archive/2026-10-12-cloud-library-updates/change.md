# 工具库与 Skill 独立云更新

状态：archived
分级：重点（发布、并发写入、快照与恢复）

## 目标与范围

按用户确认的方案建立公开 `x0rz4/Artemis-registry`。工具和 Skill 独立发布，Artemis 自动检查、用户选择更新。保留自定义源和离线安装入口，不改扫描执行器。

## 需求增量

- **ADDED CLU-001 独立发布**：锁定上游版本、摘要、平台和许可证；ZIP 经校验上传后再推进稳定索引，标签不可覆盖。GIVEN 首版锁文件，WHEN 发布，THEN amd64 八个工具、arm64 七个工具及三个 Skill 均有独立包和清单。
- **ADDED CLU-002 只读检查**：启动及每六小时检查，可关闭或立即检查；检查不得下载安装包。GIVEN 网络失败，WHEN 检查，THEN 返回错误且已安装内容不变。
- **MODIFIED CLU-003 选择与固定版本**：同步保留 `names` 为源名称，新增 `items`、`release`、`replace`；官方更新只应用已检查、摘要固定的发布版本。GIVEN 检查后稳定索引推进，WHEN 应用，THEN 使用用户确认版本。
- **ADDED CLU-004 正确判定**：分别保存包摘要与落盘摘要，展示版本、变更、平台支持、冲突。GIVEN 安装完成，WHEN 重复检查，THEN 显示无更新；文件被改则显示冲突。
- **MODIFIED CLU-005 保留工具状态**：保留 key、Agent 绑定、启用和 deferred；新增默认禁用。GIVEN 数据库侧已修改绑定，WHEN 云端更新，THEN 绑定不被清单覆盖。
- **MODIFIED CLU-006 Skill 优先级**：显式本地根优先，其后为已激活官方版本、内置根、上传与 git 根。官方文件独立持久化；同名自定义或本地修改默认跳过，显式替换才能应用。GIVEN 更新后加载 Skill，THEN 使用新内容；回滚恢复旧版本或内置回退。
- **ADDED CLU-007 原子应用与恢复**：应用和回滚必须任务/聊天空闲，互斥写入，先快照、验证后切换；失败保留旧版；云端删除不删除本地。GIVEN 忙碌、并发请求、错误摘要或断网，WHEN 应用，THEN 拒绝或安全失败，旧版可用。

## 设计与兼容

固定官方源读取 `channels/stable.json`，包含 release、两类清单 URL/SHA-256；发布清单及包均使用不可变 Release URL。客户端缓存经验证的清单并固定 release，不直接跟踪上游 latest。官方管理元数据扩展现有清单，旧字段与自定义源 API 保持兼容。

包下载与解包进入临时目录，实际安装使用版本内容目录，最后切换清单；失败不覆盖旧文件。复用库写锁、白名单下载、快照与回滚接口。Skill 激活从清单读取，因此回滚清单即可恢复优先级；不会因残留新版本目录继续生效。显式本地配置继续优先。

检查状态和自动检查设置独立于业务数据库；服务上下文取消停止定时器。任务、聊天启动与应用共用活动门禁，防止先检查空闲后启动任务的竞态。

## 任务与验证

- [x] 独立仓库锁文件、Skill 源码、许可证、打包/校验脚本与 CI。
- [x] 官方检查、设置、定时器与固定清单协议。
- [x] 选择安装、冲突、状态保留与 Skill 加载优先级。
- [x] 活动门禁、快照、失败恢复与回滚。
- [x] 两个页面的检查、选择、替换冲突与回滚入口。
- [x] 摘要/平台/网络/并发/本地修改/重复检查单元测试及隔离数据库验证。
- [x] 首版实际发布与客户端检查/安装/回滚验证。
- [x] Go、前端、Python、工作流检查及部署/发布说明。

## 验证记录

执行日期：2026-10-12。

- 独立仓库已创建并发布 [v1.0.0](https://github.com/x0rz4/Artemis-registry/releases/tag/v1.0.0)：18 个 ZIP、kscan 源码归档、两份清单、稳定索引与摘要文件，共 23 个资产。
- [Registry 发布 CI](https://github.com/x0rz4/Artemis-registry/actions/runs/38172259916) 全部通过：Python 三项打包校验测试、固定上游下载与摘要、amd64 八项和 arm64 七项帮助/版本运行、上传后逐资产重新下载校验、稳定索引推进。无目标扫描。
- Windows 本地隔离 PostgreSQL 17（独立实例、端口、数据库和临时数据根），运行 `go test ./server ./db -run 'TestRegistry|TestAssembleVisibleSkill|TestSnapshot|TestSkill|TestTool|TestSource|TestSync' -count=1 -v`，30 项通过；另新增三个真实内置 Skill 的摘要回归检查，通过 Windows 原始字节和 Linux LF 检出字节核验。`ARTEX_CONFIG` 指向空测试配置；数据库地址明确指向隔离实例，没有读取业务数据库。
- 开启 `ARTEX_TEST_REGISTRY_LIVE=1` 验证公开源：检查、只选择 api-recon 安装、重复检查无更新、既有 API 回滚；通过。Windows 不支持 Linux 工具包由平台判定覆盖；Linux 工具运行由上述双架构 CI 验证。
- `TestRegistrySkillRuntimeRefresh` 通过真实 Skill 工具调用验证更新和回滚内容；工具导入通过真实隔离数据库验证默认禁用、key/绑定/启用/deferred 保留与事务失败恢复。
- 覆盖错误清单/包/内容摘要、网络中断、不支持的平台、错误 ELF、ZIP 穿越/重复、选择语义、检查设置持久化、并发与活动门禁、本地内容冲突、快照损坏和最旧快照回滚。
- `go vet ./server ./db`、`go build ./...` 通过；Windows amd64、Linux amd64/arm64 `-tags embedui` 构建通过。临时缓存置于独立验证目录，排除在应用模块扫描之外。
- 前端使用 Node.js 22.23.3（官方 ZIP 和 SHA-256 校验），`npx tsc --noEmit`、五个改动文件的 `npm run check -- ...`、`npm run build:static` 通过。检查中一并修复所触及页面已有的空错误处理、表单关联与树节点键盘操作；未关闭 lint 规则。
- 使用隔离数据库和内嵌静态前端，通过 Edge 无头浏览器验证真实更新、重复状态、回滚、平台不支持与网络错误；检查深浅主题与 390px 窄屏。截图和测试数据仅保留在被忽略的验证目录。
- 现有离线安装器 21 项测试继续通过；主仓库和 Registry 工作流 actionlint、`git diff --check` 通过。

## 同步与交付

现行规范：`spec/specs/cloud-library-updates/spec.md`；架构优先级、锁顺序和快照约束已同步 `spec/PROJECT.md`，索引已更新。部署与 API 说明见 `packaging/REGISTRY.md`、README；独立仓库包含自身发布说明。

快照恢复按库条目生效：目录内部恢复完整快照，未涉及的其它条目保留；兼容旧快照未记录摘要的格式。

验收时 Artemis 主仓库改动尚未提交、推送或发布新的主程序镜像；独立 Registry 已实际公开发布。后续提交以 Git 历史为准，提交不表示现有部署已升级。
