package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Autumn-27/artex/config"
)

const registryRepo = "x0rz4/Artemis-registry"
const registrySource = "artemis-registry"
const registryIndexURL = "https://raw.githubusercontent.com/" + registryRepo + "/main/channels/stable.json"

var registryReleaseRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var registryHashRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

//go:embed registry_baseline.json
var registryBaselineJSON []byte

type registryRef struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type registryIndex struct {
	Release string      `json:"release"`
	Tools   registryRef `json:"tools"`
	Skills  registryRef `json:"skills"`
}
type registryAsset struct {
	registryRef
	Platform      string `json:"platform"`
	ContentSHA256 string `json:"content_sha256"`
	Entry         string `json:"entry"`
}
type registryItem struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Changes     string          `json:"changes"`
	License     string          `json:"license"`
	Assets      []registryAsset `json:"assets"`
}
type registryCatalog struct {
	Release string         `json:"release"`
	Items   []registryItem `json:"items"`
}
type registryRelease struct {
	Index  registryIndex   `json:"index"`
	Tools  registryCatalog `json:"tools"`
	Skills registryCatalog `json:"skills"`
}
type registryState struct {
	Disabled  bool                       `json:"disabled"`
	CheckedAt string                     `json:"checked_at"`
	Error     string                     `json:"error,omitempty"`
	Release   string                     `json:"release"`
	Releases  map[string]registryRelease `json:"releases"`
}
type registryInstall struct {
	Replaced      map[string]string `json:"replaced,omitempty"`
	Release       string            `json:"release"`
	Version       string            `json:"version"`
	PackageSHA256 string            `json:"package_sha256"`
	ContentSHA256 string            `json:"content_sha256"`
}
type registryUpdateItem struct {
	registryItem
	LocalVersion string `json:"local_version"`
	Status       string `json:"status"` // add, update, keep, conflict, unsupported
	Reason       string `json:"reason,omitempty"`
	Shadowed     bool   `json:"shadowed,omitempty"`
}

func explicitSkillRoot() bool {
	return strings.TrimSpace(os.Getenv("ARTEX_SKILL_DIR")) != "" || strings.TrimSpace(config.Load().SkillDir) != ""
}

func (s *Server) builtinSkillRoot() string {
	if explicitSkillRoot() {
		return filepath.Join(config.BaseDir(), "skills")
	}
	return s.skillDir
}

func (s *Server) registryCustomSkillHashes(name string) map[string]string {
	out := map[string]string{}
	for _, root := range s.skillRoots() {
		p := filepath.Join(root, name)
		if strings.HasPrefix(p, filepath.Join(s.skillsLibDir(), "managed")+string(os.PathSeparator)) {
			continue
		}
		if hash, err := registryTreeHash(p); err == nil {
			out[p] = hash
		}
	}
	return out
}

func (s *Server) registrySkillRoots() []string {
	roots := []string{}
	if s.skillsLibDir() == "" {
		return roots
	}
	m, err := s.loadSkillManifest()
	if err != nil {
		return roots
	}
	for _, rec := range m.Skills {
		if rec.Registry == nil || rec.Source != registrySource {
			continue
		}
		clean, msg := skillRelPath(rec.Dir)
		if msg == "" && strings.HasPrefix(clean, "managed/") && filepath.Base(clean) == rec.Name {
			roots = append(roots, filepath.Dir(filepath.Join(s.skillsLibDir(), filepath.FromSlash(clean))))
		}
	}
	return roots
}

func (s *Server) registryStatePath() string { return filepath.Join(s.m.dir, "registry-state.json") }

func registryWriteJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".registry-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *Server) startRegistryChecks() {
	if b, err := os.ReadFile(s.registryStatePath()); err == nil {
		_ = json.Unmarshal(b, &s.registry)
	}
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-timer.C:
				s.registryMu.Lock()
				disabled := s.registry.Disabled
				s.registryMu.Unlock()
				if !disabled {
					_ = s.checkRegistry(s.ctx)
				}
				timer.Reset(6 * time.Hour)
			}
		}
	}()
}

func (s *Server) registryFetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	client, hosts, err := s.sourceClient(libSource{Type: "github", Repo: registryRepo})
	if err != nil {
		return nil, err
	}
	return fetchSourceBytes(ctx, client, hosts, url, limit, "")
}

