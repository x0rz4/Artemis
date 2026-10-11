# 基线工具安装

在线运行时更新使用 [官方云更新](REGISTRY.md)；本页保留部署前的离线下载方式。

`tools-lock.json` 是源码中的下载锁文件，`data/tools/manifest.json` 是安装后生成的运行时清单，两者用途不同。下载器使用 Python 3.8+ 标准库，不依赖 pip，也不会执行下载的工具。

## 通过安装脚本准备

```bash
# Linux amd64：部署时下载全部八项；需要宿主机 Python 3.8+
bash install.sh --tools baseline --yes

# 只准备，不启动应用或数据库
bash install.sh --tools baseline --yes --no-start

# 仅选择部分工具
bash install.sh --tools httpx,ffuf --yes

# Linux arm64：kscan 的固定 Release 没有 arm64 资产，显式选其余七项
bash install.sh --tools httpx,naabu,subfinder,katana,ffuf,nuclei,fscan --yes
```

默认安装不下载工具。Docker 模式使用 daemon 的平台；本地模式使用宿主平台。已有 Compose 部署先执行 `docker compose stop artemis`，再运行带 `--tools` 的安装命令。安装器检测到应用仍在运行时会退出；不会自动停止服务。新工具下载失败时，安装流程以非零状态退出。

## 单独下载

先停止使用目标工具库的 Artemis，再运行：

```bash
python3 scripts/install_tools.py --platform linux/amd64

# 仅检查工具选择和平台，不下载或写入
python3 scripts/install_tools.py --platform linux/amd64 --check

# 给另一台 Linux 主机准备工具库
python3 scripts/install_tools.py --platform linux/amd64 --output ./prepared-tools
```

默认输出为仓库的 `data/tools`。Windows/macOS 可显式指定 Linux 平台准备文件，但不能直接运行这些 Linux 二进制。复制到 Linux 后应保留执行权限，并在部署路径下重新导入清单。

脚本要求工具库离线：它的 `.install-tools.lock` 防止两个安装脚本同时写入，不与服务进程内部的写锁互通。异常中断留下锁文件时，先确认没有安装进程，再移除该锁文件。直接调用脚本不会探测或停止 Artemis。

## 固定版本与来源

| 工具 | 版本 | linux/amd64 | linux/arm64 | 官方 Release |
| --- | --- | --- | --- | --- |
| httpx | 1.12.0 | 支持 | 支持 | [httpx](https://github.com/projectdiscovery/httpx/releases/tag/v1.12.0) |
| naabu | 2.6.1 | 支持 | 支持 | [naabu](https://github.com/projectdiscovery/naabu/releases/tag/v2.6.1) |
| subfinder | 2.17.0 | 支持 | 支持 | [subfinder](https://github.com/projectdiscovery/subfinder/releases/tag/v2.17.0) |
| katana | 1.8.0 | 支持 | 支持 | [katana](https://github.com/projectdiscovery/katana/releases/tag/v1.8.0) |
| ffuf | 2.3.0 | 支持 | 支持 | [ffuf](https://github.com/ffuf/ffuf/releases/tag/v2.3.0) |
| nuclei | 3.11.1 | 支持 | 支持 | [nuclei](https://github.com/projectdiscovery/nuclei/releases/tag/v3.11.1) |
| fscan | 2.2.2 | 支持 | 支持 | [fscan](https://github.com/shadow1ng/fscan/releases/tag/v2.2.2) |
| kscan | 1.85 | 支持 | 无对应资产 | [kscan](https://github.com/lcvvvv/kscan/releases/tag/v1.85) |

锁文件使用固定 URL 与 SHA-256，不在安装时追踪 `latest`。七项工具的摘要取自 GitHub 官方仓库的 Release asset digest；kscan 旧 Release 没有该字段，摘要由固定资产下载后计算，再提交到锁文件中供后续安装比对。这提供固定内容校验，不声称具备上游签名认证。

更新版本需修改锁文件中的版本、下载地址、资产摘要与归档成员名，并重新执行下载与测试。归档摘要用于验证下载，运行时清单的 `sha256` 则是提取后可执行文件的摘要。归档中的许可证文件随工具保留，各工具按其上游许可使用。

## 导入与验证

1. 启动 Artemis，进入「系统 → 工具库」，确认文件存在、`sha256_state=ok`。
2. 点击导入，将清单注册为 `kind=command` 工具。新增条目默认 `enabled=false`，选择 Agent 绑定后再启用；已有清单的绑定、启用状态和 schema 会保留。
3. 在目标平台先用工具自身的帮助/版本命令检查运行依赖。安装校验包含 ELF 架构检查，不代替运行依赖检查，也不代表已完成扫描测试。
4. 后续调用复用现有工具执行、审计和审批链路。nuclei 模板、subfinder 数据源密钥等按需要单独配置，本脚本不下载模板库或生成凭据。

脚本不连接 PostgreSQL、不自动导入数据库、不创建任务、不发起目标扫描。`data/tools` 和下载产物仍不进入源码或发行镜像。

## 更新与回滚

重复安装相同版本会检查现有文件摘要，不重复下载或改写清单。选中工具以新内容目录落盘，旧文件保持不变；更新清单时保留未选择的条目、sources、未知字段，以及已有 key、agents、enabled、deferred、schema。

清单切换前备份到 `manifest.json.bak`，并生成工具库页面可识别的 `.snapshots/<id>`，沿用最多五份快照的规则。因为旧二进制未被覆盖，这类快照只需保留旧清单。可通过页面回滚恢复旧路径；手动恢复 `manifest.json.bak` 后须重新导入。为保证回滚，不要自行删除旧内容目录。

摘要错误、归档穿越、链接、目标架构不符或清单发生并发修改时，脚本不会发布新清单。失败可能留下尚未被清单引用的内容目录，不影响现有条目。已有内容目录若被修改，脚本会报错而不覆盖它，应先恢复备份或改用新输出目录。

## 开发验证

```bash
python3 -m unittest discover -s scripts -p 'test_install_tools.py' -v
python3 -m unittest discover -s scripts -p 'test_install.py' -v
```

上述自动测试使用临时目录和伪资产，不执行扫描器或连接数据库。真实下载验收使用独立 `--output` 目录，避免修改业务工具库。
