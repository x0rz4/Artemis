# Artemis

LLM 驱动的自主渗透测试工作台 · Go + Next.js + PostgreSQL

[界面截图](#界面预览) · [开始使用](#开始使用) · [从源码构建](#从源码构建)

基于 [ARTEX](https://github.com/Autumn-27/ARTEX) 改造，保留资产图、任务探索、多代理协作、流量证据和人工审批，增加预算、证据关系、报告版本、完整导出与加密备份恢复。前端参考 Amicro，支持深浅主题与移动端。仅用于拥有或明确获得授权的目标。

**首次使用为空白业务状态。** 源码和发行包不包含演示数据库、任务、资产、漏洞、对话、模型配置、API Key 或管理员密码。首次启动需要自己的 PostgreSQL、登录密码和模型配置。内置 Agent、工具定义、提示词、拦截规则是功能默认项，会自动初始化。

## 界面预览

以下截图中的模拟记录仅展示界面，**不会随安装导入**。发布源码不提供运行时演示数据或模拟登录。

| 浅色工作台 | 深色工作台 |
| --- | --- |
| ![浅色工作台，使用模拟记录](screenshots/dashboard-light.jpg) | ![深色工作台，使用模拟记录](screenshots/dashboard-dark.jpg) |

| 登录页 | 手机工作台 |
| --- | --- |
| ![正式程序登录页](screenshots/login.jpg) | ![移动端工作台，使用模拟记录](screenshots/mobile.jpg) |

## 主要功能

- **任务与多代理**：规划、执行、对话、报告代理；模板、目标、约束、运行状态、模型配置链与纠偏入口。
- **预算与收尾**：token/工具预算、保守预留、耗尽后的收尾、有限默认轮次与无进展保护。
- **资产与范围**：企业/任务资产、范围与拦截、筛选、完整 CSV/JSON 导出、批量挂接、ScopeSentry 同步。
- **发现与证据**：请求/响应绑定、重复关系、复测、报告重生成作业、证据版本和历史报告。
- **会话与流量**：搜索与完整历史导出；流量录制、HAR/CSV 和原始报文包导出。
- **系统与恢复**：LLM、MCP、技能、自定义工具、通知、加密备份、恢复预览、隔离恢复和迁移状态。

约束提供代理上下文；工具拦截规则独立执行。token 预算是本地用量账本，不是供应商金额账单。

## 开始使用

### 一键安装（Linux / macOS）

```bash
git clone https://github.com/x0rz4/Artemis.git
cd Artemis
bash install.sh
```

默认使用 Docker Compose 部署 Artemis 与 PostgreSQL。需要 Docker 和 Compose **2.20 或更新版本**；Linux 缺少 Docker 时可使用 `bash install.sh --install-docker` 安装，macOS 请先启动 Docker Desktop。脚本检测 Docker daemon 权限，首次生成随机数据库密码到权限为 `600` 的 `.env`；如果通过环境变量提供数据库参数、镜像标签和端口，会保存这些首次安装参数。已有 `.env` 和 `config.json` 会保留。

```bash
# 无交互安装；优先拉镜像，拉取失败时从当前源码构建
bash install.sh --mode docker --yes

# 直接从源码构建镜像（宿主机无需 Go、Node.js）
bash install.sh --mode docker --build --yes

# 只准备配置和镜像，不启动服务
bash install.sh --mode docker --no-start --yes
```

服务通过健康检查后打开 **http://127.0.0.1:8787/setup**，设置管理员密码，再在「系统 → LLM」添加模型。默认 Web 和流量代理端口仅绑定本机；远程访问可在 `.env` 设置 `ARTEX_BIND=0.0.0.0`。Web、代理端口分别由 `ARTEX_PORT`、`ARTEX_PROXY_PORT` 控制。

镜像地址为 `ghcr.io/x0rz4/artemis`，支持 Linux amd64 / arm64。默认 `ARTEX_TAG=latest`，对应已成功发布的稳定版本；首个稳定版本发布前可用 `ARTEX_TAG=edge bash install.sh --yes` 安装 main 分支镜像，或使用 `--build`。Fork 仓库可以在 `.env` 中修改 `ARTEX_IMAGE`。网络受限时，源码构建可通过 `GOPROXY`、`NPM_REGISTRY` 指定自己的依赖源，默认使用官方源。

手动部署与更新：

```bash
cp .env.example .env
# 编辑 .env，设置自己的 POSTGRES_PASSWORD（没有默认密码）
docker compose pull
docker compose up -d --no-build --wait --wait-timeout 180
docker compose logs -f artemis

# 更新：先做好备份，再拉取指定标签并重建容器
docker compose pull artemis
docker compose up -d --no-build --wait --wait-timeout 180 artemis

# 停止服务，保留数据库卷
docker compose down
```

PostgreSQL 业务数据保存在 `pgdata` 卷，JWT 签名密钥保存在独立的 `authdata` 卷（`/app/auth/jwt.key`），工作区、录制和上传库保存在 `./data`；`./skills` 保留宿主机技能修改。更新与备份需保留这些数据，**不要使用 `docker compose down -v` 删除持久卷**。容器更新使用 Compose 拉取/重建。

旧容器若把密钥写在 `/app/jwt.key`，首次更新到独立认证卷后需要重新登录，已有管理员密码保持不变；如需保留原会话，应在移除旧容器前备份该文件并迁入 `authdata` 卷的 `jwt.key`（权限 `600`）。后续保留认证卷重建时，未过期的 token 会继续有效。本地运行默认仍使用程序旁的 `jwt.key`，可用 `ARTEX_JWT_KEY_DIR` 指定工作区之外的密钥目录。

本地编译模式需要 Go（版本见 `go.mod`）、Node.js 22、npm、rsync、Python 3.8+，始终构建内嵌静态前端的 `artemis`：

```bash
# 交互输入已有 PostgreSQL 参数；编译后前台运行，Ctrl-C 停止
bash install.sh --mode local

# 由 Compose 提供 PostgreSQL，仅绑定 127.0.0.1:5433
bash install.sh --mode local --database docker
```

本地无交互连接已有数据库时，通过 `POSTGRES_HOST`、`POSTGRES_PORT`、`POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_DB`、`POSTGRES_SSLMODE` 提供参数，再添加 `--yes`；已有 `ARTEX_PG_DSN` 或 `ARTEX_CONFIG` 按原有优先级使用。`--database docker` 使用 `.env` 内的数据库参数，宿主机端口可用 `ARTEX_PG_PORT` 修改；重跑相同命令会保留配置并启动已有数据库容器。

### 可选基线工具

日常更新可在「系统 → 工具库 / Skill → 官方云更新」选择安装，默认每六小时只检查、不自动安装。公开源为 [Artemis-registry](https://github.com/x0rz4/Artemis-registry)，无需 GitHub Token。支持冲突提示、固定版本更新和快照回滚，详见 [云更新与部署说明](packaging/REGISTRY.md)。

Linux amd64 部署可使用 `bash install.sh --tools baseline --yes`，下载并校验 httpx、naabu、subfinder、katana、ffuf、nuclei、fscan、kscan，生成 `data/tools/manifest.json`。宿主机需 Python 3.8+；已有部署先停止 Artemis。工具默认禁用，启动后在工具库页面导入，再设置 Agent 绑定并启用。

支持 `--tools httpx,ffuf` 按需选择，也可单独运行 `python3 scripts/install_tools.py --platform linux/amd64`。arm64 可安装除 kscan 外的七项工具。固定版本、更新、回滚与平台说明见 [基线工具安装](packaging/README.md)。

### Windows 发行包

1. 用 `scripts/package-release.ps1` 生成 Windows amd64 zip（见[从源码构建](#从源码构建)），解压到新的目录。
2. 准备 PostgreSQL 16 或更新版本。可连接自己的数据库，或仅用 Docker 创建空库：

   ```powershell
   docker run -d --name artemis-postgres -p 127.0.0.1:5433:5432 -e POSTGRES_USER=artemis -e POSTGRES_PASSWORD=YOUR_DATABASE_PASSWORD -e POSTGRES_DB=artemis -v artemis-pgdata:/var/lib/postgresql/data postgres:16
   ```

   将 `YOUR_DATABASE_PASSWORD` 换成自己的强密码。
3. 复制 `config.example.json` 为 `config.json`，填自己的数据库参数。示例是 `127.0.0.1:5433`、用户/数据库 `artemis`，密码为空，必须填写。`skill_dir` 留空使用程序旁的 `skills/`。
4. 运行 `start.bat`，或在 PowerShell 中：

   ```powershell
   .\start.bat -addr 127.0.0.1:8787 -proxy 127.0.0.1:8788
   ```

5. 打开 [本地工作台](http://localhost:8787)，首次进入初始化页，设置至少 8 位登录密码。在「系统 → LLM」添加自己的模型，再创建企业、资产、任务或对话。

发行包由 `start.bat` 守护启动，可在「系统 → 设置 → 版本与更新」检查并一键升级到 [Artemis 发布源](https://github.com/x0rz4/Artemis/releases) 上的最新正式版：下载本平台 zip、比对 `SHA256SUMS`、冒烟测试通过后换装，启动失败会自动回滚。容器部署不适用这条路径，更新仍使用 `docker compose pull`。

发行包内可执行文件名为 `artemis.exe`。原有 `ARTEX_*` 环境变量与 Go module 路径保留兼容名称——它们是内部标识，不是产品名；`ARTEX_PG_DSN` 优先于配置文件，如之前设置过，请确认它指向预期数据库。

本次首发提供 Windows amd64 包。其他平台可从源码编译；跨平台 CI 的结果以对应运行记录为准。

### 从源码构建

需要 Go **1.26.3 或更新版本**、Node.js **22**、npm 和 PostgreSQL。

```powershell
# 在 Artemis 源码目录下
.\build.bat
Copy-Item config.example.json config.json
# 编辑 config.json 后启动
.\start.bat -addr 127.0.0.1:8787 -proxy 127.0.0.1:8788
```

Windows 可分发 zip：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/package-release.ps1 -Version 2.3.0
```

打包脚本按固定文件清单组装，不复制配置、JWT 密钥、数据库、日志或工作区文件；输出到 `dist/`，拒绝覆盖已有同版本产物。

Linux/macOS 需要 Bash、rsync；打包还需 zip：

```bash
./build.sh --target linux/amd64
cp config.example.json config.json
# 编辑 config.json 后启动
./dist/artemis-linux-amd64/artemis -addr 127.0.0.1:8787 -proxy 127.0.0.1:8788
```

### 开发模式

后端 `go run ./cmd/artex`；另一终端进入 `web`，执行 `npm ci`、`npm run dev -- --port 5173`。前端 API 默认反代后端 8787。未配置模型时没有预置模型，也不会自动调用供应商。[项目约束](spec/PROJECT.md) 记录代码入口与配置优先级。

开发前阅读 [Artemis SPEC](spec/README.md)：所有助手通过 AGENTS.md 与 CLAUDE.md 遵循同一套[工作流](spec/WORKFLOW.md)，代码、界面和文案风格统一维护在[风格规范](spec/STYLE.md)。

## GitHub Actions 镜像发布

[container 工作流](.github/workflows/docker.yml) 在 main 推送、`v*` 标签推送和 PR 时执行，也可在 Actions 页面手动运行。PR 和手动取消 `publish` 时只验证；其余发布到 `ghcr.io/<仓库所有者>/<仓库名小写>`，当前仓库对应 `ghcr.io/x0rz4/artemis`。

- main 构建发布 `edge` 和 `sha-<完整提交哈希>`。
- `v2.3.0` 这样的稳定标签发布 `v2.3.0`、`2.3.0` 和 `latest`。
- `v2.3.1-rc.1` 这样的预发布标签只发布对应版本及提交标签，不覆盖 `latest`。
- 两种架构使用各自原生 runner 构建，分别验证初始化、登录鉴权、空业务/模型列表及容器重建后的持久化。两边通过后才合并发布多架构标签；`build-<运行号>-<尝试号>-<架构>` 为中间镜像标签。

GHCR 使用工作流自带的 `GITHUB_TOKEN` 与 `packages: write`，无需 Docker Hub 账号或额外 Secrets。首次发布后，在 GitHub 包设置中将镜像可见性设为 **Public**，才能匿名拉取；如果包已存在，确认该仓库获得包的 Actions 写权限。保留私有镜像时，安装机器需先 `docker login ghcr.io`。

发行 zip 仍使用独立的 [release 工作流](.github/workflows/release.yml)，手动指定已有版本标签。容器不依赖 zip、宿主机预编译文件或运行数据。安装流程参考 [mhtsec/ARTEX 的 install.sh](https://github.com/mhtsec/ARTEX/blob/main/install.sh)，镜像发布参考其 release 工作流，并适配 Artemis 品牌、GHCR 和现有配置约定。

## 数据与备份

- 业务和模型凭据保存在自己的 PostgreSQL；工作区与录制文件在应用数据目录。JWT 签名密钥首次运行随机生成。
- 不提交或转发 `config.json`、`.env`、`jwt.key`、`data/`、日志、抓包和备份；源码忽略规则覆盖这些文件。
- 制作一致备份前停止任务、应用和代理；恢复使用新的空数据库与新的数据目录。
- 连接已有数据库会保留原有记录并执行幂等迁移。“首次为空白”指新建数据库，不会清空旧数据库。
- 打包脚本同时生成 `dist/SHA256SUMS`，可与发布包对比：`Get-FileHash .\artemis-2.3.0-windows-amd64.zip -Algorithm SHA256`。

## 许可证与来源

遵守 [AGPL-3.0](LICENSE)，保留 ARTEX 源码来源及版权。修改后通过网络向其他用户提供服务时，按许可证提供对应源码。

字体和前端模板许可见 [第三方说明](THIRD_PARTY_NOTICES.md)。UI 参考 [Amicro](https://github.com/Subhan-code/Amicro--Micro-transitions-)，产品布局与流程按 Artemis 调整。
