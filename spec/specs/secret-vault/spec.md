# 机密库

## 目的与边界

集中保存任务中发现的凭据（API key、密码、token、Cookie、证书、私钥、连接串），并与任务、资产、发现、企业、host 和会话建立关联，供后续任务与其他 Agent 检索复用。本规范描述已落地的第一阶段行为；加密存储、现有平台凭据迁移与自动扫描不在范围内。

明文存储是产品选择：PostgreSQL、数据库备份与 `pg_dump` 都能读到值。掩码只保护界面回显与普通接口响应，不保护数据落盘。

## 需求

### SECRET-001：存储与去重

凭据以 `(kind, value)` 的 SHA-256 指纹唯一存储；相同指纹再次写入复用原记录。首次写入的名称、说明与来源保留，后到调用方只补充关联。类型为空时归一为 `other`，未知类型拒绝。值不参与日志与错误信息。

#### 场景：同一凭据被两个任务发现

- GIVEN 任务 A 已写入某密码。
- WHEN 任务 B 用同类型同值再次写入。
- THEN 返回同一个 id，标记为已存在，不产生第二行，且值仍为首次写入的内容。

#### 场景：并发写入

- GIVEN 两个请求同时写入同一凭据。
- WHEN 两个请求完成。
- THEN 两者拿到同一 id，数据库只有一行。

### SECRET-002：来源与自动关联

写入方记录名称、类型、来源说明、host、创建者，以及（任务 run 中）`source_task_id`。任务 run 自动建立 `task` 关联，聊天会话自动建立 `conversation` 关联；调用方可附加 `asset`、`finding`、`company`、`host` 关联。关联幂等。

#### 场景：Agent 在 worker 中写入

- GIVEN worker 正在执行某任务的意图。
- WHEN 调用 `secret_store`。
- THEN 记录 `source_task_id`，并存在一条 `target_type=task` 的关联。

### SECRET-003：跨任务检索

`GET /api/secrets` 与 `secret_search` 支持按关键字（名称/说明/host/来源）、类型、host、任务 id 过滤，按更新时间倒序。结果只含元数据、掩码、来源与关联数；不支持按明文搜索。

#### 场景：其他任务复用发现

- GIVEN 任务 A 已写入凭据。
- WHEN 任务 B 的 Agent 调用 `secret_search`。
- THEN 能看到该凭据的 id、名称、类型、掩码与来源，但看不到明文。

### SECRET-004：受审计取用

`POST /api/secrets/{id}/reveal` 与 `secret_reveal` 返回明文，要求非空 `reason`。成功时写一条 `reveal` 审计并更新 `last_used_at`；缺少原因返回 400，且不返回明文、不写 `reveal` 审计。取用不改变凭据内容。

#### 场景：缺少原因

- GIVEN 调用方未提供原因。
- WHEN 请求明文。
- THEN 返回 400，响应体不含明文。

#### 场景：Agent 取用

- GIVEN worker 在任务中调用 `secret_reveal` 并给出原因。
- WHEN 调用完成。
- THEN 返回明文；审计包含动作、Agent 标识、任务 id 与原因。

### SECRET-005：掩码回显与替换

列表、详情、`secret_store`、`secret_search` 的响应中，值一律是 `__masked__` 或 `__masked__:…末6位`（值不足 7 字节时只给标记）。更新时替换值必须设置 `replace_value=true` 并提交真实新值；提交掩码字面量返回 400，不会写入库。

#### 场景：回填掩码被拒绝

- GIVEN 前端把详情里的掩码值原样提交为替换值。
- WHEN 服务端处理 PATCH。
- THEN 返回 400，库中原值不变。

### SECRET-006：审计与日志边界

创建、读取详情、取用、更新、删除、建立与解除关联都写入 `secret_access_log`，包含动作、操作者、操作者类型、任务/会话、原因与时间。服务端日志（含 `server_logs`）只记录 id、动作、操作者与任务，不记录明文、掩码或密文。删除后审计保留 `secret_name`，`secret_id` 置空。

#### 场景：检索日志

- GIVEN 已完成若干机密操作。
- WHEN 在日志页或 `server_logs` 中检索明文。
- THEN 检索不到。

### SECRET-007：鉴权、删除与边界

所有 `/api/secrets*` 位于统一的 JWT 中间件之后，未认证返回 401。删除凭据级联删除关联，访问审计保留。删除策略删除会立即生效，不保留软删除副本。

#### 场景：未登录访问

- GIVEN 请求未携带有效令牌。
- WHEN 访问任一机密接口。
- THEN 返回 401。

## 已知边界

- 明文随数据库与备份导出；加密存储与密钥轮换需要独立变更。
- `secret_reveal` 的明文会进入当次 run 的工具结果与 `data/transcripts/`，无法事后从模型上下文中撤回。
- 现有平台凭据（`mcp_servers.env`、工具库/技能库源令牌、`llm_profiles.api_key`、通知渠道配置）尚未接入机密库；本阶段不自动迁移。
- 任务删除后 `secret_links` 中的 `task` 关联可能悬空，界面按引用展示，不级联删除凭据。
- 关联表不设多态外键，资产/发现/企业的目标存在性由调用方保证。

## 关联资料

- [统一工作流](../../WORKFLOW.md)
- [统一风格规范](../../STYLE.md)
- [项目约束](../../PROJECT.md)
- [变更记录](../../changes/secret-vault/change.md)
