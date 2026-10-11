package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 工具库多源同步：拉源清单 → 与本地清单比对 → 下载校验落盘 → 写回清单 → 注册工具。
//
// 合并策略是**并集**：本地独有的条目（手工加的、页面上传的）不会被源抹掉，源里删掉的
// 条目也不会删本地文件——仓库是别人的，本地磁盘是你自己的。同名条目以源为准刷新
// version/sha256/path/kind/runtime/entry/purpose，但保留本地的 key/agents/enabled/
// deferred（平台侧状态不是源能决定的事）。
//
// 落地顺序是「文件先、清单后」：先把文件按原子替换装好，再写清单。反过来会出现清单
// 声称的版本在磁盘上不存在，比"文件新一点"更糟。清单写入前会留 manifest.json.bak。

const (
	toolSourceTokenPrefix = "tool"
	syncOverallTimeout    = 30 * time.Minute
)

// reSourceName 约束源名：它同时是 settings 里的令牌键后缀与清单里的标识。
var reSourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// toolSyncItem 是一条同步结果（一条目一行，UI 直接渲染）。
type toolSyncItem struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Action string `json:"action"` // add | update | keep | failed
	Reason string `json:"reason,omitempty"`
	Local  string `json:"local_version,omitempty"`
	Remote string `json:"remote_version,omitempty"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

type toolSyncResult struct {
	DryRun  bool           `json:"dry_run"`
	Sources []string       `json:"sources"`
	Items   []toolSyncItem `json:"items"`
	Changed int            `json:"changed"`
	Failed  int            `json:"failed"`
}

// sourceTypeCanSyncTools 报告源类型能否参与工具库的「清单 + 资产」同步。
func sourceTypeCanSyncTools(src libSource) error {
	switch src.typeOrDefault() {
	case "github", "http":
		return nil
	case "git":
		return errors.New("git 型源只支持技能库（整仓克隆），工具库请用 github / http 型清单")
	default:
		return fmt.Errorf("不支持的源类型 %q", src.Type)
	}
}

// tlSources 列出已配置的源（令牌只回"是否已配置"，不回值）。
func (s *Server) tlSources(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(m.Sources))
	for _, src := range m.Sources {
		item := map[string]any{
			"name": src.Name, "type": src.typeOrDefault(),
			"repo": src.Repo, "branch": src.branchOrDefault(), "path": src.Path,
			"url": src.URL, "asset_base": src.AssetBase,
			"allow_unpinned": src.AllowUnpinned,
			"last_sync_at":   src.LastSyncAt, "last_error": src.LastError,
			"has_token": s.sourceToken(toolSourceTokenPrefix, src.Name) != "",
		}
		if u, uerr := sourceManifestURL(src); uerr == nil {
			item["manifest_url"] = u
		} else {
			item["config_error"] = uerr.Error()
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"sources": out})
}

// tlSaveSource 新增或修改一个源（令牌单独处理，不进清单）。
func (s *Server) tlSaveSource(w http.ResponseWriter, r *http.Request) {
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
	if err := sourceTypeCanSyncTools(src); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// 先验证配置能不能用：白名单推导 + 清单地址推导都会在这里报错。
	if _, _, err := s.sourceClient(src); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if _, err := sourceManifestURL(src); err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	m.Sources = upsertLibSource(m.Sources, src)
	if err := s.saveToolManifest(m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	switch {
	case req.ClearToken:
		if err := s.setSourceToken(toolSourceTokenPrefix, src.Name, ""); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	case strings.TrimSpace(req.Token) != "":
		if err := s.setSourceToken(toolSourceTokenPrefix, src.Name, req.Token); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "has_token": s.sourceToken(toolSourceTokenPrefix, src.Name) != ""})
}

// tlDeleteSource 移除源（连带清掉它的令牌）。
func (s *Server) tlDeleteSource(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	name := r.PathValue("name")
	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	kept := m.Sources[:0:0]
	found := false
	for _, src := range m.Sources {
		if src.Name == name {
			found = true
			continue
		}
		kept = append(kept, src)
	}
	if !found {
		writeErr(w, 404, "没有这个源: "+name)
		return
	}
	m.Sources = kept
	if err := s.saveToolManifest(m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := s.setSourceToken(toolSourceTokenPrefix, name, ""); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": name})
}

// tlSync 执行同步。dry_run=true 只回计划和变更原因，不碰磁盘。
func (s *Server) tlSync(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Names  []string `json:"names"`
		DryRun bool     `json:"dry_run"`
		Force  bool     `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	m, err := s.loadToolManifest()
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

	res := toolSyncResult{DryRun: req.DryRun, Items: []toolSyncItem{}}
	agents := s.allAgentKeys()
	touched := []toolEntry{}
	for i := range m.Sources {
		src := m.Sources[i]
		if len(req.Names) > 0 && !contains(req.Names, src.Name) {
			continue
		}
		res.Sources = append(res.Sources, src.Name)
		items, changed, serr := s.syncToolSource(ctx, src, m, req.DryRun, req.Force)
		res.Items = append(res.Items, items...)
		for _, it := range items {
			switch it.Action {
			case "add", "update":
				res.Changed++
			case "failed":
				res.Failed++
			}
		}
		touched = append(touched, changed...)
		if serr != nil {
			putLibSourceState(m.Sources, src.Name, "", serr.Error())
			res.Failed++
			res.Items = append(res.Items, toolSyncItem{Source: src.Name, Name: "-", Action: "failed", Error: serr.Error()})
		} else if !req.DryRun {
			putLibSourceState(m.Sources, src.Name, time.Now().UTC().Format(time.RFC3339), "")
		}
	}

	if !req.DryRun {
		if err := s.saveToolManifest(m); err != nil {
			writeErr(w, 500, "同步已落盘但清单写入失败："+err.Error())
			return
		}
		// 注册（或刷新）被同步到的条目：清单是定义真源，工具行跟着走。
		for _, e := range touched {
			row, rerr := s.toolEntryToRow(e, agents)
			if rerr != nil {
				res.Items = append(res.Items, toolSyncItem{Source: e.Source, Name: e.Name, Action: "failed", Error: "注册失败：" + rerr.Error()})
				res.Failed++
				continue
			}
			if _, uerr := pg.UpsertLibraryTool(row, e.overwriteBinding()); uerr != nil {
				res.Items = append(res.Items, toolSyncItem{Source: e.Source, Name: e.Name, Action: "failed", Error: "注册失败：" + uerr.Error()})
				res.Failed++
			}
		}
		if len(touched) > 0 {
			s.invalidateCommandTemplates()
		}
	}
	writeJSON(w, 200, res)
}

