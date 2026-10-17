package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/secret"
	actool "github.com/Autumn-27/norma/tool"
)

// 本文件是机密库的 HTTP 接口。全部路由挂在 requireAuth 之后（见 Handler()）。
//
// 对外 DTO 的原则：任何列表/详情响应都只含掩码，明文只能通过 reveal 接口取得，
// 且必须给出原因并写审计。解密/取明文只发生在服务端内部。

// secretDTO 是机密的对外表述。Value 始终是掩码值（secret.Mask 生成），
// 前端把它展示给用户即可；更新时提交掩码字面量会被拒绝。
type secretDTO struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	Value        string           `json:"value"` // 掩码，如 "__masked__:…ab12cd"
	ValueHint    string           `json:"value_hint"`
	Source       string           `json:"source"`
	SourceTaskID *int64           `json:"source_task_id,omitempty"`
	Host         string           `json:"host"`
	CompanyID    *int64           `json:"company_id,omitempty"`
	Notes        string           `json:"notes"`
	Metadata     any              `json:"metadata,omitempty"`
	CreatedBy    string           `json:"created_by"`
	CreatedAt    any              `json:"created_at"`
	UpdatedAt    any              `json:"updated_at"`
	LastUsedAt   any              `json:"last_used_at,omitempty"`
	LinkCount    int              `json:"link_count"`
	Links        []*db.SecretLink `json:"links,omitempty"`
}

func toSecretDTO(s *db.Secret, linkCount int) secretDTO {
	dto := secretDTO{
		ID: s.ID, Name: s.Name, Kind: s.Kind,
		Value: secret.Mask(s.Value), ValueHint: s.ValueHint,
		Source: s.Source, SourceTaskID: s.SourceTaskID, Host: s.Host,
		CompanyID: s.CompanyID, Notes: s.Notes,
		CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		LastUsedAt: s.LastUsedAt, LinkCount: linkCount,
	}
	if len(s.Metadata) > 0 && string(s.Metadata) != "{}" {
		var meta any
		if json.Unmarshal(s.Metadata, &meta) == nil {
			dto.Metadata = meta
		}
	}
	return dto
}

// secretRequest 是创建/更新请求体。Value 只用于写入，永不出现在响应里。
type secretRequest struct {
	Name     *string `json:"name"`
	Kind     *string `json:"kind"`
	Value    *string `json:"value"`
	Source   *string `json:"source"`
	Host     *string `json:"host"`
	Notes    *string `json:"notes"`
	Metadata any     `json:"metadata"`
	// Links 在创建时一并声明关联，元素形如 {"target_type":"asset","target_ref":"12"}。
	Links []secretLinkRequest `json:"links"`
	// ReplaceValue 显式声明「本次提交的是新凭据值」。缺省 false 时忽略 value，
	// 避免前端表单里的掩码回填被误当成新值。
	ReplaceValue bool `json:"replace_value"`
}

type secretLinkRequest struct {
	TargetType string `json:"target_type"`
	TargetRef  string `json:"target_ref"`
}

func (s *Server) secretsMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"kinds":         secret.Kinds(),
		"link_types":    []string{"task", "asset", "finding", "company", "host", "conversation"},
		"masked_prefix": secret.MaskedPrefix,
	})
}

