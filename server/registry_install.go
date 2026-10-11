package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/skill"
)

// Hash the sorted relative paths and file bytes, independent of ZIP timestamps
// and executable permission bits. Symlinks and special files are never accepted.
func registryTreeHash(root string) (string, error) {
	lines := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("内容包含链接或特殊文件")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum, err := toolFileSHA256(path)
		if err != nil {
			return err
		}
		lines = append(lines, filepath.ToSlash(rel)+"\x00"+sum)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", errors.New("内容目录为空")
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, line := range lines {
		parts := strings.SplitN(line, "\x00", 2)
		fmt.Fprintf(h, "%s  %s\n", parts[1], parts[0])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type librarySyncRequest struct {
	Names   []string `json:"names"` // source names, preserved for existing clients
	Items   []string `json:"items"`
	Release string   `json:"release"`
	Replace []string `json:"replace"` // explicit conflict replacement per item
	DryRun  bool     `json:"dry_run"`
	Force   bool     `json:"force"`
}

// Existing admission barriers cover every task operation and chat entry point.
// Hold them only while switching files/metadata, never during network downloads.
func (s *Server) lockLibraryIdle() (func(), error) {
	if !s.concMu.TryLock() {
		return nil, errors.New("任务状态正在变化，请稍后重试")
	}
	if !s.chatMu.TryLock() {
		s.concMu.Unlock()
		return nil, errors.New("聊天状态正在变化，请稍后重试")
	}
	if s.engine != nil && !s.engine.deleteMu.TryLock() {
		s.chatMu.Unlock()
		s.concMu.Unlock()
		return nil, errors.New("任务正在运行，请稍后重试")
	}
	unlock := func() {
		if s.engine != nil {
			s.engine.deleteMu.Unlock()
		}
		s.chatMu.Unlock()
		s.concMu.Unlock()
	}
	busy := false
	for _, v := range s.chatBusy {
		busy = busy || v
	}
	if s.engine != nil {
		s.engine.inflight.Range(func(_, v any) bool {
			if atomic.LoadInt64(v.(*int64)) > 0 {
				busy = true
			}
			return !busy
		})
	}
	if s.m != nil {
		for _, t := range s.m.List() {
			state := t.lifecycleSnapshot()
			if !state.Paused && !state.Queued && !isTerminalStatus(state.Status) {
				busy = true
			}
		}
	}
	if busy {
		unlock()
		return nil, errors.New("任务或聊天正在运行，请空闲后重试")
	}
	return unlock, nil
}

type registryStaged struct {
	item       registryItem
	asset      registryAsset
	stage, dir string
	meta       *registryInstall
}

func (s *Server) stageRegistry(ctx context.Context, kind string, item registryItem, a registryAsset, release string) (registryStaged, error) {
	result := registryStaged{item: item, asset: a}
	base := s.toolsDir()
	if kind == "skills" {
		base = s.skillsLibDir()
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return result, err
	}
	b, err := s.registryFetch(ctx, a.URL, maxSourceAssetBytes)
	if err != nil {
		return result, err
	}
	if hashSourceBytes(b) != a.SHA256 {
		return result, errors.New("安装包 SHA-256 不符")
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, "/")
		if _, msg := skillRelPath(name); msg != "" {
			return result, errors.New("安装包含非法路径")
		}
		key := strings.ToLower(name)
		if seen[key] || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) {
			return result, errors.New("安装包含重复路径、链接或特殊文件")
		}
		seen[key] = true
	}
	stage, err := os.MkdirTemp(base, ".registry-stage-*")
	if err != nil {
		return result, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()
	if _, err = extractToolZip(zr, stage); err != nil {
		return result, err
	}
	got, err := registryTreeHash(stage)
	if err != nil {
		return result, err
	}
	if got != a.ContentSHA256 {
		return result, errors.New("解包内容 SHA-256 不符")
	}
	entry := filepath.Join(stage, filepath.FromSlash(a.Entry))
	if st, err := os.Stat(entry); err != nil || !st.Mode().IsRegular() {
		return result, errors.New("安装包缺少入口文件")
	}
	if kind == "tools" {
		if err = registryCheckELF(entry, a.Platform); err != nil {
			return result, err
		}
		if err = os.Chmod(entry, 0o755); err != nil {
			return result, err
		}
	} else {
		// Validate the actual Skill parser before activation; no arbitrary scripts run.
		validationRoot, err := os.MkdirTemp(base, ".registry-validate-*")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(validationRoot)
		if err = os.Rename(stage, filepath.Join(validationRoot, item.Name)); err != nil {
			return result, err
		}
		reg, loadErr := skill.LoadDir(validationRoot)
		if err = os.Rename(filepath.Join(validationRoot, item.Name), stage); err != nil {
			return result, err
		}
		if loadErr != nil || reg == nil || len(reg.List()) != 1 {
			return result, errors.New("SKILL.md 无法加载")
		}
	}
	result.stage = stage
	// Unique generation also preserves edited versions when explicitly replacing.
	result.dir = filepath.ToSlash(filepath.Join("managed", item.Name, filepath.Base(stage)))
	if kind == "skills" {
		result.dir += "/" + item.Name
	}
	result.meta = &registryInstall{Release: release, Version: item.Version, PackageSHA256: a.SHA256, ContentSHA256: got}
	ok = true
	return result, nil
}

func registryCheckELF(path, platform string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var header [20]byte
	if n, err := f.Read(header[:]); err != nil || n != 20 {
		return errors.New("工具 ELF 文件头不完整")
	}
	machine := byte(62)
	if platform == "linux/arm64" {
		machine = 183
	} else if platform != "linux/amd64" {
		return errors.New("不支持的工具平台")
	}
	if string(header[:6]) != "\x7fELF\x02\x01" || header[18] != machine || header[19] != 0 {
		return errors.New("工具架构与清单不符")
	}
	return nil
}

// Called with the corresponding library write lock held by the existing sync API.
func (s *Server) syncRegistry(w http.ResponseWriter, r *http.Request, kind string, req librarySyncRequest) {
	if len(req.Names) != 1 || req.Names[0] != registrySource {
		writeErr(w, 400, "官方更新须单独选择 artemis-registry 源")
		return
	}
	catalog, err := s.registryPinned(req.Release, kind)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	items, err := s.registryItems(kind, catalog)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if req.DryRun {
		if len(req.Items) > 0 {
			filtered := []registryUpdateItem{}
			for _, item := range items {
				if contains(req.Items, item.Name) {
					filtered = append(filtered, item)
				}
			}
			if len(filtered) != len(req.Items) {
				writeErr(w, 400, "选择包含重复或不存在的条目")
				return
			}
			items = filtered
		}
		writeJSON(w, 200, map[string]any{"release": req.Release, "items": items, "dry_run": true})
		return
	}
	if len(req.Items) == 0 {
		writeErr(w, 400, "请选择要更新的条目")
		return
	}
	selected := map[string]bool{}
	for _, name := range req.Items {
		if selected[name] {
			writeErr(w, 400, "条目重复")
			return
		}
		selected[name] = true
	}
	for _, name := range req.Replace {
		if !selected[name] {
			writeErr(w, 400, "替换条目必须包含在选择中")
			return
		}
	}
	for name := range selected {
		found := false
		for _, item := range items {
			if item.Name == name {
				found = true
				break
			}
		}
		if !found {
			writeErr(w, 400, "选择包含清单中不存在的条目")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), syncOverallTimeout)
	defer cancel()
	staged := []registryStaged{}
	defer func() {
		for _, p := range staged {
			_ = os.RemoveAll(p.stage)
		}
	}()
	skipped := []string{}
	for _, item := range items {
		if !selected[item.Name] {
			continue
		}
		delete(selected, item.Name)
		if item.Status == "unsupported" {
			writeErr(w, 400, item.Name+" 不支持当前平台")
			return
		}
		if item.Status == "keep" {
			continue
		}
		if item.Status == "conflict" && !contains(req.Replace, item.Name) {
			skipped = append(skipped, item.Name)
			continue
		}
		a := registryPlatformAsset(item.registryItem)
		p, err := s.stageRegistry(ctx, kind, item.registryItem, *a, req.Release)
		if err != nil {
			writeErr(w, 502, item.Name+": "+err.Error())
			return
		}
		staged = append(staged, p)
	}
	if len(selected) > 0 {
		writeErr(w, 400, "选择包含清单中不存在的条目")
		return
	}
	if len(staged) == 0 {
		writeJSON(w, 200, map[string]any{"changed": 0, "skipped": skipped})
		return
	}
	unlock, err := s.lockLibraryIdle()
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer unlock()
	// Recheck after download so a local edit during transfer cannot be overwritten.
	now, err := s.registryItems(kind, catalog)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, item := range now {
		for _, p := range staged {
			if item.Name == p.item.Name && item.Status == "conflict" && !contains(req.Replace, item.Name) {
				writeErr(w, 409, item.Name+" 在下载期间发生本地修改，请重新检查")
				return
			}
		}
	}
	snapshot, err := s.commitRegistry(kind, staged, req.Release)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"changed": len(staged), "skipped": skipped, "snapshot": snapshot, "release": req.Release})
}

