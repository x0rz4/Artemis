package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/artex/secret"
)

// Secret 是一行机密记录。Value 是明文，只允许在 internal 路径使用：
// 它带 json:"-"，任何直接序列化 Secret 的 DTO 都不会把它带出去。
// 对外展示必须走 server 层的掩码 DTO（见 secret.Mask）。
type Secret struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	Value        string     `json:"-"`
	ValueHint    string     `json:"value_hint"`
	Fingerprint  string     `json:"-"`
	Source       string     `json:"source"`
	SourceTaskID *int64     `json:"source_task_id,omitempty"`
	Host         string     `json:"host"`
	CompanyID    *int64     `json:"company_id,omitempty"`
	Notes        string     `json:"notes"`
	Metadata     []byte     `json:"-"`
	MetadataJSON string     `json:"metadata,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

// SecretLink 把机密关联到任务/资产/发现/企业/host/会话。
// target_ref 用字符串保存：task/asset/finding/company 存十进制 id，host 存 host 名，
// conversation 存会话 key。刻意不做外键，避免删除业务对象时连带删掉机密本身。
type SecretLink struct {
	ID         int64     `json:"id"`
	SecretID   int64     `json:"secret_id"`
	TargetType string    `json:"target_type"`
	TargetRef  string    `json:"target_ref"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// SecretAccess 是一条访问审计。Reason 由取用方填写，绝不含明文。
