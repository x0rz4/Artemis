# baseline-tool-installer：基线工具下载与清单生成

- 状态：archived
- 类型：重点（下载校验、磁盘清单更新与恢复）
- 关联能力：[基线工具安装](../../../specs/baseline-tool-install/spec.md)；现有边界见 [PROJECT.md](../../../PROJECT.md)

## 目标与范围

- 用户提供候选清单，明确选择实现 httpx、naabu、subfinder、katana、ffuf、nuclei、fscan、kscan 的下载、校验、manifest 生成，并接入安装流程。
- 当前工具库支持磁盘清单导入和源同步，但源码没有可复现的基线下载器。附件单条 JSON 不能直接替代完整 manifest；下载归档摘要也不能作为解压后文件摘要。
- 新增受版本控制的下载锁文件、Python 标准库脚本和 `install.sh --tools baseline|名称列表`。默认不增加下载；不预装镜像、不接触业务数据库、不自动创建任务或启用 Agent 工具，不下载模板库或第二批工具。
- 未提供 artex-ymh 的仓库地址；以本仓库 toolEntry/toolManifest 为格式依据，从各工具官方 Release 获取固定版本。

## 需求增量

### ADDED：BT-001 固定来源与严格校验

每个安装项必须具有固定版本、平台、HTTPS 资产地址和非空 SHA-256。下载必须先校验归档，再提取指定的普通文件，生成落盘文件摘要；不得执行下载内容。未知工具、缺失平台、恶意归档或摘要不符必须失败。

- GIVEN：有效锁文件与所选工具；WHEN：安装；THEN：得到真实可执行文件和包含文件摘要的 Artemis 清单。
- GIVEN：错误摘要、穿越路径、链接归档项或 kscan 的 arm64 请求；WHEN：安装；THEN：非零退出，原清单与现有工具不变。

### ADDED：BT-002 保留已有工具与平台配置

离线安装必须合并清单，保留未选择的条目、sources、未知元数据及已有 key/agents/enabled/deferred/schema。新条目默认禁用，之后由用户在平台导入并设置绑定。重复安装同一版本不改写清单或产生快照。

- GIVEN：含本地条目及定制绑定的清单；WHEN：更新选中工具；THEN：本地条目和绑定保持，旧文件保留，清单原子更新。
- GIVEN：失败或同时运行两个安装器；WHEN：写入；THEN：不发布未验证的清单，第二个安装器失败。

### ADDED：BT-003 安装流程与恢复

安装脚本必须提供显式工具选项，Docker 模式按 daemon 架构选择，本地模式按宿主架构选择，`--no-start` 仍准备文件但不启动服务。工具下载失败不得报告整套安装成功；正在运行的 Compose 应用必须先停止才可写工具库。直接运行 Python 脚本时需用户先停止使用该库的 Artemis。

- GIVEN：`--tools baseline --no-start`；WHEN：安装；THEN：准备八项工具和清单，不启动应用或数据库。
- GIVEN：已有清单的更新；WHEN：提交新清单；THEN：生成兼容工具库页面的快照和 manifest.json.bak，可恢复旧清单所引用的文件。

## 设计与影响

- `packaging/tools-lock.json` 保存官方固定资产地址、归档 SHA-256、精确文件成员；Linux amd64 支持八项，arm64 支持除 kscan 外七项，缺失平台不静默跳过。锁定摘要来自 GitHub Release 资产 digest；旧 kscan 无 digest，实际获取其固定 Release 后计算并记录。
- `scripts/install_tools.py` 下载到库内临时目录，流式限量校验，仅提取指定可执行文件和许可证文件，校验 ELF 架构。工具存入按内容摘要区分的目录，不覆盖旧路径；最后原子写清单，失败最多留下未被引用的新目录。
- 离线脚本的独占锁只覆盖其它脚本实例，不能替代服务内的互斥锁，因此明确要求停止应用；提交前再次比对旧清单，发现变化时退出。Compose 安装入口检查运行状态；本地模式说明停机前提。
- 更新前快照只需备份清单，因为旧工具文件从不覆盖；沿用 `.snapshots/<id>/meta.json`，最多保留五份，保留 manifest.json.bak。失败不删除旧版本，恢复走现有页面回滚/导入链路。
- `install.sh` 的 Docker 工具选项额外需要宿主 Python 3.8+，写宿主 `data/tools`，不启动辅助容器；普通安装需求不变。保持现有执行器与审计实现。
- 测试采用临时目录、本地伪资产和下载函数替身，覆盖安全解压、失败原子性、重复安装、配置保留、快照与流程接线；真实下载在独立 `release-work/` 目录验证，不使用业务数据。

## 任务

- [x] BT-001：官方固定下载锁文件与校验/提取脚本。
- [x] BT-002：清单合并、内容目录、独占锁、失败与幂等回归。
- [x] BT-003：安装入口、快照与恢复测试。
- [x] 验证官方八项资产、测试、Shell/Actions 检查。
- [x] 文档与现行能力规范；验证完成后归档。

## 验证记录

| 检查项 | 执行位置与命令 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| 官方资产调查 | GitHub 官方 Release API，固定版本资产列表 | 七项具有双架构与资产 digest；kscan v1.85 仅提供 linux/amd64，已下载并计算摘要 | — |
| 官方资产安装 | 仓库根目录 `python scripts/install_tools.py --platform linux/amd64 --output release-work/tool-bootstrap/amd64`；arm64 显式选择除 kscan 外七项，输出至独立 arm64 目录 | 8 + 7 个资产均实际下载，归档摘要与 ELF 架构校验通过，运行时清单摘要匹配文件 | 未执行 Linux 二进制；本次验收范围是文件安装与清单生成 |
| 真实重复安装 | 对上述两套实际安装库再次调用 install，下载函数替换为直接失败，比较清单原始字节 | 两套均通过，未再次下载，清单原样保留 | — |
| 下载器回归 | `python -m unittest discover -s scripts -p test_install_tools.py -v` | 21 项通过，覆盖三种包格式、摘要/路径/链接/架构拒绝、合并、重复、并发锁、清单冲突、写失败、快照保留和旧文件恢复依据 | — |
| 安装流程回归 | `python -m unittest discover -s scripts -p test_install.py -v`；补跑 `-k test_real_compose_round_trip_of_saved_credentials` | 17 项均验证通过；首次 16 项通过、Compose CLI 临时路径失效导致一项失败，重新下载官方 Compose 并校验后该项单独通过 | Docker 启动行为使用命令替身，不声称实际启动容器 |
| 静态检查 | `shellcheck install.sh start.sh`、`bash -n install.sh`、`actionlint -shellcheck= -pyflakes= .github/workflows/docker.yml .github/workflows/release.yml`、`git diff --check` | 通过 | — |

## 收尾

- 下载与安装脚本验收完成；现有服务执行器、数据库导入与审批链路未改动，不将文件验证写成扫描器运行或 Agent 调用通过。
- 官方下载验收产物仅在忽略的 `release-work/tool-bootstrap`，未修改业务 `data/tools`。未提交或推送；保留工作区其它变更。
- 现行规范已同步到 `spec/specs/baseline-tool-install/spec.md` 和 SPEC 索引；README、packaging/README.md、第三方来源说明已更新。文档相对链接、Python 3.8 语法与最终差异检查通过。
- 归档日期：2026-10-12；目录：`spec/changes/archive/2026-10-12-baseline-tool-installer`。
