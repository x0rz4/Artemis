# secret-vault：任务发现机密的集中存储与共享

- 状态：implementing
- 类型：重点（鉴权、安全边界、数据持久化）
- 关联能力：机密库（新增，验证完成后建立现行能力规范）、[工具库与技能库](../../specs/tool-library/spec.md)（后续接入点）、[观测](../../specs/observability/spec.md)（日志不落明文）

## 目标与范围

- 问题与当前行为：任务执行中发现的 key、密码、token、Cookie、证书等只能写进 facts/notes 或工作区文件，无法跨任务、跨 Agent 检索复用；平台自身凭据也分散在 `settings`、`llm_profiles.api_key`、`mcp_servers.env`、`notification_channels.config`、`tools.exec.headers` 等处，没有统一的关联与访问审计。
- 预期结果：新增独立的机密库能力。Agent 可显式写入发现凭据并自动关联当前任务/会话；后续任务可按类型、host、关键字检索；需要实际使用时显式取用并留下审计。所有列表与详情接口均不回显明文。
- 范围：机密表、关联表、访问审计表；`/api/secrets*` 管理接口；`secret_store` / `secret_search` / `secret_reveal` 三个 Agent 工具；系统「机密库」页面；`spec/PROJECT.md` 与能力索引同步。
- 不做的内容：不自动从工具输出中扫描提取凭据（由 Agent 显式调用写入）；不做加密存储与密钥轮换（独立变更）；不迁移现有平台凭据（MCP env、源 token、LLM key、通知渠道）到机密库；不把明文注入提示词或系统消息。
- 依据与待确认事项：用户明确要求「把发现的 key、密钥信息关联添加到单独的机密模块，用于信息共享和关联，方便其他任务获取和调用」，并选择第一阶段只做 Agent 发现链路、明文存储 + 掩码回显、Agent 侧提供存储 + 检索 + 受审计取用。

## 需求增量

### ADDED SECRET-001 集中存储与去重

凭据按 `kind + value` 计算指纹并唯一存储。相同指纹再次写入时复用原记录，只补充关联与说明，不产生重复行。

- GIVEN Agent 在任务中取得一个凭据。
- WHEN 调用 `secret_store` 写入。
- THEN 返回记录 id 与掩码提示；同一值再次写入返回同一 id 并标记为已存在。
- GIVEN 两个并发请求写入同一凭据。
- WHEN 两个请求同时完成。
- THEN 只存在一行记录，两个请求都拿到同一 id。

### ADDED SECRET-002 自动关联

写入时根据运行上下文自动关联当前任务与聊天会话；调用方可以额外声明 host、资产、发现或企业关联。

- GIVEN 在任务 run 中写入凭据。
- WHEN 入库完成。
- THEN 记录 `source_task_id`，并存在一条指向该任务的 `secret_links` 记录。
- GIVEN 同一凭据先后被两个任务发现。
- WHEN 第二次写入。
- THEN 两条任务关联都存在，凭据仍只有一行。

### ADDED SECRET-003 跨任务检索

检索接口与工具只返回元数据、掩码、来源和关联摘要，不返回明文。

- GIVEN 任一已认证调用方。
- WHEN 调用 `GET /api/secrets` 或 `secret_search`。
- THEN 响应中没有任何字段等于库中明文。
- GIVEN 按 `kind`、`host`、`task_id` 或关键字过滤。
- WHEN 查询完成。
- THEN 只返回匹配记录，且结果按更新时间倒序。

### ADDED SECRET-004 受审计取用

取用明文必须说明原因并写访问审计；取用不会改变凭据内容。

- GIVEN 调用方需要实际使用某条凭据。
- WHEN 调用 `secret_reveal` 或 `POST /api/secrets/{id}/reveal` 且提供非空原因。
- THEN 返回明文，`secret_access_log` 新增一条 `reveal` 记录，`last_used_at` 更新。
- GIVEN 未提供原因。
- WHEN 调用取用接口。
- THEN 返回 400，不返回明文，不写 `reveal` 审计。

### ADDED SECRET-005 掩码回显与管理

管理界面与详情接口只展示掩码与末位提示；替换值必须显式提交新值。

- GIVEN 用户打开机密详情。
- WHEN 页面加载完成。
- THEN 值区域显示掩码与末 6 位提示（值过短则只显示掩码标记）。
- GIVEN 用户提交带掩码字面量的更新请求。
- WHEN 服务端处理。
- THEN 拒绝该请求，不把掩码写入库。

### ADDED SECRET-006 审计与日志边界

所有写入、读取、展示、更新、删除与关联动作都写入访问审计；服务端日志只记录 id、动作、操作者与任务，不记录明文。

- GIVEN 任一机密操作完成。
- WHEN 查看访问审计或 `server_logs`。
- THEN 审计含动作、操作者、任务与时间；日志中检索不到明文。

### ADDED SECRET-007 鉴权与删除

所有 `/api/secrets*` 接口遵循统一 JWT 鉴权；删除需要显式动作并保留删除前审计。

- GIVEN 未携带有效令牌。
- WHEN 访问任一机密接口。
- THEN 返回 401。
- GIVEN 删除一条存在关联的凭据。
- WHEN 删除完成。
- THEN 凭据与关联被删除，访问审计保留 `delete` 记录与凭据名称。

