# 第三方来源与许可证

- **ARTEX**：基于 https://github.com/Autumn-27/ARTEX 改造，保留 AGPL-3.0 许可证及源码版权声明。Go module 和环境变量沿用兼容名称。
- **安装与容器构建参考**：https://github.com/mhtsec/ARTEX 的 `install.sh`、`Dockerfile` 和 `.github/workflows/release.yml`，按 Artemis 配置与 GHCR 发布流程调整，保留 AGPL-3.0 来源说明。
- **前端模板**：继承模板的许可证见 `web/LICENSE`，发行包放入 `licenses/web-LICENSE`。
- **Outfit 字体**：许可位于 `web/src/lib/fonts/assets/Outfit-LICENSE.txt`，发行包同步包含；字体许可与应用 AGPL 许可分别适用。
- **Amicro 设计参考**：https://github.com/Subhan-code/Amicro--Micro-transitions- 。产品页面和流程按 Artemis 调整。
- **依赖**：Go/npm 版本与完整性信息记录于 `go.mod`、`go.sum`、`web/package-lock.json`；各依赖按自己的许可证使用。
- **可选基线工具**：`packaging/tools-lock.json` 指向各工具官方 Release，版本与来源见 [基线工具安装](packaging/README.md)。这些二进制由用户选择下载，不随 Artemis 源码或镜像分发；安装器保留归档中的许可证文件，各工具按其上游许可使用。
- **官方云更新**：[Artemis-registry](https://github.com/x0rz4/Artemis-registry) 独立发布工具与 Skill ZIP，锁定上游资产及许可证摘要；工具包包含来源和许可，GPL-3.0 的 kscan 同时提供对应版本源码归档。Playwright Skill 保留 Microsoft 的 Apache-2.0 许可，其余继承内容按各自来源许可分发。