func (s *Server) secretsList(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	q := r.URL.Query()
	f := db.SecretFilter{
		Q:      strings.TrimSpace(q.Get("q")),
		Kind:   strings.TrimSpace(q.Get("kind")),
		Host:   strings.TrimSpace(q.Get("host")),
		TaskID: int64(atoiDefault(q.Get("task_id"), 0)),
		Limit:  atoiDefault(q.Get("limit"), 100),
	}
	if f.Kind != "" && !secret.ValidKind(f.Kind) {
		writeErr(w, http.StatusBadRequest, "机密类型无效")
		return
	}
	list, err := pg.ListSecrets(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取机密列表失败")
		return
	}
	ids := make([]int64, 0, len(list))
	for _, it := range list {
		ids = append(ids, it.ID)
	}
	counts, err := pg.CountSecretLinks(r.Context(), ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取关联统计失败")
		return
	}
	out := make([]secretDTO, 0, len(list))
	for _, it := range list {
		out = append(out, toSecretDTO(it, counts[it.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": out})
}

func (s *Server) secretsCreate(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req secretRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name 必填")
		return
	}
	if req.Value == nil || strings.TrimSpace(*req.Value) == "" {
		writeErr(w, http.StatusBadRequest, "value 必填")
		return
	}
	if secret.IsMasked(*req.Value) {
		writeErr(w, http.StatusBadRequest, "提交的是掩码值；新建必须提供真实凭据")
		return
	}
	kind := ""
	if req.Kind != nil {
		kind = *req.Kind
	}
	sec := &db.Secret{
		Name:      strings.TrimSpace(*req.Name),
		Kind:      kind,
		Value:     *req.Value,
		Source:    derefString(req.Source),
		Host:      strings.TrimSpace(derefString(req.Host)),
		Notes:     derefString(req.Notes),
		CreatedBy: "user",
	}
	if req.Metadata != nil {
		if b, err := json.Marshal(req.Metadata); err == nil {
			sec.Metadata = b
		}
	}
	id, created, err := pg.CreateSecret(r.Context(), sec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, l := range req.Links {
		if _, err := pg.AddSecretLink(r.Context(), id, l.TargetType, l.TargetRef, "user"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.logSecretAction(r.Context(), pg, &db.SecretAccess{
		SecretID: &id, SecretName: sec.Name, Action: "create",
		Actor: "user", ActorKind: "user", Reason: "API 创建",
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "created": created, "value_mask": secret.Mask(sec.Value)})
}

func (s *Server) secretsGet(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	sec, err := pg.SecretByID(r.Context(), id)
	if err != nil {
		writeSecretErr(w, err)
		return
	}
	links, err := pg.ListSecretLinks(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取关联失败")
		return
	}
	dto := toSecretDTO(sec, len(links))
	dto.Links = links
	s.logSecretAction(r.Context(), pg, &db.SecretAccess{
		SecretID: &id, SecretName: sec.Name, Action: "read",
		Actor: "user", ActorKind: "user", Reason: "查看详情",
	})
	writeJSON(w, http.StatusOK, map[string]any{"secret": dto})
}

func (s *Server) secretsUpdate(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	cur, err := pg.SecretByID(r.Context(), id)
	if err != nil {
		writeSecretErr(w, err)
		return
	}
	var req secretRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	// 局部更新：未提供的字段保持原值。value 只有显式 replace_value 时才替换。
	next := *cur
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		next.Name = strings.TrimSpace(*req.Name)
	}
	if req.Kind != nil && *req.Kind != "" {
		next.Kind = *req.Kind
	}
	if req.Source != nil {
		next.Source = *req.Source
	}
	if req.Host != nil {
		next.Host = strings.TrimSpace(*req.Host)
	}
	if req.Notes != nil {
		next.Notes = *req.Notes
	}
	if req.Metadata != nil {
		if b, err := json.Marshal(req.Metadata); err == nil {
			next.Metadata = b
		}
	}
	replace := req.ReplaceValue && req.Value != nil
	if replace {
		next.Value = *req.Value
	}
	if err := pg.UpdateSecret(r.Context(), id, &next, replace); err != nil {
		if errors.Is(err, db.ErrSecretNotFound) {
			writeSecretErr(w, err)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logSecretAction(r.Context(), pg, &db.SecretAccess{
		SecretID: &id, SecretName: next.Name, Action: "update",
		Actor: "user", ActorKind: "user", Reason: "API 更新",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) secretsDelete(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	sec, err := pg.SecretByID(r.Context(), id)
	if err != nil {
		writeSecretErr(w, err)
		return
	}
	// 先写审计再删除：删除成功后 secret_id 会被置空，靠 secret_name 追溯。
	s.logSecretAction(r.Context(), pg, &db.SecretAccess{
		SecretID: &id, SecretName: sec.Name, Action: "delete",
		Actor: "user", ActorKind: "user", Reason: "API 删除",
	})
	if err := pg.DeleteSecret(r.Context(), id); err != nil {
		writeSecretErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// secretsReveal 返回明文。必须提供非空 reason；每次取用都会写审计并刷新 last_used_at。
func (s *Server) secretsReveal(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErr(w, http.StatusBadRequest, "reason 必填：取用凭据需要说明用途")
		return
	}
	sec, err := pg.SecretByID(r.Context(), id)
	if err != nil {
		writeSecretErr(w, err)
		return
	}
	s.logSecretAction(r.Context(), pg, &db.SecretAccess{
		SecretID: &id, SecretName: sec.Name, Action: "reveal",
		Actor: "user", ActorKind: "user", Reason: reason,
	})
	_ = pg.TouchSecretUsed(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "name": sec.Name, "kind": sec.Kind, "value": sec.Value,
	})
}

func (s *Server) secretsAccess(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	if _, err := pg.SecretByID(r.Context(), id); err != nil {
		writeSecretErr(w, err)
		return
	}
	list, err := pg.ListSecretAccess(r.Context(), id, atoiDefault(r.URL.Query().Get("limit"), 100))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取访问审计失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": list})
}

func (s *Server) secretsAddLink(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	sec, err := pg.SecretByID(r.Context(), id)
	if err != nil {
		writeSecretErr(w, err)
		return
	}
	var req secretLinkRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	created, err := pg.AddSecretLink(r.Context(), id, req.TargetType, req.TargetRef, "user")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if created {
		s.logSecretAction(r.Context(), pg, &db.SecretAccess{
			SecretID: &id, SecretName: sec.Name, Action: "link",
			Actor: "user", ActorKind: "user",
			Reason: fmt.Sprintf("%s:%s", req.TargetType, req.TargetRef),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created})
}

func (s *Server) secretsRemoveLink(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "机密 id 无效")
		return
	}
	linkID, ok := pathInt(r, "linkID")
	if !ok {
		writeErr(w, http.StatusBadRequest, "关联 id 无效")
		return
	}
	sec, err := pg.SecretByID(r.Context(), id)
	if err != nil {
		writeSecretErr(w, err)
		return
	}
	if err := pg.RemoveSecretLink(r.Context(), id, linkID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logSecretAction(r.Context(), pg, &db.SecretAccess{
		SecretID: &id, SecretName: sec.Name, Action: "unlink",
		Actor: "user", ActorKind: "user", Reason: fmt.Sprintf("link:%d", linkID),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// logSecretAction 写访问审计。日志失败不阻断主流程，但记一条不含明文的错误。
func (s *Server) logSecretAction(ctx context.Context, pg *db.DB, a *db.SecretAccess) {
	if err := pg.LogSecretAccess(ctx, a); err != nil {
		fmt.Printf("[secret] 审计写入失败 action=%s err=%v\n", a.Action, err)
	}
}

func writeSecretErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrSecretNotFound) {
		writeErr(w, http.StatusNotFound, "机密不存在")
		return
	}
	writeErr(w, http.StatusBadRequest, err.Error())
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---- Agent 工具 ----

// secretTools 返回三个主机工具。它们通过 hostTools() 注入，再由 tools 表的
// 按 agent 绑定决定谁真正可见（默认绑定见 seedSecretTools）。
func (s *Server) secretTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolSecretStore(),
		s.toolSecretSearch(),
		s.toolSecretReveal(),
	}
}

// secretToolKeys 是机密工具默认绑定的 agent。
var secretToolKeys = []string{"secret_store", "secret_search", "secret_reveal"}

// seedSecretTools 把三个机密工具首插入 tools 表并默认绑定执行 agent。
// SeedTool 只插不改（保护 UI 编辑）；老库的补绑走一次性的 MigrateToolBindings。
func (s *Server) seedSecretTools() {
	if s.m.pg == nil {
		return
	}
	agents, _ := json.Marshal([]string{"mainagent", "planner", "worker", "auto", "pentest"})
	for _, t := range s.secretTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.pg.SeedTool(t.Name(), t.Description(), schema, agents)
	}
	// MigrateToolBindings 用 settings 里的 flag 做一次性门禁，因此每个 agent 用独立
	// flag：共用一个 flag 会让第一次调用之后的全部分支直接短路。
	for _, agentKey := range []string{"mainagent", "planner", "worker", "auto", "pentest"} {
		if err := s.m.pg.MigrateToolBindings("secret_vault_bindings_v1_"+agentKey, agentKey, secretToolKeys, false); err != nil {
			return
		}
	}
}

func (s *Server) toolSecretStore() actool.CoreTool {
	return wrTool("secret_store",
		"把本次任务中发现的凭据（API key、密码、token、Cookie、证书等）写入机密库，供后续任务检索复用。\n"+
			"写入会自动关联当前任务/会话；同类型同值的凭据只保留一条记录并复用原 id。\n"+
			"只存真实凭据，不要把猜测值、示例值或占位符写进来。返回值只含 id 与掩码。",
		objSchema(map[string]any{
			"name":        strParam("简短可辨识的名称，如「example.com 后台管理员」"),
			"kind":        strParam("类型：api_key | password | token | cookie | certificate | private_key | connection_string | other"),
			"value":       strParam("凭据明文，只提交一次，不会回显"),
			"host":        strParam("可选：凭据所属 host/域名"),
			"notes":       strParam("可选：适用范围、获取方式等说明（不要重复写明文）"),
			"source":      strParam("可选：发现来源，如「nuclei 输出」「目录扫描发现的 .env」"),
			"target_type": strParam("可选：附加关联类型 task | asset | finding | company | host | conversation"),
			"target_ref":  strParam("可选：附加关联目标（id 或 host）"),
		}, "name", "kind", "value"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			pg := s.m.pg
			if pg == nil {
				return actool.Errorf("机密库未初始化（未连接 PostgreSQL）"), nil
			}
			var a struct {
				Name, Kind, Value, Host, Notes, Source string
				TargetType, TargetRef                  string
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Value) == "" {
				return actool.Errorf("name 与 value 必填"), nil
			}
			if secret.IsMasked(a.Value) {
				return actool.Errorf("value 是掩码值；写入必须提供真实凭据"), nil
			}
			ri := agent.RunInfoFrom(ctx)
			actor := "agent"
			sec := &db.Secret{
				Name: strings.TrimSpace(a.Name), Kind: a.Kind, Value: a.Value,
				Source: strings.TrimSpace(a.Source), Host: strings.TrimSpace(a.Host),
				Notes: a.Notes, CreatedBy: actor,
			}
			if ri.TaskID > 0 {
				tid := ri.TaskID
				sec.SourceTaskID = &tid
			}
			id, created, err := pg.CreateSecret(ctx, sec)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			links := 0
			if ri.TaskID > 0 {
				if ok, err := pg.AddSecretLink(ctx, id, "task", strconv.FormatInt(ri.TaskID, 10), actor); err == nil && ok {
					links++
				}
			}
			if a.TargetType != "" && a.TargetRef != "" {
				if ok, err := pg.AddSecretLink(ctx, id, a.TargetType, a.TargetRef, actor); err != nil {
					return actool.Errorf("凭据已保存，但附加关联失败: " + err.Error()), nil
				} else if ok {
					links++
				}
			}
			if ri.SessionID != "" {
				if ok, err := pg.AddSecretLink(ctx, id, "conversation", ri.SessionID, actor); err == nil && ok {
					links++
				}
			}
			action := "create"
			if !created {
				action = "update"
			}
			s.logAgentSecretAccess(ctx, pg, id, sec.Name, action, ri, "agent 写入")
			return jsonResult(map[string]any{
				"id": id, "duplicate": !created, "value_mask": secret.Mask(a.Value),
				"kind": sec.Kind, "links_added": links,
			})
		})
}

func (s *Server) toolSecretSearch() actool.CoreTool {
	return roTool("secret_search",
		"检索机密库。只返回元数据与掩码，不返回明文；需要实际使用时再调用 secret_reveal。\n"+
			"支持按关键字、类型、host、任务 id 过滤。跨任务的凭据也可以查到。",
		objSchema(map[string]any{
			"q":       strParam("可选：关键字，匹配名称/说明/host/来源"),
			"kind":    strParam("可选：api_key | password | token | cookie | certificate | private_key | connection_string | other"),
			"host":    strParam("可选：按 host 精确过滤"),
			"task_id": map[string]any{"type": "integer", "description": "可选：只看与某任务关联（或起源于该任务）的凭据"},
			"limit":   map[string]any{"type": "integer", "description": "可选：返回上限，默认 20，最大 100"},
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			pg := s.m.pg
			if pg == nil {
				return actool.Errorf("机密库未初始化（未连接 PostgreSQL）"), nil
			}
			var a struct {
				Q      string `json:"q"`
				Kind   string `json:"kind"`
				Host   string `json:"host"`
				TaskID int64  `json:"task_id"`
				Limit  int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Limit <= 0 || a.Limit > 100 {
				a.Limit = 20
			}
			list, err := pg.ListSecrets(ctx, db.SecretFilter{
				Q: strings.TrimSpace(a.Q), Kind: a.Kind, Host: strings.TrimSpace(a.Host),
				TaskID: a.TaskID, Limit: a.Limit,
			})
			if err != nil {
				return actool.Errorf("检索失败: " + err.Error()), nil
			}
			ids := make([]int64, 0, len(list))
			for _, it := range list {
				ids = append(ids, it.ID)
			}
			counts, _ := pg.CountSecretLinks(ctx, ids)
			out := make([]map[string]any, 0, len(list))
			for _, it := range list {
				out = append(out, map[string]any{
					"id": it.ID, "name": it.Name, "kind": it.Kind,
					"value_mask": secret.Mask(it.Value), "host": it.Host,
					"source": it.Source, "source_task_id": it.SourceTaskID,
					"notes": it.Notes, "link_count": counts[it.ID],
					"updated_at": it.UpdatedAt, "last_used_at": it.LastUsedAt,
				})
			}
			return jsonResult(map[string]any{
				"count": len(out), "secrets": out,
				"hint": "结果不含明文；确需使用某条凭据时调用 secret_reveal 并说明原因。",
			})
		})
}

func (s *Server) toolSecretReveal() actool.CoreTool {
	return wrTool("secret_reveal",
		"取用一条凭据的明文。必须说明用途（reason），每次取用都会写审计。\n"+
			"使用后不要把明文写进漏洞、说明、日志或给用户的总结；只在调用工具的参数里使用。",
		objSchema(map[string]any{
			"id":     map[string]any{"type": "integer", "description": "secret_search / secret_store 返回的 id"},
			"reason": strParam("必填：为什么需要这条凭据，例如「用该账号登录 example.com 后台验证越权」"),
		}, "id", "reason"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			pg := s.m.pg
			if pg == nil {
				return actool.Errorf("机密库未初始化（未连接 PostgreSQL）"), nil
			}
			var a struct {
				ID     int64  `json:"id"`
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			if a.ID == 0 {
				return actool.Errorf("id 必填"), nil
			}
			if strings.TrimSpace(a.Reason) == "" {
				return actool.Errorf("reason 必填：取用凭据需要说明用途"), nil
			}
			sec, err := pg.SecretByID(ctx, a.ID)
			if err != nil {
				if errors.Is(err, db.ErrSecretNotFound) {
					return actool.Errorf(fmt.Sprintf("机密不存在: %d", a.ID)), nil
				}
				return actool.Errorf("读取失败: " + err.Error()), nil
			}
			ri := agent.RunInfoFrom(ctx)
			s.logAgentSecretAccess(ctx, pg, a.ID, sec.Name, "reveal", ri, a.Reason)
			_ = pg.TouchSecretUsed(ctx, a.ID)
			return jsonResult(map[string]any{
				"id": sec.ID, "name": sec.Name, "kind": sec.Kind, "value": sec.Value,
				"warning": "明文仅用于本次调用，不要复述到报告、日志或对话总结中。",
			})
		})
}

func (s *Server) logAgentSecretAccess(ctx context.Context, pg *db.DB, id int64, name, action string, ri agent.RunInfo, reason string) {
	a := &db.SecretAccess{
		SecretID: &id, SecretName: name, Action: action,
		Actor: "agent", ActorKind: "agent", SessionID: ri.SessionID, Reason: reason,
	}
	if ri.TaskID > 0 {
		tid := ri.TaskID
		a.TaskID = &tid
	}
	if err := pg.LogSecretAccess(ctx, a); err != nil {
		fmt.Printf("[secret] 审计写入失败 id=%d action=%s err=%v\n", id, action, err)
	}
}