type SecretAccess struct {
	ID         int64     `json:"id"`
	SecretID   *int64    `json:"secret_id,omitempty"`
	SecretName string    `json:"secret_name"`
	Action     string    `json:"action"`
	Actor      string    `json:"actor"`
	ActorKind  string    `json:"actor_kind"`
	TaskID     *int64    `json:"task_id,omitempty"`
	SessionID  string    `json:"session_id"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// SecretFilter 控制列表查询。空字段表示不过滤；Q 对名称/说明/host/来源做不区分大小写的包含匹配。
type SecretFilter struct {
	Q       string
	Kind    string
	Host    string
	TaskID  int64
	LinkRef string // 例如 task:<id> 之外的精确关联，一般由 TaskID 覆盖
	Limit   int
}

// 合法的关联类型与审计动作，与 schema CHECK 约束保持一致。
var (
	secretLinkTypes = map[string]bool{
		"task": true, "asset": true, "finding": true,
		"company": true, "host": true, "conversation": true,
	}
	secretActions = map[string]bool{
		"create": true, "read": true, "reveal": true,
		"update": true, "delete": true, "link": true, "unlink": true,
	}
)

// ValidSecretLinkType 校验关联类型。
func ValidSecretLinkType(t string) bool { return secretLinkTypes[t] }

const secretCols = `id, name, kind, value, value_hint, fingerprint, source, source_task_id,
	host, company_id, notes, metadata, created_by, created_at, updated_at, last_used_at`

func scanSecret(sc interface{ Scan(...any) error }) (*Secret, error) {
	var s Secret
	var meta []byte
	err := sc.Scan(&s.ID, &s.Name, &s.Kind, &s.Value, &s.ValueHint, &s.Fingerprint,
		&s.Source, &s.SourceTaskID, &s.Host, &s.CompanyID, &s.Notes, &meta,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.LastUsedAt)
	if err != nil {
		return nil, err
	}
	s.Metadata = meta
	s.MetadataJSON = string(meta)
	return &s, nil
}

// CreateSecret 写入一条机密并返回 id 与是否新建。
//
// 去重键是 (kind, value) 的指纹：同一凭据被不同任务发现时只保留一行，
// 但调用方仍应补一条关联（见 AddSecretLink）。ON CONFLICT 让并发写入也安全：
// 两个请求撞车时都能拿到同一 id，不会出现唯一约束错误。
//
// 已存在的记录只刷新 updated_at，不覆盖名称/说明——首次发现者提供的信息
// 更接近原始上下文，后到的调用方通过关联与 notes 补充。
func (d *DB) CreateSecret(ctx context.Context, s *Secret) (id int64, created bool, err error) {
	if strings.TrimSpace(s.Name) == "" {
		return 0, false, errors.New("机密名称不能为空")
	}
	kind, err := secret.NormalizeKind(s.Kind)
	if err != nil {
		return 0, false, err
	}
	if strings.TrimSpace(s.Value) == "" {
		return 0, false, errors.New("机密值不能为空")
	}
	s.Kind = kind
	s.ValueHint = secret.Hint(s.Value)
	s.Fingerprint = secret.Fingerprint(kind, s.Value)
	if len(s.Metadata) == 0 {
		s.Metadata = []byte(`{}`)
	}
	err = d.QueryRowContext(ctx, `
INSERT INTO secrets(name, kind, value, value_hint, fingerprint, source, source_task_id,
                   host, company_id, notes, metadata, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (fingerprint) DO UPDATE SET updated_at = now()
RETURNING id, (xmax = 0) AS created`,
		s.Name, s.Kind, s.Value, s.ValueHint, s.Fingerprint, s.Source, s.SourceTaskID,
		s.Host, s.CompanyID, s.Notes, string(s.Metadata), s.CreatedBy).Scan(&id, &created)
	if err != nil {
		return 0, false, err
	}
	// source_task_id 只在首次写入时保留；合并来的记录若原本没有来源任务，补上。
	if !created && s.SourceTaskID != nil {
		_, _ = d.ExecContext(ctx, `UPDATE secrets SET source_task_id=$2 WHERE id=$1 AND source_task_id IS NULL`, id, *s.SourceTaskID)
	}
	return id, created, nil
}

// UpdateSecret 更新可变字段。Value 为空表示不替换；非空时必须不是掩码值。
// 返回 ErrSecretNotFound 由调用方映射为 404。
func (d *DB) UpdateSecret(ctx context.Context, id int64, s *Secret, replaceValue bool) error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("机密名称不能为空")
	}
	if s.Kind != "" {
		kind, err := secret.NormalizeKind(s.Kind)
		if err != nil {
			return err
		}
		s.Kind = kind
	}
	if replaceValue {
		if secret.IsMasked(s.Value) {
			return errors.New("提交的是掩码值；替换凭据必须显式提交新值")
		}
		if strings.TrimSpace(s.Value) == "" {
			return errors.New("替换凭据时新值不能为空")
		}
		kind := s.Kind
		if kind == "" {
			cur, err := d.SecretByID(ctx, id)
			if err != nil {
				return err
			}
			kind = cur.Kind
		}
		res, err := d.ExecContext(ctx, `UPDATE secrets SET name=$2, kind=$3, value=$4, value_hint=$5,
	fingerprint=$6, source=$7, host=$8, notes=$9, metadata=$10, updated_at=now() WHERE id=$1`,
			id, s.Name, kind, s.Value, secret.Hint(s.Value), secret.Fingerprint(kind, s.Value),
			s.Source, s.Host, s.Notes, string(orEmptyJSON(s.Metadata)))
		if err != nil {
			return secretConstraintErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrSecretNotFound
		}
		return nil
	}
	if s.Metadata == nil {
		s.Metadata = []byte(`{}`)
	}
	res, err := d.ExecContext(ctx, `UPDATE secrets SET
	name = CASE WHEN $2 <> '' THEN $2 ELSE name END,
	kind = CASE WHEN $3 <> '' THEN $3 ELSE kind END,
	source = $4, host = $5, notes = $6, metadata = $7, updated_at = now()
	WHERE id=$1`,
		id, s.Name, s.Kind, s.Source, s.Host, s.Notes, string(s.Metadata))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSecretNotFound
	}
	return nil
}

// secretConstraintErr 把唯一约束冲突换成可读错误。
// 错误信息只说明「已存在同值记录」，绝不回显值本身。
func secretConstraintErr(err error) error {
	if strings.Contains(err.Error(), "secrets_fingerprint_key") ||
		strings.Contains(err.Error(), "duplicate key") {
		return errors.New("已存在相同类型的同值凭据，请直接复用该记录")
	}
	return err
}

func orEmptyJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte(`{}`)
	}
	return b
}

// ErrSecretNotFound 表示目标机密不存在。
var ErrSecretNotFound = errors.New("机密不存在")

// SecretByID 读取单条机密（含明文）。仅供服务端内部与取用路径使用。
func (d *DB) SecretByID(ctx context.Context, id int64) (*Secret, error) {
	s, err := scanSecret(d.QueryRowContext(ctx, `SELECT `+secretCols+` FROM secrets WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, ErrSecretNotFound
	}
	return s, err
}

