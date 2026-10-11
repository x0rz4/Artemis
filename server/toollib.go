package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// 工具库（data/tools）。
//
// 真源是**磁盘**：data/tools/ 下的文件 + manifest.json。清单由页面导入、上传追加或
// 手工编辑；导入动作把条目 upsert 成 tools 表里 kind=command 的自定义工具行，之后
// 的执行/审批/用量记账全部复用既有链路（server/customtool.go 的 runCommandTool →
// Bash 底层 run）。反向不成立：在「系统 → 工具」里编辑自定义工具**不会**回写清单，
// 否则两边都能当权威，早晚漂移。
//
// 单真源之外的另一条原则是**只告警不阻断**：文件缺失、sha256 不符、runtime 没装都
// 只在自检结果里标出来，不影响启动，也不阻止其它条目导入——武器库不全时项目仍然要能跑。

const (
	toolsLibDirName   = "tools"
	toolsLibManifest  = "manifest.json"
	toolsLibBackupExt = ".bak"
	toolsLibVersion   = "1"
)

const toolsLibComment = "运行时工具清单：data/tools 下的文件与本清单是工具库的真源。" +
	"在「系统 → 工具库」点「导入」把条目注册成 Agent 可调用的工具（kind=command）。" +
	"kind: binary|script|jar|dir；runtime: python3|node|php|java|留空(原生二进制)。" +
	"path 相对 data/tools（目录型工具用 entry 指定入口文件）。" +
	"sources 预留给多源同步（github / http 静态源），当前不参与运行。"

// toolSource 是清单里的一个同步源。P0 只解析与保留，拉取逻辑（github/http）留到 P1。
type toolSource struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // github | http
	Repo   string `json:"repo,omitempty"`
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	URL    string `json:"url,omitempty"`
}

// toolEntry 是清单里的一条工具。字段与 artex-ymh 的 packaging/tools-manifest.json 对齐，
// 并补上本平台需要的注册信息（key/agents/enabled/deferred）。
type toolEntry struct {
	Name     string          `json:"name"`
	Key      string          `json:"key,omitempty"` // 注册成 tools 行的 key；缺省由 name 推导
	Purpose  string          `json:"purpose,omitempty"`
	Version  string          `json:"version,omitempty"`
	Platform string          `json:"platform,omitempty"`
	SHA256   string          `json:"sha256,omitempty"`
	Path     string          `json:"path"`
	Kind     string          `json:"kind,omitempty"`    // binary | script | jar | dir
	Runtime  string          `json:"runtime,omitempty"` // python3 | node | php | java | 空
	Entry    string          `json:"entry,omitempty"`   // kind=dir 时的入口文件（相对 path）
	Category string          `json:"category,omitempty"`
	Source   string          `json:"source,omitempty"` // official | git:<name> | uploaded
	Schema   json.RawMessage `json:"schema,omitempty"`
	Agents   []string        `json:"agents,omitempty"`  // 缺省/空 = 全部 agent
	Enabled  *bool           `json:"enabled,omitempty"` // 缺省 = true
	Deferred bool            `json:"deferred,omitempty"`
}

// toolManifest 是 manifest.json 的整体结构。
type toolManifest struct {
	Comment   string       `json:"_comment,omitempty"`
	Version   string       `json:"version"`
	UpdatedAt string       `json:"updated_at,omitempty"`
	Sources   []toolSource `json:"sources"`
	Tools     []toolEntry  `json:"tools"`
}

func emptyToolManifest() *toolManifest {
	return &toolManifest{
		Comment: toolsLibComment,
		Version: toolsLibVersion,
		Sources: []toolSource{},
		Tools:   []toolEntry{},
	}
}

// toolsDir 是工具库根目录（data/tools）。
func (s *Server) toolsDir() string { return filepath.Join(s.m.dir, toolsLibDirName) }

// toolsManifestPath 是清单文件路径（data/tools/manifest.json）。
func (s *Server) toolsManifestPath() string { return filepath.Join(s.toolsDir(), toolsLibManifest) }

// loadToolManifest 读取清单；文件不存在时创建空骨架。清单写坏时报错而不是默默覆盖
// ——里面可能有手工写进去的条目。
func (s *Server) loadToolManifest() (*toolManifest, error) {
	path := s.toolsManifestPath()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		m := emptyToolManifest()
		if werr := s.saveToolManifest(m); werr != nil {
			// 只读挂载之类的场景：仍然给出骨架，让页面能显示"工具库不可写"。
			log.Printf("[tool-lib] 创建 %s 失败: %v", path, werr)
		}
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取工具清单失败: %w", err)
	}
	var m toolManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if m.Tools == nil {
		m.Tools = []toolEntry{}
	}
	if m.Sources == nil {
		m.Sources = []toolSource{}
	}
	return &m, nil
}