// syncToolSource 同步单个源：拉清单、逐条比对安装，并把新条目写进（内存里的）本地清单。
// 返回本次结果、实际发生变化的条目、以及源级别的错误。
func (s *Server) syncToolSource(ctx context.Context, src libSource, m *toolManifest, dryRun, force bool) ([]toolSyncItem, []toolEntry, error) {
	items := []toolSyncItem{}
	if err := sourceTypeCanSyncTools(src); err != nil {
		return items, nil, err
	}
	client, hosts, err := s.sourceClient(src)
	if err != nil {
		return items, nil, err
	}
	manifestURL, err := sourceManifestURL(src)
	if err != nil {
		return items, nil, err
	}
	token := s.sourceToken(toolSourceTokenPrefix, src.Name)
	raw, err := fetchSourceBytes(ctx, client, hosts, manifestURL, maxSourceManifestBytes, token)
	if err != nil {
		return items, nil, fmt.Errorf("拉取清单失败：%w", err)
	}
	var remote toolManifest
	if err := json.Unmarshal(raw, &remote); err != nil {
		return items, nil, fmt.Errorf("解析源清单失败：%w", err)
	}
	if len(remote.Tools) == 0 {
		return items, nil, errors.New("源清单里没有 tools 条目")
	}

	changed := []toolEntry{}
	for _, re := range remote.Tools {
		item := toolSyncItem{Source: src.Name, Name: re.Name, Remote: re.Version, Path: re.Path}
		if strings.TrimSpace(re.Name) == "" {
			item.Action = "failed"
			item.Error = "条目缺少 name"
			items = append(items, item)
			continue
		}
		cleanPath, perr := toolEntryPath(re)
		if perr != nil {
			item.Action = "failed"
			item.Error = perr.Error()
			items = append(items, item)
			continue
		}

		_, localEntry := findToolEntry(m, re.Name)
		if localEntry != nil {
			item.Local = localEntry.Version
		}
		needInstall, reason := toolEntryChanged(localEntry, re)
		if force {
			needInstall, reason = true, "强制重装"
		}
		switch {
		case localEntry == nil:
			item.Action = "add"
			if reason == "" {
				reason = "本地缺失"
			}
		case needInstall:
			item.Action = "update"
		default:
			item.Action = "keep"
			item.Reason = "已是源上的版本"
			items = append(items, item)
			continue
		}
		item.Reason = reason
		if dryRun {
			items = append(items, item)
			continue
		}

		merged, ierr := s.installToolFromSource(ctx, client, hosts, src, token, manifestURL, re, cleanPath, localEntry)
		if ierr != nil {
			item.Action = "failed"
			item.Error = ierr.Error()
			items = append(items, item)
			continue
		}
		if err := appendOrReplaceEntry(m, merged); err != nil {
			item.Action = "failed"
			item.Error = err.Error()
			items = append(items, item)
			continue
		}
		changed = append(changed, merged)
		item.Path = merged.Path
		items = append(items, item)
	}
	return items, changed, nil
}