func (s *Server) commitRegistry(kind string, staged []registryStaged, release string) (string, error) {
	base := s.toolsDir()
	if kind == "skills" {
		base = s.skillsLibDir()
	}
	tm, err := s.loadToolManifest()
	if err != nil {
		return "", err
	}
	sm, err := s.loadSkillManifest()
	if err != nil {
		return "", err
	}
	rels := []string{}
	for _, p := range staged {
		if kind == "tools" {
			if _, local := findToolEntry(tm, p.item.Name); local != nil {
				rels = append(rels, local.Path)
			}
		} else {
			if rec := skillRecordOf(sm.Skills, p.item.Name); rec != nil && rec.Dir != "" {
				rels = append(rels, rec.Dir)
			}
		}
	}
	id, err := takeSnapshot(base, "registry:"+release, rels)
	if err != nil {
		return "", err
	}
	rows := []*db.Tool{}
	for _, p := range staged {
		target := filepath.Join(base, filepath.FromSlash(p.dir))
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return id, err
		}
		if err = os.Rename(p.stage, target); err != nil {
			return id, err
		}
		if kind == "tools" {
			disabled := false
			e := toolEntry{Name: p.item.Name, Purpose: p.item.Description, Version: p.item.Version, Platform: p.asset.Platform, Source: registrySource, Kind: "dir", Path: p.dir, Entry: p.asset.Entry, Enabled: &disabled, Registry: p.meta}
			if _, local := findToolEntry(tm, e.Name); local != nil {
				e.Key = local.Key
				e.Agents = local.Agents
				e.Enabled = local.Enabled
				e.Deferred = local.Deferred
			}
			e.SHA256, err = toolFileSHA256(filepath.Join(target, filepath.FromSlash(e.Entry)))
			if err != nil {
				return id, err
			}
			row, err := s.toolEntryToRow(e, s.allAgentKeys())
			if err != nil {
				return id, err
			}
			rows = append(rows, row)
			if err = appendOrReplaceEntry(tm, e); err != nil {
				return id, err
			}
		} else {
			p.meta.Replaced = s.registryCustomSkillHashes(p.item.Name)
			sm.Skills = appendOrReplaceSkillRecord(sm.Skills, skillSyncRecord{Name: p.item.Name, Source: registrySource, Dir: p.dir, Revision: p.asset.SHA256, SyncedAt: time.Now().UTC().Format(time.RFC3339), Registry: p.meta})
		}
	}
	if kind == "skills" {
		err = s.saveSkillManifest(sm)
	} else {
		if s.m.pg == nil {
			return id, errors.New("数据库不可用")
		}
		before, readErr := os.ReadFile(s.toolsManifestPath())
		if readErr != nil {
			return id, readErr
		}
		err = s.m.pg.ApplyLibraryTools(rows, func() error { return s.saveToolManifest(tm) })
		if err != nil {
			var old toolManifest
			if jsonErr := json.Unmarshal(before, &old); jsonErr == nil {
				err = errors.Join(err, s.saveToolManifest(&old))
			}
		}
		s.invalidateCommandTemplates()
	}
	return id, err
}