func registryURL(release, url string) bool {
	prefix := "https://github.com/" + registryRepo + "/releases/download/" + release + "/"
	name := strings.TrimPrefix(url, prefix)
	return strings.HasPrefix(url, prefix) && name != "" && !strings.ContainsAny(name, "/\\?#%")
}

func validateRegistryCatalog(c registryCatalog, release, kind string) error {
	if c.Release != release || len(c.Items) == 0 || len(c.Items) > 1000 {
		return errors.New("官方清单版本或条目数量无效")
	}
	names := map[string]bool{}
	for _, item := range c.Items {
		if !reSourceName.MatchString(item.Name) || names[item.Name] || item.Version == "" || item.License == "" {
			return errors.New("官方条目名称、版本或许可证无效")
		}
		names[item.Name] = true
		platforms := map[string]bool{}
		for _, a := range item.Assets {
			clean, msg := skillRelPath(a.Entry)
			if !registryURL(release, a.URL) || !strings.HasSuffix(a.URL, ".zip") || !registryHashRE.MatchString(a.SHA256) || !registryHashRE.MatchString(a.ContentSHA256) || msg != "" || clean != a.Entry || platforms[a.Platform] {
				return errors.New("官方包地址、摘要或入口无效")
			}
			if kind == "skills" && (a.Platform != "any" || a.Entry != "SKILL.md") {
				return errors.New("Skill 包平台或入口无效")
			}
			if kind == "tools" && a.Platform != "linux/amd64" && a.Platform != "linux/arm64" {
				return errors.New("工具包平台无效")
			}
			platforms[a.Platform] = true
		}
		if len(item.Assets) == 0 {
			return errors.New("官方条目缺少安装包")
		}
	}
	return nil
}

