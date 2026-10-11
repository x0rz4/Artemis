# 工具与 Skill 云更新

官方源为公开仓库 [x0rz4/Artemis-registry](https://github.com/x0rz4/Artemis-registry)，首版 [v1.0.0](https://github.com/x0rz4/Artemis-registry/releases/tag/v1.0.0)。应用镜像继续由 Artemis 主仓库发布到 GHCR；工具和 Skill 的 ZIP 独立使用 Registry Releases 分发。部署端无需 GitHub Token。

## 使用

在「系统 → 工具库」或「系统 → Skill」打开“官方云更新”：

1. 程序启动后检查一次，此后每六小时检查；可关闭“每 6 小时自动检查”，也可点击“立即检查”。检查只读取稳定索引和两份清单。
2. 查看本地/云端版本、更新内容和支持平台，选择需要的条目。Windows 可以更新 Skill，首批工具包仅支持 Linux amd64/arm64；arm64 不提供 kscan。
3. 本地修改或同名自定义内容显示冲突，默认跳过。确需更新时勾选该项的“允许用官方版本替换此条目”，再确认更新。
4. 应用和回滚要求任务、聊天空闲。忙碌或已有写操作时返回 409，稍后重试。下载校验可以后台进行。
5. 更新前自动保存快照，在同一面板选择快照回滚。最多保留五份；不会因云端删除条目而删除本地文件。

回滚以库条目为单位恢复：被恢复目录内部回到快照内容，未包含在快照中的其它工具或 Skill 保留。

工具更新保留 key、数据库中的 Agent 绑定、启用状态及 deferred；新工具默认禁用，确认用途并配置绑定后再启用。安装包 SHA-256、落盘内容 SHA-256 和工具入口文件 SHA-256 分开保存，重复检查不会再次提示同一包有更新。

Linux 本地部署需安装工具运行库，例如 naabu 所需的 libpcap（Debian/Ubuntu：`apt-get install libpcap0.8`）。Artemis 容器的 nmap 依赖已包含 libpcap。

## 持久化和 Skill 优先级

需要保留整个 `data/`，包括 `registry-state.json`、`tools/`、`skills/` 和两类库的 `.snapshots/`。云端版本放在各库的 `managed/` 内，由清单引用决定是否生效。更新失败或断网不切换清单，原版本继续使用；不要手工移动这些目录。

Skill 读取顺序：显式设置的 `ARTEX_SKILL_DIR` / `skill_dir` → 已激活官方版本 → 默认程序旁的内置 `skills/` → 上传目录 → git 源目录。相同名称以靠前的根为准。

显式本地根始终优先，即使已确认下载官方版本，页面也提示本地覆盖。解除对应本地配置或移走同名内容后才会使用官方版本。默认内置文件若被修改也会提示冲突；确认后官方版本优先生效，但内置文件仍保留。更新与回滚后的下一轮 Agent 调用重新读取当前 Skill，不需要重启。

旧的自定义 HTTP/GitHub/git 源继续可用，离线安装入口 `install.sh --tools ...` 和 `scripts/install_tools.py` 保留。离线内容与官方条目同名时提示冲突，由用户决定是否转为官方管理。

## API

- `GET /api/library-updates?kind=tools|skills`：检查状态、平台、版本、条目和错误。
- `POST /api/library-updates/check?kind=tools|skills`：立即检查。
- `PUT /api/library-updates/settings`，请求 `{"disabled":true}`：关闭自动检查（两个库共用设置）。
- `POST /api/tool-lib/sync` 或 `/api/skills/sync`：官方更新请求示例：

```json
{"names":["artemis-registry"],"release":"v1.0.0","items":["api-recon"],"replace":[]}
```

`names` 仍是源名称，`items` 为条目名称，`replace` 为明确允许替换冲突的条目。官方源必须单独提交；`release` 必须是该实例检查过的版本。`dry_run:true` 返回计划，不下载包。确认后的版本固定到缓存清单及包摘要；稳定渠道后来推进不会改变本次应用目标。回滚沿用两类库现有的 `/snapshots` 和 `/rollback` 接口。

## 维护发布

按 [Registry 发布说明](https://github.com/x0rz4/Artemis-registry#发布) 更新锁文件并推送新标签。CI 下载固定资产、核验许可证与架构、生成 ZIP 和清单，在两个原生架构运行帮助或版本命令。先上传草稿 Release 并重新下载逐个核验，公开后才更新稳定索引；不覆盖已存在的版本。

主程序内置 Skill 修改时，同步更新 `server/registry_baseline.json` 的内容摘要，避免将新的正式内置文件误判为用户修改。该文件兼容源码检出时的 LF/CRLF；云端 ZIP 的内容摘要始终按发布包原始字节计算。