// saveToolManifest 原子写回清单（临时文件 + rename），并把上一版留成 manifest.json.bak
// ——手工改坏了还能自己滚回来。
func (s *Server) saveToolManifest(m *toolManifest) error {
	if m.Version == "" {
		m.Version = toolsLibVersion
	}
	if m.Comment == "" {
		m.Comment = toolsLibComment
	}
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	dir := s.toolsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	path := s.toolsManifestPath()
	if old, rerr := os.ReadFile(path); rerr == nil {
		_ = os.WriteFile(path+toolsLibBackupExt, old, 0o644)
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// ensureToolLib 在启动时建好 data/tools + 空清单，并打一行自检摘要。
// 永不致命：磁盘只读、清单写坏都不该让平台起不来。
func (s *Server) ensureToolLib() {
	if s == nil || s.m == nil {
		return
	}
	if _, err := s.loadToolManifest(); err != nil {
		log.Printf("[tool-lib] 工具库不可用（不影响启动）: %v", err)
		return
	}
	sum := s.checkToolLib()
	log.Printf("[tool-lib] 工具库 %s: %d 个条目（文件缺失 %d，校验不符 %d，runtime 缺失 %d）",
		filepath.ToSlash(s.toolsDir()), sum.Total, sum.Missing, sum.Mismatch, sum.RuntimeMissing)
}

// toolKeyFromName 由条目名推导 tools 表 key（须满足 reToolKey：小写字母开头，只含
// 小写字母/数字/下划线），所以短横线、点、空格一律折成下划线，数字开头补 t_ 前缀。
func toolKeyFromName(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			continue
		}
		sb.WriteByte('_')
	}
	k := strings.Trim(sb.String(), "_")
	for strings.Contains(k, "__") {
		k = strings.ReplaceAll(k, "__", "_")
	}
	if k == "" {
		return ""
	}
	if k[0] < 'a' || k[0] > 'z' {
		k = "t_" + k
	}
	if len(k) > 64 {
		k = strings.TrimRight(k[:64], "_")
	}
	if !reToolKey.MatchString(k) {
		return ""
	}
	return k
}

// entryKey 返回条目注册用的 key（显式 key 优先生效，但同样要过校验）。
func entryKey(e toolEntry) (string, error) {
	if k := strings.TrimSpace(e.Key); k != "" {
		if !reToolKey.MatchString(k) {
			return "", fmt.Errorf("key %q 非法：需小写字母开头，仅含小写字母/数字/下划线", k)
		}
		return k, nil
	}
	k := toolKeyFromName(e.Name)
	if k == "" {
		return "", fmt.Errorf("名称 %q 推导不出合法 key，请在清单里显式写 key", e.Name)
	}
	return k, nil
}

// toolEntryPath 校验并返回条目相对 data/tools 的干净路径。
func toolEntryPath(e toolEntry) (string, error) {
	rel := strings.TrimSpace(e.Path)
	if rel == "" {
		return "", errors.New("path 为空")
	}
	// 复用 skill 的文件路径校验（拒 '..'、反斜杠、百分号、控制字符等）。
	clean, msg := skillRelPath(rel)
	if msg != "" {
		return "", fmt.Errorf("path %q 非法：%s", rel, msg)
	}
	return clean, nil
}

