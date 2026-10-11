package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/norma/skill"
)

// 技能库：多扫描根 + 多源同步。
//
// 扫描根按优先级：
//
//  1. s.skillDir          —— 主根（config.SkillDir()：发布包/镜像里的 skills/，或 ARTEX_SKILL_DIR）
//  2. <data>/skills/uploaded —— 页面上传与 github/http 源同步进来的 skill
//  3. <data>/skills/git/<name> —— git 型源克隆下来的整仓，每个仓库一个根
//
// 同名 skill 以靠前的根为准：内置的不会被用户上传顶掉。写入（改文件、删文件、改元数据）
// 落在该 skill 当前所在的根；新建与上传默认落 uploaded 根，这样程序目录里的内置技能集
// 不会被用户内容污染，重装/更新程序也不会带走用户技能。

const (
	skillsLibDirName    = "skills"
	skillsUploadDirName = "uploaded"
	skillsGitDirName    = "git"
	skillsLibManifest   = "manifest.json"
	skillSourceTokenPre = "skill"
)

const skillLibComment = "技能库清单：sources 是同步源（git 整仓 / github·http 的 zip 清单），skills 是同步记录。" +
	"扫描根依次为程序旁的 skills/、data/skills/uploaded、data/skills/git/<源名>，同名以靠前的为准。"