func (s *Server) checkRegistry(ctx context.Context) (err error) {
	if !s.registryCheckMu.TryLock() {
		return errors.New("正在检查更新，请稍后重试")
	}
	defer s.registryCheckMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	defer func() {
		s.registryMu.Lock()
		defer s.registryMu.Unlock()
		s.registry.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		s.registry.Error = ""
		if err != nil {
			s.registry.Error = err.Error()
		}
		if saveErr := registryWriteJSON(s.registryStatePath(), s.registry); saveErr != nil {
			err = errors.Join(err, saveErr)
			s.registry.Error = err.Error()
		}
	}()
	b, err := s.registryFetch(ctx, registryIndexURL, maxSourceManifestBytes)
	if err != nil {
		return err
	}
	var release registryRelease
	if err = json.Unmarshal(b, &release.Index); err != nil {
		return err
	}
	idx := release.Index
	if !registryReleaseRE.MatchString(idx.Release) {
		return errors.New("官方发布版本格式无效")
	}
	for kind, ref := range map[string]registryRef{"tools": idx.Tools, "skills": idx.Skills} {
		if !registryURL(idx.Release, ref.URL) || !registryHashRE.MatchString(ref.SHA256) {
			return errors.New("官方清单地址或摘要无效")
		}
		raw, ferr := s.registryFetch(ctx, ref.URL, maxSourceManifestBytes)
		if ferr != nil {
			return ferr
		}
		if hashSourceBytes(raw) != ref.SHA256 {
			return errors.New("官方清单 SHA-256 不符")
		}
		var catalog registryCatalog
		if err = json.Unmarshal(raw, &catalog); err != nil {
			return err
		}
		if err = validateRegistryCatalog(catalog, idx.Release, kind); err != nil {
			return err
		}
		if kind == "tools" {
			release.Tools = catalog
		} else {
			release.Skills = catalog
		}
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if s.registry.Releases == nil {
		s.registry.Releases = map[string]registryRelease{}
	}
	if old, ok := s.registry.Releases[idx.Release]; ok && old.Index != idx {
		return errors.New("已发布版本的清单摘要发生变化，拒绝覆盖")
	}
	s.registry.Releases[idx.Release] = release
	s.registry.Release = idx.Release
	return nil
}

func registryPlatformAsset(item registryItem) *registryAsset {
	for _, a := range item.Assets {
		if a.Platform == "any" || a.Platform == runtime.GOOS+"/"+runtime.GOARCH {
			return &a
		}
	}
	return nil
}

func (s *Server) registryItems(kind string, catalog registryCatalog) ([]registryUpdateItem, error) {
	result := []registryUpdateItem{}
	tm, err := s.loadToolManifest()
	if err != nil {
		return nil, err
	}
	sm, err := s.loadSkillManifest()
	if err != nil {
		return nil, err
	}
	baseline := map[string][]string{}
	_ = json.Unmarshal(registryBaselineJSON, &baseline)
	for _, item := range catalog.Items {
		v := registryUpdateItem{registryItem: item, Status: "add"}
		a := registryPlatformAsset(item)
		var meta *registryInstall
		var dir string
		if kind == "tools" {
			if _, local := findToolEntry(tm, item.Name); local != nil {
				v.LocalVersion = local.Version
				meta = local.Registry
				if meta == nil {
					v.Status = "conflict"
					v.Reason = "存在同名本地工具"
				} else {
					dir = filepath.Join(s.toolsDir(), filepath.FromSlash(local.Path))
				}
			}
		} else {
			if rec := skillRecordOf(sm.Skills, item.Name); rec != nil && rec.Registry != nil {
				meta = rec.Registry
				dir = filepath.Join(s.skillsLibDir(), filepath.FromSlash(rec.Dir))
			}
			// Detect custom copies even when a managed copy currently shadows them.
			for _, root := range s.skillRoots() {
				p := filepath.Join(root, item.Name)
				if _, statErr := os.Stat(p); statErr != nil {
					continue
				}
				if strings.HasPrefix(p, filepath.Join(s.skillsLibDir(), "managed")+string(os.PathSeparator)) {
					continue
				}
				got, hashErr := registryTreeHash(p)
				if meta != nil && hashErr == nil && meta.Replaced[p] == got {
					v.Shadowed = explicitSkillRoot() && root == s.skillDir
					continue
				}
				if root == s.builtinSkillRoot() && !(explicitSkillRoot() && root == s.skillDir) && hashErr == nil && contains(baseline[item.Name], got) {
					if v.LocalVersion == "" {
						v.LocalVersion = "内置"
					}
					continue
				}
				v.Status = "conflict"
				v.Reason = "存在同名自定义 Skill 或本地修改"
				v.Shadowed = explicitSkillRoot() && root == s.skillDir
			}
		}
		if meta != nil {
			v.LocalVersion = meta.Version
			got, hashErr := registryTreeHash(dir)
			if hashErr != nil || got != meta.ContentSHA256 {
				v.Status = "conflict"
				v.Reason = "已安装内容被修改或缺失"
			}
			if v.Status != "conflict" {
				v.Status = "update"
				if a != nil && a.SHA256 == meta.PackageSHA256 && item.Version == meta.Version {
					v.Status = "keep"
				}
			}
		} else if v.LocalVersion != "" && v.Status != "conflict" {
			v.Status = "update"
		}
		if a == nil {
			v.Status = "unsupported"
			v.Reason = "当前平台没有安装包"
		}
		result = append(result, v)
	}
	return result, nil
}

func (s *Server) registryStatus(w http.ResponseWriter, r *http.Request) {
	s.registryMu.Lock()
	state := s.registry
	release := state.Releases[state.Release]
	s.registryMu.Unlock()
	kind := r.URL.Query().Get("kind")
	if kind != "tools" && kind != "skills" {
		writeErr(w, 400, "kind 必须为 tools 或 skills")
		return
	}
	catalog := release.Tools
	if kind == "skills" {
		catalog = release.Skills
	}
	items, err := s.registryItems(kind, catalog)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"source": registrySource, "release": state.Release, "disabled": state.Disabled, "checked_at": state.CheckedAt, "error": state.Error, "platform": runtime.GOOS + "/" + runtime.GOARCH, "items": items})
}

func (s *Server) registryCheck(w http.ResponseWriter, r *http.Request) {
	if err := s.checkRegistry(r.Context()); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	s.registryStatus(w, r)
}

func (s *Server) registrySettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "无效请求")
		return
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	before := s.registry.Disabled
	s.registry.Disabled = req.Disabled
	if err := registryWriteJSON(s.registryStatePath(), s.registry); err != nil {
		s.registry.Disabled = before
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"disabled": req.Disabled})
}

func (s *Server) registryPinned(release, kind string) (registryCatalog, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if !registryReleaseRE.MatchString(release) {
		return registryCatalog{}, errors.New("请指定检查过的发布版本")
	}
	r, ok := s.registry.Releases[release]
	if !ok {
		return registryCatalog{}, fmt.Errorf("尚未检查版本 %s，请先检查更新", release)
	}
	if kind == "skills" {
		return r.Skills, nil
	}
	return r.Tools, nil
}