// toolCommand 由条目生成可执行的命令模板（kind=command 的 exec.command）。
//
// 路径写成绝对路径 + 正斜杠：命令的工作目录随调用方变化（任务工作区或项目目录），
// 相对路径会在别处失效；正斜杠在 POSIX shell 和 Windows 上都能用。副作用是**移动
// data 目录后要重新导入**——清单里的 path 始终是相对的，重新导入即修复。
// {args} 是留给模型填的占位符，由 renderTemplate + shellQuote 转义。
func (s *Server) toolCommand(e toolEntry) (string, error) {
	clean, err := toolEntryPath(e)
	if err != nil {
		return "", err
	}
	abs := filepath.ToSlash(filepath.Join(s.toolsDir(), filepath.FromSlash(clean)))
	runtime := strings.ToLower(strings.TrimSpace(e.Runtime))
	entry := strings.TrimSpace(e.Entry)
	if entry != "" {
		eclean, msg := skillRelPath(entry)
		if msg != "" {
			return "", fmt.Errorf("entry %q 非法：%s", entry, msg)
		}
		abs = filepath.ToSlash(filepath.Join(s.toolsDir(), filepath.FromSlash(clean), filepath.FromSlash(eclean)))
	}
	switch runtime {
	case "":
		switch strings.ToLower(strings.TrimSpace(e.Kind)) {
		case "jar":
			return "java -jar " + shellQuote(abs) + " {args}", nil
		case "dir":
			if entry == "" {
				return "", errors.New("kind=dir 必须提供 entry（入口文件）")
			}
		case "script":
			return "", errors.New("kind=script 必须提供 runtime（python3/node/php/java）")
		}
		return shellQuote(abs) + " {args}", nil
	case "python3", "python":
		return "python3 " + shellQuote(abs) + " {args}", nil
	case "node":
		return "node " + shellQuote(abs) + " {args}", nil
	case "php":
		return "php " + shellQuote(abs) + " {args}", nil
	case "java":
		return "java -jar " + shellQuote(abs) + " {args}", nil
	default:
		return "", fmt.Errorf("不支持的 runtime %q（可用：python3/node/php/java，原生二进制留空）", e.Runtime)
	}
}

// toolEntryToRow 把清单条目转成 tools 行。kind 固定 command ——它复用 Bash 底层 run，
// 所以 PHP/Node/JAR/二进制/脚本都能跑，不需要给执行层新增 runtime 分支。
func (s *Server) toolEntryToRow(e toolEntry, allAgents []string) (*db.Tool, error) {
	key, err := entryKey(e)
	if err != nil {
		return nil, err
	}
	cmd, err := s.toolCommand(e)
	if err != nil {
		return nil, err
	}
	exec, err := json.Marshal(commandExec{Command: cmd})
	if err != nil {
		return nil, err
	}
	desc := strings.TrimSpace(e.Purpose)
	if desc == "" {
		desc = "工具库: " + e.Name
	}
	if v := strings.TrimSpace(e.Version); v != "" {
		desc += " (v" + v + ")"
	}
	agents := e.Agents
	if len(agents) == 0 {
		// 没有任何绑定的自定义工具对每个 agent 都不可见（见 assembly.ToolResolve 的
		// contains(row.Agents, agentKey)），而清单里通常不写绑定，所以默认绑全部——
		// 上传即用，之后可在「系统 → 工具」里收窄。
		agents = allAgents
	}
	enabled := true
	if e.Enabled != nil {
		enabled = *e.Enabled
	}
	return &db.Tool{
		Key: key, Description: desc, Schema: e.Schema, Agents: agents,
		Enabled: enabled, Kind: "command", Exec: exec, Deferred: e.Deferred,
	}, nil
}

// overwriteBinding 报告这次导入是否允许覆盖行上已有的 agents/enabled：只在清单显式
// 写了这两项时才覆盖，否则保留用户在页面上的绑定。
func (e toolEntry) overwriteBinding() bool {
	return len(e.Agents) > 0 || e.Enabled != nil
}