## 设计与影响

- 实现入口与方案：`db/schema.sql` 追加 `secrets`、`secret_links`、`secret_access_log`；新增 `db/secrets.go` 数据访问、`secret/secret.go` 领域校验与掩码、`server/secrets_api.go` 管理接口、`server/secret_tools.go` Agent 工具；前端新增 `/system/secrets` 页面与侧边栏入口。
- 关键取舍：明文存储是用户明确选择，换取实现简单与可直接使用；去重按 `kind+value` 合并，同一值不同用途通过名称、说明与关联表达；掩码只用于展示，不作更新哨兵，替换必须提交新值。
- API / 数据 / 配置 / 兼容性影响：新增表与接口，不修改既有表；无新增环境变量；新库由幂等 schema 自动建表，老库启动时同样建表，不需要数据迁移。
- 失败、并发与边界：写入用 `ON CONFLICT (fingerprint)` 保证并发去重；删除凭据级联删除关联，访问审计通过 `ON DELETE SET NULL` 保留；任务删除后关联可能悬空，前端显示为「已引用」而不是级联删除凭据。
- 迁移与恢复方式：纯新增，无迁移；回滚删除新表与新路由即可，既有业务不受影响。
- 安全边界：明文会出现在 PostgreSQL、备份与 `pg_dump` 中；`secret_reveal` 的明文会进入本次 run 的工具结果与 `data/transcripts/`；Windows 无 0600 语义、`data/` 权限偏宽等既有缺口沿用 security-defaults 记录，不在本变更声称已解决。
- 适用规范及例外理由：遵循 [WORKFLOW](../../WORKFLOW.md)、[STYLE](../../STYLE.md)、[PROJECT](../../PROJECT.md)；不违反日志文本契约与 JWT 全域鉴权。

## 任务

- [x] 1. SECRET-001/002/005：建表与 `db/secrets.go`，含指纹去重、关联与掩码提示；测试已编写，隔离 PG 未配置时跳过。
- [x] 2. SECRET-001/005：`secret/secret.go` 的类型校验、掩码与指纹；`go test ./secret` 通过。
- [x] 3. SECRET-003/004/006/007：`server/secrets_api.go` 与路由，鉴权、掩码 DTO、reveal 审计；测试已编写，隔离 PG 未配置时跳过。
- [x] 4. SECRET-002/003/004：`server/secret_tools.go` 三个工具与播种/默认绑定；测试已编写。
- [x] 5. SECRET-005：前端机密库页面、侧边栏、API 客户端与类型；`tsc --noEmit` 与 `next build` 通过。
- [x] 6. SECRET-006：审计与日志只记录 id/动作/操作者/任务。
- [x] 7. 同步现行规范与文档：新建 `spec/specs/secret-vault/spec.md`，更新 `spec/README.md`、`spec/PROJECT.md`。
- [ ] 8. 在隔离 PostgreSQL 上执行机密库集成测试与人工验收，然后归档本变更。

## 验证记录

| 需求 / 检查项 | 执行位置与命令或操作 | 实际结果 | 未完成原因 |
| --- | --- | --- | --- |
| SECRET-005 | `go test ./secret -count=1` | 通过：掩码、短值不泄漏、指纹含类型与分隔符、kind 与 schema CHECK 一致 | — |
| SECRET-001/002/006 | `go test ./db -run '^TestSecret' -count=1` | 未执行：本机未配置 PostgreSQL，测试自行跳过（跳过不算通过） | 需隔离 PG |
| SECRET-003/004/007 | `go test ./server -run '^TestSecret' -count=1` | 未执行：同上，测试自行跳过 | 需隔离 PG |
| 编译与静态检查 | `go build ./...`、`go vet ./secret ./db ./server`、`gofmt -l`（新增文件） | 通过 | — |
| 前端类型与构建 | `cd web && npx tsc --noEmit`、`npx next build`（生成 `/system/secrets`） | 通过 | — |
| 前端风格 | `npx biome check "src/app/(main)/system/secrets"` | 通过；仓库既有 `src/lib` 文件的 CRLF 格式告警与本次改动无关 | — |
| 路由鉴权 | 代码核对：`/api/secrets*` 注册在 `Handler()` 内、位于 `requireAuth` 之前的手工路由之后，仍由统一 JWT 中间件覆盖 | 设计确认；实际 401 由 SECRET-007 集成测试覆盖 | 待隔离 PG |

## 收尾

- 现行规范与通用文档更新位置：`spec/specs/secret-vault/spec.md`、`spec/README.md`、`spec/PROJECT.md`。
- 剩余限制或阻塞：本机没有可用的隔离 PostgreSQL，`db`/`server` 的机密库集成测试执行时自行跳过；按 WORKFLOW 不计为通过，归档前必须在隔离库上跑完。明文存储的加密与轮换、现有平台凭据接入、备份策略对机密表的显式处理留待后续独立变更。
- 实现入口：`secret/secret.go`、`db/secrets.go`、`server/secrets_api.go`（含三个 Agent 工具）、`web/src/app/(main)/system/secrets/`。
- 已知边界：`secret_reveal` 的明文会进入当次 run 的工具结果与 `data/transcripts/`；Agent 工具不自动扫描输出，写入由模型显式调用。
- 归档日期与目录：待验证完成后填写。