// toolEntryChanged 报告源条目相对本地是否需要安装（"" = 不需要）。
func toolEntryChanged(local *toolEntry, remote toolEntry) (bool, string) {
	if local == nil {
		return true, "本地缺失"
	}
	switch {
	case remote.SHA256 == "" && remote.Version == "":
		// 源上什么都没钉：只能每次重装，否则这个条目永远停在首次安装的版本。
		return true, "源未钉摘要/版本，按需重装"
	case remote.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(local.SHA256), strings.TrimSpace(remote.SHA256)):
		return true, "sha256 变化"
	case remote.Version != "" && local.Version != remote.Version:
		return true, "版本变化"
	case remote.Path != "" && local.Path != remote.Path:
		return true, "路径变化"
	case remote.Kind != "" && remote.Kind != local.Kind:
		return true, "类型变化"
	case remote.Runtime != "" && remote.Runtime != local.Runtime:
		return true, "runtime 变化"
	}
	return false, ""
}

// installToolFromSource 下载并安装一条源条目，返回合并后的清单条目。
//
// zip 资产走与上传同一条解压/落盘路径（swapDir 保证覆盖失败可回退）；其它资产按相对
// 路径落到 data/tools 下并钉摘要。任何校验失败都不会破坏本地已有文件。
func (s *Server) installToolFromSource(
	ctx context.Context, client *http.Client, hosts map[string]bool, src libSource, token, manifestURL string,
	remote toolEntry, cleanPath string, local *toolEntry,
) (toolEntry, error) {
	assetURL, err := sourceAssetURL(src, manifestURL, remote.URL, cleanPath)
	if err != nil {
		return toolEntry{}, err
	}
	// 合并：源定义生效，本地平台状态保留。
	merged := remote
	merged.Source = src.Name
	if local != nil {
		merged.Key = local.Key
		if len(remote.Agents) == 0 {
			merged.Agents = local.Agents
		}
		if remote.Enabled == nil {
			merged.Enabled = local.Enabled
		}
		if local.Deferred {
			merged.Deferred = true
		}
	}
	if merged.Name == "" {
		merged.Name = strings.TrimSuffix(filepath.Base(cleanPath), filepath.Ext(cleanPath))
	}

	if strings.EqualFold(filepath.Ext(cleanPath), ".zip") {
		name := merged.Name
		if msg := validToolName(name); msg != "" {
			return toolEntry{}, fmt.Errorf("工具名无效：%s", msg)
		}
		b, ferr := fetchSourceBytes(ctx, client, hosts, assetURL, maxSourceAssetBytes, token)
		if ferr != nil {
			return toolEntry{}, ferr
		}
		want := merged
		if sha := strings.TrimSpace(remote.SHA256); sha != "" {
			if got := hashSourceBytes(b); !strings.EqualFold(got, sha) {
				return toolEntry{}, fmt.Errorf("sha256 不符（清单 %s，实际 %s），已放弃安装", sha, got)
			}
		} else if !src.AllowUnpinned {
			return toolEntry{}, errors.New("条目未提供 sha256（如确认信任该源，可在源上开启 allow_unpinned）")
		}
		tmp, terr := os.CreateTemp(s.toolsDir(), ".sync-*")
		if terr != nil {
			return toolEntry{}, terr
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if _, werr := tmp.Write(b); werr != nil {
			tmp.Close()
			return toolEntry{}, werr
		}
		if cerr := tmp.Close(); cerr != nil {
			return toolEntry{}, cerr
		}
		installed, ierr := s.installToolZip(tmpName, name, want, true)
		if ierr != nil {
			return toolEntry{}, ierr
		}
		if installed.SHA256 == "" {
			installed.SHA256 = hashSourceBytes(b)
		}
		return installed, nil
	}

	dst := filepath.Join(s.toolsDir(), filepath.FromSlash(cleanPath))
	got, err := s.installSourceFile(ctx, client, hosts, assetURL, dst, remote.SHA256, token, src.AllowUnpinned)
	if err != nil {
		return toolEntry{}, err
	}
	if merged.Kind != "script" && merged.Kind != "jar" {
		_ = os.Chmod(dst, 0o755)
	}
	merged.Path = cleanPath
	merged.Entry = ""
	merged.SHA256 = got
	if merged.Kind == "" {
		merged.Kind = "binary"
	}
	return merged, nil
}