// allAgentKeys 返回当前全部 agent key（工具库条目的默认绑定集合）。
func (s *Server) allAgentKeys() []string {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	rows, err := s.m.pg.ListAgents()
	if err != nil {
		log.Printf("[tool-lib] 读取 agent 列表失败: %v", err)
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, a := range rows {
		out = append(out, a.Key)
	}
	sort.Strings(out)
	return out
}

// findToolEntry 按清单条目名或推导出的 key 定位条目（删除接口用）。
func findToolEntry(m *toolManifest, nameOrKey string) (int, *toolEntry) {
	for i := range m.Tools {
		e := &m.Tools[i]
		if e.Name == nameOrKey {
			return i, e
		}
		if k, err := entryKey(*e); err == nil && k == nameOrKey {
			return i, e
		}
	}
	return -1, nil
}

// appendOrReplaceEntry 写入清单条目：同名或同 key 视为同一条（再次导入/重传是刷新，
// 不是追加），否则追加。
func appendOrReplaceEntry(m *toolManifest, e toolEntry) error {
	key, err := entryKey(e)
	if err != nil {
		return err
	}
	for i := range m.Tools {
		old := m.Tools[i]
		if old.Name == e.Name {
			m.Tools[i] = e
			return nil
		}
		if oldKey, kerr := entryKey(old); kerr == nil && oldKey == key {
			m.Tools[i] = e
			return nil
		}
	}
	m.Tools = append(m.Tools, e)
	return nil
}

// ---------- HTTP handlers ----------

// tlList 返回清单 + 每个条目的自检结果 + 已注册的工具行绑定情况。
func (s *Server) tlList(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	rows, _ := pg.ListCustomTools()
	byKey := make(map[string]*db.Tool, len(rows))
	for _, t := range rows {
		byKey[t.Key] = t
	}
	// 自检一次全库，按条目名索引复用——每个条目单独探测会把 python 解释器查询放大 N 倍。
	sum := s.checkToolLib()
	checkByName := make(map[string]toolEntryStatus, len(sum.Items))
	for _, st := range sum.Items {
		checkByName[st.Name] = st
	}
	items := make([]map[string]any, 0, len(m.Tools))
	for _, e := range m.Tools {
		key, _ := entryKey(e)
		item := map[string]any{
			"name": e.Name, "key": key, "path": e.Path, "kind": e.Kind,
			"runtime": e.Runtime, "entry": e.Entry, "purpose": e.Purpose,
			"version": e.Version, "platform": e.Platform, "category": e.Category,
			"source": e.Source, "sha256": e.SHA256,
			"check": checkByName[e.Name],
		}
		if t := byKey[key]; t != nil {
			item["registered"] = true
			item["agents"] = t.Agents
			item["enabled"] = t.Enabled
		} else {
			item["registered"] = false
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{
		"dir":        filepath.ToSlash(s.toolsDir()),
		"manifest":   filepath.ToSlash(s.toolsManifestPath()),
		"version":    m.Version,
		"updated_at": m.UpdatedAt,
		"sources":    m.Sources,
		"tools":      items,
		"summary":    sum,
	})
}

// tlCheck 只重跑自检（页面上的「重新自检」按钮）。
func (s *Server) tlCheck(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	writeJSON(w, 200, s.checkToolLib())
}

// tlImport 把清单条目注册（或刷新）成自定义工具行。body 可选 {"names": [...]} 限定子集。
func (s *Server) tlImport(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	agents := s.allAgentKeys()
	type result struct {
		Name    string `json:"name"`
		Key     string `json:"key,omitempty"`
		Created bool   `json:"created"`
		Error   string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(m.Tools))
	imported := 0
	for _, e := range m.Tools {
		if len(req.Names) > 0 && !contains(req.Names, e.Name) {
			continue
		}
		res := result{Name: e.Name}
		row, rerr := s.toolEntryToRow(e, agents)
		if rerr != nil {
			res.Error = rerr.Error()
			results = append(results, res)
			continue
		}
		res.Key = row.Key
		created, uerr := pg.UpsertLibraryTool(row, e.overwriteBinding())
		if uerr != nil {
			res.Error = uerr.Error()
		} else {
			res.Created = created
			imported++
		}
		results = append(results, res)
	}
	if imported > 0 {
		s.invalidateCommandTemplates()
	}
	writeJSON(w, 200, map[string]any{"imported": imported, "results": results})
}

// tlDelete 从清单里移除一条（可选连文件与注册的工具行一起删）。
// purge=false 只改清单：磁盘文件与已注册的工具行都保留，适合"先撤下清单条目"。
func (s *Server) tlDelete(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	name := r.PathValue("name")
	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	idx, entry := findToolEntry(m, name)
	if entry == nil {
		writeErr(w, 404, "清单中没有此条目: "+name)
		return
	}
	purge := r.URL.Query().Get("purge") == "true"
	key, _ := entryKey(*entry)
	if purge {
		if clean, msg := toolEntryPath(*entry); msg == nil {
			// 只删条目自己的路径，且必须落在 data/tools 内（清单是本地文件，
			// 但仍按不可信输入处理——手工改过的清单不该能删到别的目录）。
			target := filepath.Join(s.toolsDir(), filepath.FromSlash(clean))
			if rel, rerr := filepath.Rel(s.toolsDir(), target); rerr == nil &&
				rel != "." && !strings.HasPrefix(rel, "..") {
				if rmErr := os.RemoveAll(target); rmErr != nil {
					writeErr(w, 500, "删除文件失败: "+rmErr.Error())
					return
				}
			}
		}
		if key != "" {
			if derr := pg.DeleteCustomTool(key); derr != nil {
				writeErr(w, 500, derr.Error())
				return
			}
		}
	}
	m.Tools = append(m.Tools[:idx], m.Tools[idx+1:]...)
	if err := s.saveToolManifest(m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if purge {
		s.invalidateCommandTemplates()
	}
	writeJSON(w, 200, map[string]any{"deleted": name, "key": key, "purged": purge})
}
