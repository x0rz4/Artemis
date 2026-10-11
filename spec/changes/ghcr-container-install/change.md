# ghcr-container-install：GHCR 镜像发布与一键安装

- 状态：implementing
- 类型：重点（发布与安装行为）
- 关联能力：容器交付（新增，验证完成后建立现行能力规范）

## 目标与范围

- 当前 Dockerfile 依赖宿主机预编译二进制，Compose 指向 `artemis:local`，没有镜像发布工作流和 install.sh。
- 用户要求参考 mhtsec/ARTEX 的 CI 和 install.sh，明确选择 GHCR。
- 增加源码镜像构建、双架构构建/发布、Docker 与本地安装、安装验证和使用文档。
- 不修改 Norma、业务执行器、数据库种子、自更新发布源，不自动提交、推送或触发远端发布。
- 工作期间发现仓库新增统一 SPEC 工作流，补建本记录并按其要求继续验证，保留并行编辑的规范文件。

## 需求增量

### ADDED：DEP-001 从源码构建正式镜像

镜像必须使用锁文件、Node.js 22 和符合 go.mod 的 Go 编译内嵌前端的 Artemis，支持 linux/amd64 与 linux/arm64；不得包含宿主机凭据、业务数据、抓包或备份，必须保留许可证。

- GIVEN：干净源码，无 dist 预编译文件。
- WHEN：执行 Docker 构建。
- THEN：镜像中的 /app/artemis 能启动并提供静态初始化页；数据保存在独立 PostgreSQL 与运行目录。

### ADDED：DEP-002 测试通过后发布 GHCR

PR 只构建验证；main 发布 edge，稳定版本标签发布版本标签和 latest，预发布不覆盖 latest；必须在两种架构分别通过隔离初始化、鉴权、空业务状态与持久化测试后发布多架构标签。

- GIVEN：main 推送或符合格式的版本标签，Actions 具备包写权限。
- WHEN：工作流运行。
- THEN：两种架构测试通过才更新多架构标签；任一失败不会更新正式标签。
- GIVEN：PR 或手动关闭 publish。
- WHEN：工作流运行。
- THEN：不登录 GHCR、不上传镜像。

### ADDED：DEP-003 可重复的一键安装

支持 Docker 与本地源码编译两种安装方式；必须保留已有配置，不执行 .env 内容；新数据库密码随机生成且不输出，密码特殊字符必须可用。Docker 模式必须在健康检查通过后才报告成功，拉取失败可选择源码构建。

- GIVEN：首次安装，未提供模型或管理员密码。
- WHEN：运行默认安装。
- THEN：生成数据库配置，启动空业务系统，通过 /setup 初始化真实管理员；不预置模型。
- GIVEN：已有 .env/config.json，或 ARTEX_PG_DSN/ARTEX_CONFIG。
- WHEN：再次安装。
- THEN：保留配置并遵循原有优先级；错误配置、构建失败或健康检查失败以非零状态退出。
- GIVEN：--no-start。
- WHEN：准备配置和构建/拉取。
- THEN：不得启动应用或数据库容器。

## 设计与影响

- Dockerfile 采用前端、后端、运行层多阶段构建；.dockerignore 限制构建输入。容器启动仍走现有 start.sh。
- 新 container 工作流采用原生 amd64/arm64 runner，推送已测试的本地镜像，最终按 digest 合并标签；无需 Docker Hub 凭据。
- Compose 使用 PGUSER/PGPASSWORD/PGDATABASE 配合固定 DSN，避免密码 URI 转义问题；数据库默认不映射宿主机端口。保留 data/skills 挂载与 pgdata 卷。
- 本地安装复用 build.sh；Python 使用 JSON 序列化、独占创建 config.json。只在本地数据库模式添加回环端口映射。
- 兼容：ARTEX_* 保留；现有 PostgreSQL 卷和配置不迁移、不覆盖。Web 绑定默认改为回环，可用 ARTEX_BIND 显式开放。
- 失败与恢复：拉取失败可源码构建；构建/健康检查失败终止并提示日志；升级前备份，恢复需匹配数据库版本，脚本不删除运行数据或卷。
- GHCR 首次发布需确认包的 Public 可见性，已有包需授予仓库写权限。
- 规范：[工作流](../../WORKFLOW.md)、[风格](../../STYLE.md)、[项目约束](../../PROJECT.md)。

## 任务

- [x] DEP-001：源码 Dockerfile、Compose、构建上下文与许可文件。
- [x] DEP-002：GHCR CI、原生双架构构建及隔离容器冒烟脚本。
- [x] DEP-003：安装脚本和配置生成器。
- [ ] 完成本地脚本、Actions、Compose 检查和回归测试。
- [ ] 完成真实双架构构建与隔离容器测试。
- [ ] 更新 README 和来源说明；全部验收通过后同步现行能力规范。
- [ ] 检查完成条件并归档。

## 验证记录

| 检查项 | 执行位置与命令 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| 安装回归 | 仓库根目录 `python -m unittest discover -s scripts -p test_install.py -v` | 首轮修复 Windows 测试 PATH 后 11 项通过，后续修改待复验 | — |
| Shell | 仓库根目录 `shellcheck install.sh start.sh` | 通过，后续修改待复验 | — |
| Actions | 仓库根目录 `actionlint -shellcheck= -pyflakes= .github/workflows/docker.yml .github/workflows/release.yml` | 通过，后续修改待复验 | — |
| 容器构建/运行 | `python3 scripts/smoke_container.py artemis:ci`，已接入两种架构 CI | 未执行 | 本机无 Docker daemon，远端工作流未运行 |

## 收尾

- 待完成真实容器验收后建立容器交付能力规范；当前不标记 verified/archived。
- 远端镜像尚未上传；本次交付包含实现和可执行 CI，不将未运行的检查记录为通过。