// skillSourceEntry 是 github/http 型源清单里的一条 skill（一个 zip 资产）。
type skillSourceEntry struct {
	Name        string `json:"name"`
	Zip         string `json:"zip"`
	URL         string `json:"url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Description string `json:"description,omitempty"`
}

// skillSourceManifest 是 github/http 型源上那份清单文件的结构。
type skillSourceManifest struct {
	Skills []skillSourceEntry `json:"skills"`
}

// skillSyncRecord 记录一个由源同步进来的 skill（仅用于展示与清理）。
type skillSyncRecord struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Dir      string `json:"dir,omitempty"`
	Revision string `json:"revision,omitempty"`
	SyncedAt string `json:"synced_at,omitempty"`
}

type skillManifest struct {
	Comment   string             `json:"_comment,omitempty"`
	Version   string             `json:"version"`
	UpdatedAt string             `json:"updated_at,omitempty"`
	Sources   []libSource        `json:"sources"`
	Skills    []skillSyncRecord  `json:"skills"`
}

func emptySkillManifest() *skillManifest {
	return &skillManifest{Comment: skillLibComment, Version: "1", Sources: []libSource{}, Skills: []skillSyncRecord{}}
}

// ---------- 扫描根 ----------

// skillsLibDir 是 <data>/skills（没有 data 目录时返回 ""：单元测试里的裸 Server 只有主根）。
func (s *Server) skillsLibDir() string {
	if s.m == nil || s.m.dir == "" {
		return ""
	}
	return filepath.Join(s.m.dir, skillsLibDirName)
}

// gitSkillsRoot 是 git 型源的克隆根（<data>/skills/git）。
func (s *Server) gitSkillsRoot() string {
	base := s.skillsLibDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, skillsGitDirName)
}

// skillRoots 返回全部扫描根（按优先级）。
func (s *Server) skillRoots() []string {
	roots := []string{}
	if s.skillDir != "" {
		roots = append(roots, s.skillDir)
	}
	if base := s.skillsLibDir(); base != "" {
		roots = append(roots, filepath.Join(base, skillsUploadDirName))
		if entries, err := os.ReadDir(filepath.Join(base, skillsGitDirName)); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					roots = append(roots, filepath.Join(base, skillsGitDirName, e.Name()))
				}
			}
		}
	}
	return roots
}

// skillWriteRoot 是新建/上传/同步的落点。
func (s *Server) skillWriteRoot() string {
	if base := s.skillsLibDir(); base != "" {
		return filepath.Join(base, skillsUploadDirName)
	}
	return s.skillDir
}

// skillOwnerRoot 返回当前拥有该 skill 的根（"" = 不存在）。
func (s *Server) skillOwnerRoot(name string) string {
	if !validSkillName(name) {
		return ""
	}
	for _, root := range s.skillRoots() {
		if st, err := os.Stat(filepath.Join(root, name)); err == nil && st.IsDir() {
			return root
		}
	}
	return ""
}

// skillPath 返回读写该 skill 用的绝对路径：已存在 → 它所在的根；不存在 → 写入根。
func (s *Server) skillPath(name string) string {
	if root := s.skillOwnerRoot(name); root != "" {
		return filepath.Join(root, name)
	}
	return filepath.Join(s.skillWriteRoot(), name)
}

// loadAllSkills 合并扫描全部根，同名以靠前的根为准。
func loadAllSkills(roots []string) map[string]skill.Skill {
	out := map[string]skill.Skill{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		reg, err := skill.LoadDir(root)
		if err != nil || reg == nil {
			continue
		}
		for _, sk := range reg.List() {
			if sk.Dir == "" {
				continue
			}
			name := filepath.Base(sk.Dir)
			if _, exists := out[name]; exists {
				continue // 靠前的根优先
			}
			out[name] = sk
		}
	}
	return out
}

// loadSkillRegistry 合并全部根，按 agent 可见的目录名过滤，返回可用的注册表（nil = 无）。
func loadSkillRegistry(roots []string, nameSet map[string]bool) *skill.Registry {
	all := loadAllSkills(roots)
	reg := skill.NewRegistry()
	for name, sk := range all {
		if nameSet[name] {
			reg.Add(sk)
		}
	}
	if len(reg.List()) == 0 {
		return nil
	}
	return reg
}

// ---------- 清单读写 ----------

func (s *Server) skillManifestPath() string {
	base := s.skillsLibDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, skillsLibManifest)
}

// loadSkillManifest 读取技能库清单；不存在时创建空骨架（没有 data 目录时给内存骨架）。
func (s *Server) loadSkillManifest() (*skillManifest, error) {
	p := s.skillManifestPath()
	if p == "" {
		return emptySkillManifest(), nil
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		m := emptySkillManifest()
		if werr := s.saveSkillManifest(m); werr != nil {
			return m, nil
		}
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取技能库清单失败: %w", err)
	}
	var m skillManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", p, err)
	}
	if m.Sources == nil {
		m.Sources = []libSource{}
	}
	if m.Skills == nil {
		m.Skills = []skillSyncRecord{}
	}
	return &m, nil
}

// saveSkillManifest 原子写回（临时文件 + rename），上一版留成 manifest.json.bak。
func (s *Server) saveSkillManifest(m *skillManifest) error {
	p := s.skillManifestPath()
	if p == "" {
		return errors.New("未配置数据目录")
	}
	if m.Version == "" {
		m.Version = "1"
	}
	if m.Comment == "" {
		m.Comment = skillLibComment
	}
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if old, rerr := os.ReadFile(p); rerr == nil {
		_ = os.WriteFile(p+".bak", old, 0o644)
	}
	tmp, err := os.CreateTemp(dir, ".skills-manifest-*")
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
	if err := os.Rename(tmpName, p); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// ensureSkillLib 启动时建好 data/skills/uploaded 与清单骨架，并打一行扫描根摘要。
// 永不致命：目录只读、清单写坏都不该让平台起不来。
func (s *Server) ensureSkillLib() {
	if s == nil || s.m == nil {
		return
	}
	if _, err := s.loadSkillManifest(); err != nil {
		log.Printf("[skill-lib] 技能库不可用（不影响启动）: %v", err)
		return
	}
	if base := s.skillsLibDir(); base != "" {
		if err := os.MkdirAll(filepath.Join(base, skillsUploadDirName), 0o755); err != nil {
			log.Printf("[skill-lib] 创建上传目录失败（不影响启动）: %v", err)
			return
		}
	}
	roots := s.skillRoots()
	names := loadAllSkills(roots)
	log.Printf("[skill-lib] 扫描根 %d 个，共 %d 个 skill", len(roots), len(names))
}

// ---------- 同步 ----------

type skillSyncItem struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Action string `json:"action"` // add | update | keep | failed
	From   string `json:"from_revision,omitempty"`
	To     string `json:"to_revision,omitempty"`
	Error  string `json:"error,omitempty"`
}

type skillSyncResult struct {
	DryRun  bool            `json:"dry_run"`
	Sources []string        `json:"sources"`
	Items   []skillSyncItem `json:"items"`
	Changed int             `json:"changed"`
	Failed  int             `json:"failed"`
}

// skSources 列出技能库的源。
func (s *Server) skSources(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	m, err := s.loadSkillManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(m.Sources))
	for _, src := range m.Sources {
		item := map[string]any{
			"name": src.Name, "type": src.typeOrDefault(),
			"repo": src.Repo, "branch": src.branchOrDefault(), "path": src.Path, "url": src.URL,
			"allow_unpinned": src.AllowUnpinned,
			"last_sync_at":   src.LastSyncAt, "last_error": src.LastError,
			"has_token": s.sourceToken(skillSourceTokenPre, src.Name) != "",
		}
		if src.typeOrDefault() == "git" {
			item["target_dir"] = filepath.ToSlash(filepath.Join(s.gitSkillsRoot(), src.Name))
		} else if u, uerr := sourceManifestURL(src); uerr == nil {
			item["manifest_url"] = u
		} else {
			item["config_error"] = uerr.Error()
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{
		"roots":   s.skillRoots(),
		"sources": out,
		"skills":  m.Skills,
	})
}

// skSaveSource 新增或修改一个技能库源（令牌单独处理）。
func (s *Server) skSaveSource(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	var req struct {
		Source     libSource `json:"source"`
		Token      string    `json:"token"`
		ClearToken bool      `json:"clear_token"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "无效的请求体")
		return
	}
	src := req.Source
	src.Name = strings.ToLower(strings.TrimSpace(src.Name))
	if !reSourceName.MatchString(src.Name) {
		writeErr(w, 400, "源名需为小写字母或数字开头，仅含小写字母/数字/短横线/下划线（≤64）")
		return
	}
	switch src.typeOrDefault() {
	case "git":
		if strings.TrimSpace(src.URL) == "" {
			writeErr(w, 400, "git 源缺少 url")
			return
		}
	case "github", "http":
		if _, _, err := s.sourceClient(src); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if _, err := sourceManifestURL(src); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	default:
		writeErr(w, 400, fmt.Sprintf("不支持的源类型 %q（可用：git / github / http）", src.Type))
		return
	}

	m, err := s.loadSkillManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	m.Sources = upsertLibSource(m.Sources, src)
	if err := s.saveSkillManifest(m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	switch {
	case req.ClearToken:
		if err := s.setSourceToken(skillSourceTokenPre, src.Name, ""); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	case strings.TrimSpace(req.Token) != "":
		if err := s.setSourceToken(skillSourceTokenPre, src.Name, req.Token); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "has_token": s.sourceToken(skillSourceTokenPre, src.Name) != ""})
}

