# 观测

## 目的与边界

后端日志同时是运行诊断入口与「系统 → 日志」页面的数据源：一行一条文本，带显式级别与标签，落 stderr、内存环、`server_logs` 表并推送给 SSE 订阅者。本规范描述已落地的日志契约与四类事件（task / intent / tool / decision）的结构化要求。

仍然存在、本规范不覆盖的部分：运行期（页面或接口）调整级别的入口尚未提供，级别只能在启动时由环境变量决定；大量历史调用点仍走标准库 `log`，级别由 `server/logsink.go` 的关键词表推断，属过渡状态。

## 需求

### OBS-001：结构化日志入口与文本契约

后端必须提供结构化日志入口，级别与标签由调用方显式给出，不依赖消息文本猜测；输出必须是单行 `[tag] 消息 key=value …` 文本，并进入现有日志汇（stderr、内存环、`server_logs`、SSE）。字段值含空白时必须加引号以保留字段边界。未安装日志汇时不得 panic 或丢日志。

#### 场景：级别由调用方决定

- GIVEN：一条消息里含"失败"字样的 info 日志。
- WHEN：写入日志汇。
- THEN：级别为 info；只有用 Warn / Error 调用时才是 warn / error。

#### 场景：标签与字段

- GIVEN：携带任务与意图标识的日志。
- WHEN：写入日志汇。
- THEN：文本形如 `[worker] 领取意图 task=t-7 intent=12`，标签不重复出现在正文里，字段值含空白时被引号包裹。

### OBS-002：四类事件的关键站点

task、intent、tool、decision 四类事件的关键站点必须走结构化入口，并带可关联标识：任务超时收尾与任务控制、意图领取与收场、活动写入重试与丢弃、工具 panic、拦截与审批决策。决策类只记非放行结果（拦截、拒绝、审批请求、超时、模型兜底）与决策记录落库失败。日志不得写入工具命令行或输入原文。

#### 场景：意图终态

- GIVEN：worker 结束一条意图并判定为 exhausted。
- WHEN：写终态日志。
- THEN：日志带 `task`、`intent`、`worker`、`state` 字段。

#### 场景：人工审批

- GIVEN：拦截规则命中 ask。
- WHEN：创建 pending 并等待用户决定。
- THEN：产生一条 warn 级决策日志，带 `decision=ask`、`tool`、`pending`；用户放行时不额外刷日志。

#### 场景：工具 panic

- GIVEN：工具处理函数 panic。
- WHEN：工具守卫恢复该 panic。
- THEN：产生一条 error 级日志，堆栈截断为单行字段并带工具名。

### OBS-003：高频日志源的分级与字段

高频失败路径的日志源必须走结构化入口，一次给出阶段与关联字段，级别按严重度显式指定；URL 不得带查询串与片段进日志。同一事件不得刷多条同形诊断。

#### 场景：流量写入失败

- GIVEN：索引库写入在任一阶段失败（开启事务、写索引、取 rowid、写正文、登记 blob 引用、写全文索引、提交）。
- WHEN：记录一条流量。
- THEN：产生一条 warn 级日志，带 `stage`、`url`（已去查询串与片段）与 `err`。

#### 场景：LLM 轮询链耗尽

- GIVEN：轮询链上的配置全部失败。
- WHEN：流式与非流式路径分别走到耗尽。
- THEN：各产生一条 error 级日志，带失败配置数量与最后错误；熔断与故障转移各自带 `profile`/`model` 与下一跳字段。

### OBS-004：日志级别开关

最低输出级别必须由 `ARTEX_LOG_LEVEL`（`debug|info|warn|error`，缺省 `info`，大小写不敏感）决定，且对结构化日志与标准库 `log` 的行使用同一阈值，过滤效果在 stderr、内存环、`server_logs` 与 SSE 上一致。无效取值必须回落 `info` 并给出可读提示；启动时必须说明当前生效的阈值。

#### 场景：调高阈值

- GIVEN：`ARTEX_LOG_LEVEL=warn`。
- WHEN：分别产生一条 info 与一条 warn 日志（结构化调用与标准库 `log` 各来一遍）。
- THEN：只有 warn 及以上的行出现在 stderr、内存环、`server_logs` 与 SSE。

#### 场景：写错取值

- GIVEN：`ARTEX_LOG_LEVEL=verbose`。
- WHEN：进程启动。
- THEN：阈值回落 `info`，stderr 上出现一行提示，日志量不因笔误变化。

## 关联资料

- [统一工作流](../../WORKFLOW.md)
- [统一风格规范](../../STYLE.md)
- [项目约束](../../PROJECT.md)
- 代码入口：`logging/logging.go`（入口与字段构造器）、`server/logsink.go`（文本契约、入库与 SSE）、`web/src/app/(main)/system/logs/page.tsx`（展示）