// ListSecrets 返回过滤后的机密列表（含明文，调用方必须立刻转换成掩码 DTO）。
// 排序固定为 updated_at DESC, id DESC，让最近发现/使用的记录排在前面。
func (d *DB) ListSecrets(ctx context.Context, f SecretFilter) ([]*Secret, error) {
	var where []string
	var args []any
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		args = append(args, "%"+q+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(name ILIKE $%d OR notes ILIKE $%d OR host ILIKE $%d OR source ILIKE $%d)", n, n, n, n))
	}
	if f.Kind != "" {
		add("kind = $%d", f.Kind)
	}
	if f.Host != "" {
		add("host = $%d", f.Host)
	}
	if f.TaskID > 0 {
		args = append(args, f.TaskID)
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(source_task_id = $%d OR id IN (SELECT secret_id FROM secret_links WHERE target_type='task' AND target_ref = $%d::text))", n, n))
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	q := `SELECT ` + secretCols + ` FROM secrets`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += fmt.Sprintf(` ORDER BY updated_at DESC, id DESC LIMIT $%d`, len(args))
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Secret{}
	for rows.Next() {
		s, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSecret 删除机密；关联随外键级联，访问审计保留（secret_id 置空）。
func (d *DB) DeleteSecret(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM secrets WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSecretNotFound
	}
	return nil
}

// AddSecretLink 幂等新增关联，返回是否新建。
func (d *DB) AddSecretLink(ctx context.Context, secretID int64, targetType, targetRef, createdBy string) (bool, error) {
	if !ValidSecretLinkType(targetType) {
		return false, fmt.Errorf("关联类型无效: %s", targetType)
	}
	targetRef = strings.TrimSpace(targetRef)
	if targetRef == "" {
		return false, errors.New("关联目标不能为空")
	}
	res, err := d.ExecContext(ctx, `
INSERT INTO secret_links(secret_id, target_type, target_ref, created_by)
VALUES ($1,$2,$3,$4)
ON CONFLICT (secret_id, target_type, target_ref) DO NOTHING`,
		secretID, targetType, targetRef, createdBy)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RemoveSecretLink 删除一条关联；返回影响行数。
func (d *DB) RemoveSecretLink(ctx context.Context, secretID, linkID int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM secret_links WHERE id=$1 AND secret_id=$2`, linkID, secretID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("关联不存在")
	}
	return nil
}

// ListSecretLinks 返回一条机密的所有关联。
func (d *DB) ListSecretLinks(ctx context.Context, secretID int64) ([]*SecretLink, error) {
	rows, err := d.QueryContext(ctx, `
SELECT id, secret_id, target_type, target_ref, created_by, created_at
FROM secret_links WHERE secret_id=$1 ORDER BY created_at, id`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SecretLink{}
	for rows.Next() {
		var l SecretLink
		if err := rows.Scan(&l.ID, &l.SecretID, &l.TargetType, &l.TargetRef, &l.CreatedBy, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// CountSecretLinks 按 secret_id 批量统计关联数，供列表页一次查询。
func (d *DB) CountSecretLinks(ctx context.Context, ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := d.QueryContext(ctx, `
SELECT secret_id, count(*) FROM secret_links WHERE secret_id = ANY($1::bigint[]) GROUP BY secret_id`, pgInt64Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// LogSecretAccess 写一条访问审计。reason 不得包含明文；调用方负责口径。
func (d *DB) LogSecretAccess(ctx context.Context, a *SecretAccess) error {
	if !secretActions[a.Action] {
		return fmt.Errorf("审计动作无效: %s", a.Action)
	}
	if a.ActorKind == "" {
		a.ActorKind = "user"
	}
	if a.ActorKind != "user" && a.ActorKind != "agent" {
		return fmt.Errorf("操作者类型无效: %s", a.ActorKind)
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO secret_access_log(secret_id, secret_name, action, actor, actor_kind, task_id, session_id, reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.SecretID, a.SecretName, a.Action, a.Actor, a.ActorKind, a.TaskID, a.SessionID, a.Reason)
	return err
}

// ListSecretAccess 返回一条机密的访问审计，最新在前。
func (d *DB) ListSecretAccess(ctx context.Context, secretID int64, limit int) ([]*SecretAccess, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.QueryContext(ctx, `
SELECT id, secret_id, secret_name, action, actor, actor_kind, task_id, session_id, reason, created_at
FROM secret_access_log WHERE secret_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`, secretID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SecretAccess{}
	for rows.Next() {
		var a SecretAccess
		if err := rows.Scan(&a.ID, &a.SecretID, &a.SecretName, &a.Action, &a.Actor,
			&a.ActorKind, &a.TaskID, &a.SessionID, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// TouchSecretUsed 记录最近使用时间。
func (d *DB) TouchSecretUsed(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `UPDATE secrets SET last_used_at=now() WHERE id=$1`, id)
	return err
}

// pgInt64Array 把 []int64 转成 pgx 能接受的数组参数。
// 用 pgx 的数组字面量而不是构造 SQL 片段，避免拼接注入面。
func pgInt64Array(ids []int64) any {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