// skDeleteSource 移除源；?purge=true 时连同 git 克隆目录与它同步进来的 skill 一起删。
func (s *Server) skDeleteSource(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	name := r.PathValue("name")
	m, err := s.loadSkillManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var src *libSource
	kept := m.Sources[:0:0]
	for i := range m.Sources {
		if m.Sources[i].Name == name {
			src = &m.Sources[i]
			continue
		}
		kept = append(kept, m.Sources[i])
	}
	if src == nil {
		writeErr(w, 404, "没有这个源: "+name)
		return
	}
	m.Sources = kept
	purge := r.URL.Query().Get("purge") == "true"
	if purge {
		if root := s.gitSkillsRoot(); root != "" {
			_ = os.RemoveAll(filepath.Join(root, name))
		}
		rest := m.Skills[:0:0]
		for _, rec := range m.Skills {
			if rec.Source != name {
				rest = append(rest, rec)
				continue
			}
			// 只删落在写入根的（zip 同步进来的）；git 型的目录随上面的仓库目录一起删。
			if s.skillOwnerRoot(rec.Name) == s.skillWriteRoot() {
				_ = os.RemoveAll(filepath.Join(s.skillWriteRoot(), rec.Name))
			}
		}
		m.Skills = rest
	}
	if err := s.saveSkillManifest(m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := s.setSourceToken(skillSourceTokenPre, name, ""); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": name, "purged": purge})
}

// skSync 同步技能库。dry_run=true 只回计划。
func (s *Server) skSync(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	var req struct {
		Names  []string `json:"names"`
		DryRun bool     `json:"dry_run"`
		Force  bool     `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	m, err := s.loadSkillManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(m.Sources) == 0 {
		writeErr(w, 400, "尚未配置任何源")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), syncOverallTimeout)
	defer cancel()

	res := skillSyncResult{DryRun: req.DryRun, Items: []skillSyncItem{}}
	for i := range m.Sources {
		src := m.Sources[i]
		if len(req.Names) > 0 && !contains(req.Names, src.Name) {
			continue
		}
		res.Sources = append(res.Sources, src.Name)
		items, serr := s.syncSkillSource(ctx, src, m, req.DryRun, req.Force)
		res.Items = append(res.Items, items...)
		for _, it := range items {
			switch it.Action {
			case "add", "update":
				res.Changed++
			case "failed":
				res.Failed++
			}
		}
		if serr != nil {
			putLibSourceState(m.Sources, src.Name, "", serr.Error())
			res.Failed++
			res.Items = append(res.Items, skillSyncItem{Source: src.Name, Name: "-", Action: "failed", Error: serr.Error()})
		} else if !req.DryRun {
			putLibSourceState(m.Sources, src.Name, time.Now().UTC().Format(time.RFC3339), "")
		}
	}
	if !req.DryRun {
		if err := s.saveSkillManifest(m); err != nil {
			writeErr(w, 500, "同步已完成但清单写入失败："+err.Error())
			return
		}
	}
	writeJSON(w, 200, res)
}

// syncSkillSource 同步单个技能源：git 拉整仓，github/http 拉 zip 清单逐个装。
func (s *Server) syncSkillSource(ctx context.Context, src libSource, m *skillManifest, dryRun, force bool) ([]skillSyncItem, error) {
	switch src.typeOrDefault() {
	case "git":
		return s.syncSkillGit(ctx, src, m, dryRun, force)
	case "github", "http":
		return s.syncSkillZips(ctx, src, m, dryRun, force)
	}
	return nil, fmt.Errorf("不支持的源类型 %q", src.Type)
}

// syncSkillGit 克隆或更新一个 git 仓库到 data/skills/git/<源名>。
//
// 目录是远程的镜像：本地改动会被覆盖（fetch + reset --hard），这样"同步"的结果可预期。
// 首次克隆先落到临时目录再换入，半截的仓库不会顶掉原来可用的那份。
func (s *Server) syncSkillGit(ctx context.Context, src libSource, m *skillManifest, dryRun, force bool) ([]skillSyncItem, error) {
	root := s.gitSkillsRoot()
	if root == "" {
		return nil, errors.New("未配置数据目录")
	}
	target := filepath.Join(root, src.Name)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("未找到 git，git 型源需要本机安装 git")
	}
	item := skillSyncItem{Source: src.Name, Name: src.Name}
	from := gitHead(ctx, gitBin, target)
	item.From = from

	if _, statErr := os.Stat(target); statErr != nil {
		item.Action = "add"
		if dryRun {
			return []skillSyncItem{item}, nil
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		stage, terr := os.MkdirTemp(root, "."+src.Name+".clone-*")
		if terr != nil {
			return nil, terr
		}
		defer os.RemoveAll(stage)
		args := []string{"clone", "--depth", "1"}
		if b := strings.TrimSpace(src.Branch); b != "" {
			args = append(args, "--branch", b)
		}
		args = append(args, strings.TrimSpace(src.URL), stage)
		if out, cerr := runGit(ctx, gitBin, "", args...); cerr != nil {
			return nil, fmt.Errorf("克隆失败：%v（%s）", cerr, firstLine(out))
		}
		if err := swapDir(stage, target, true); err != nil {
			return nil, err
		}
		item.To = gitHead(ctx, gitBin, target)
		m.Skills = appendOrReplaceSkillRecord(m.Skills, skillSyncRecord{
			Name: src.Name, Source: src.Name, Dir: filepath.ToSlash(filepath.Join(skillsGitDirName, src.Name)),
			Revision: item.To, SyncedAt: time.Now().UTC().Format(time.RFC3339),
		})
		return []skillSyncItem{item}, nil
	}

	// 已存在：fetch 后比对 revision，只有变化（或 force）才 reset。
	branch := src.branchOrDefault()
	if _, ferr := runGit(ctx, gitBin, target, "fetch", "--depth", "1", "origin", branch); ferr != nil {
		return nil, fmt.Errorf("fetch 失败：%v", ferr)
	}
	remote := gitRevParse(ctx, gitBin, target, "FETCH_HEAD")
	item.To = remote
	if remote != "" && remote == from && !force {
		item.Action = "keep"
		return []skillSyncItem{item}, nil
	}
	item.Action = "update"
	if dryRun {
		return []skillSyncItem{item}, nil
	}
	if out, rerr := runGit(ctx, gitBin, target, "reset", "--hard", "FETCH_HEAD"); rerr != nil {
		return []skillSyncItem{item}, fmt.Errorf("reset 失败：%v（%s）", rerr, firstLine(out))
	}
	m.Skills = appendOrReplaceSkillRecord(m.Skills, skillSyncRecord{
		Name: src.Name, Source: src.Name, Dir: filepath.ToSlash(filepath.Join(skillsGitDirName, src.Name)),
		Revision: remote, SyncedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return []skillSyncItem{item}, nil
}

// syncSkillZips 拉取 github/http 源上的 zip 清单并逐个安装（校验 sha256，覆盖走 swapDir）。
func (s *Server) syncSkillZips(ctx context.Context, src libSource, m *skillManifest, dryRun, force bool) ([]skillSyncItem, error) {
	client, hosts, err := s.sourceClient(src)
	if err != nil {
		return nil, err
	}
	manifestURL, err := sourceManifestURL(src)
	if err != nil {
		return nil, err
	}
	token := s.sourceToken(skillSourceTokenPre, src.Name)
	raw, err := fetchSourceBytes(ctx, client, hosts, manifestURL, maxSourceManifestBytes, token)
	if err != nil {
		return nil, fmt.Errorf("拉取清单失败：%w", err)
	}
	var remote skillSourceManifest
	if err := json.Unmarshal(raw, &remote); err != nil {
		return nil, fmt.Errorf("解析源清单失败：%w", err)
	}
	if len(remote.Skills) == 0 {
		return nil, errors.New("源清单里没有 skills 条目")
	}

	items := make([]skillSyncItem, 0, len(remote.Skills))
	for _, se := range remote.Skills {
		item := skillSyncItem{Source: src.Name, Name: se.Name}
		if !validSkillName(se.Name) {
			item.Action = "failed"
			item.Error = "skill 名不合法：" + se.Name
			items = append(items, item)
			continue
		}
		seURL, uerr := sourceAssetURL(src, manifestURL, se.URL, se.Zip)
		if uerr != nil {
			item.Action = "failed"
			item.Error = uerr.Error()
			items = append(items, item)
			continue
		}
		want := strings.ToLower(strings.TrimSpace(se.SHA256))
		if want == "" && !src.AllowUnpinned {
			item.Action = "failed"
			item.Error = "条目未提供 sha256（如确认信任该源，可在源上开启 allow_unpinned）"
			items = append(items, item)
			continue
		}
		installed := skillRecordOf(m.Skills, se.Name)
		// 只有钉了摘要才谈得上"已是源上的版本"；未钉摘要的源每次同步都会重装（updated_at 变，内容未必变）。
		if installed != nil && want != "" && strings.EqualFold(strings.TrimSpace(installed.Revision), want) && !force {
			item.Action = "keep"
			items = append(items, item)
			continue
		}
		if installed == nil {
			item.Action = "add"
		} else {
			item.Action = "update"
		}
		if dryRun {
			items = append(items, item)
			continue
		}
		b, ferr := fetchSourceBytes(ctx, client, hosts, seURL, maxSkillZipBytes, token)
		if ferr != nil {
			item.Action = "failed"
			item.Error = ferr.Error()
			items = append(items, item)
			continue
		}
		sum := hashSourceBytes(b)
		if want != "" && sum != want {
			item.Action = "failed"
			item.Error = fmt.Sprintf("sha256 不符（清单 %s，实际 %s）", want, sum)
			items = append(items, item)
			continue
		}
		name, _, code, ierr := s.installSkillZipBytes(b, se.Name, true)
		if ierr != nil {
			item.Action = "failed"
			item.Error = fmt.Sprintf("%d: %s", code, ierr.Error())
			items = append(items, item)
			continue
		}
		item.Name = name
		item.To = sum
		m.Skills = appendOrReplaceSkillRecord(m.Skills, skillSyncRecord{
			Name: name, Source: src.Name, Revision: sum, SyncedAt: time.Now().UTC().Format(time.RFC3339),
		})
		items = append(items, item)
	}
	return items, nil
}

func appendOrReplaceSkillRecord(list []skillSyncRecord, rec skillSyncRecord) []skillSyncRecord {
	for i := range list {
		if list[i].Name == rec.Name {
			list[i] = rec
			return list
		}
	}
	return append(list, rec)
}

func skillRecordOf(list []skillSyncRecord, name string) *skillSyncRecord {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// runGit 执行一个 git 子命令（dir 为空 = 在任意位置执行）。输出一并返回便于报错。
func runGit(ctx context.Context, gitBin, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, gitBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// gitHead 返回仓库当前 revision（"" = 目录不存在或不是仓库）。
func gitHead(ctx context.Context, gitBin, dir string) string {
	return gitRevParse(ctx, gitBin, dir, "HEAD")
}

func gitRevParse(ctx context.Context, gitBin, dir, rev string) string {
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	out, err := runGit(ctx, gitBin, dir, "rev-parse", rev)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// firstLine 取输出首行做错误提示（git 的报错常带多行进度信息）。
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}
