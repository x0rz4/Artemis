package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

type registryTransport func(*http.Request) (*http.Response, error)

func (f registryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func registryMock(t *testing.T, files map[string][]byte, hits *[]string) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = registryTransport(func(r *http.Request) (*http.Response, error) {
		if hits != nil {
			*hits = append(*hits, r.URL.String())
		}
		if b, ok := files[r.URL.String()]; ok {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header), Request: r}, nil
		}
		return nil, errors.New("simulated network interruption")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}

func registryFixture(t *testing.T, s *Server, kind, name, version string) (registryItem, []byte) {
	t.Helper()
	root := t.TempDir()
	entry := "SKILL.md"
	platform := "any"
	if kind == "skills" {
		writeSkill(t, filepath.Dir(root), filepath.Base(root), "fixture")
		if err := os.WriteFile(filepath.Join(root, entry), []byte("---\nname: "+name+"\ndescription: "+version+"\n---\n"+version), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		entry = name
		platform = "linux/amd64"
		b := make([]byte, 32)
		copy(b, []byte("\x7fELF\x02\x01"))
		b[18] = 62
		copy(b[20:], version)
		if err := os.WriteFile(filepath.Join(root, entry), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "LICENSE"), []byte("fixture license"), 0o644)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{entry, "LICENSE"} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(root, name))
		_, _ = w.Write(b)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	sum, err := registryTreeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	a := registryAsset{registryRef: registryRef{URL: "https://github.com/" + registryRepo + "/releases/download/v1.0.0/" + name + ".zip", SHA256: hashSourceBytes(buf.Bytes())}, Platform: platform, ContentSHA256: sum, Entry: entry}
	return registryItem{Name: name, Version: version, Description: "fixture", License: "MIT", Assets: []registryAsset{a}}, buf.Bytes()
}

func TestRegistryCheckPinsManifestsWithoutDownloadingPackages(t *testing.T) {
	s := newToolLibServer(t)
	tool, tb := registryFixture(t, s, "tools", "example", "1")
	sk, sb := registryFixture(t, s, "skills", "example", "1")
	tools, _ := json.Marshal(registryCatalog{Release: "v1.0.0", Items: []registryItem{tool}})
	skills, _ := json.Marshal(registryCatalog{Release: "v1.0.0", Items: []registryItem{sk}})
	prefix := "https://github.com/" + registryRepo + "/releases/download/v1.0.0/"
	idx := registryIndex{Release: "v1.0.0", Tools: registryRef{prefix + "tools.json", hashSourceBytes(tools)}, Skills: registryRef{prefix + "skills.json", hashSourceBytes(skills)}}
	b, _ := json.Marshal(idx)
	files := map[string][]byte{registryIndexURL: b, idx.Tools.URL: tools, idx.Skills.URL: skills, tool.Assets[0].URL: tb, sk.Assets[0].URL: sb}
	hits := []string{}
	registryMock(t, files, &hits)
	if err := s.checkRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("check downloaded package: %v", hits)
	}
	if _, err := s.registryPinned("v1.0.0", "tools"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.registryPinned("v2.0.0", "tools"); err == nil {
		t.Fatal("unchecked version accepted")
	}
	files[idx.Tools.URL] = []byte("bad digest")
	if err := s.checkRegistry(context.Background()); err == nil {
		t.Fatal("bad digest accepted")
	}
	if s.registry.Release != "v1.0.0" || s.registry.Error == "" {
		t.Fatal("failed check lost previous release or error")
	}
	delete(files, registryIndexURL)
	if err := s.checkRegistry(context.Background()); err == nil {
		t.Fatal("network failure accepted")
	}
}

func TestRegistrySkillActivationConflictRepeatAndRollback(t *testing.T) {
	t.Setenv("ARTEX_SKILL_DIR", "")
	s := newToolLibServer(t)
	s.skillDir = t.TempDir()
	writeSkill(t, s.skillDir, "example", "custom")
	item, b := registryFixture(t, s, "skills", "example", "1.0.0")
	registryMock(t, map[string][]byte{item.Assets[0].URL: b}, nil)
	c := registryCatalog{Release: "v1.0.0", Items: []registryItem{item}}
	items, err := s.registryItems("skills", c)
	if err != nil || items[0].Status != "conflict" {
		t.Fatalf("custom conflict missing: %v %v", items, err)
	}
	p, err := s.stageRegistry(context.Background(), "skills", item, item.Assets[0], c.Release)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.commitRegistry("skills", []registryStaged{p}, c.Release)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadAllSkills(s.skillRoots())["example"]
	if loaded.Dir == filepath.Join(s.skillDir, "example") || !strings.Contains(loaded.Dir, "managed") {
		t.Fatalf("managed version not active: %v", loaded)
	}
	items, err = s.registryItems("skills", c)
	if err != nil || items[0].Status != "keep" {
		t.Fatalf("repeat check: %v %v", items, err)
	}
	t.Setenv("ARTEX_SKILL_DIR", s.skillDir)
	if s.skillPath("example") != filepath.Join(s.skillDir, "example") {
		t.Fatal("explicit local directory lost priority")
	}
	t.Setenv("ARTEX_SKILL_DIR", "")
	if err = os.WriteFile(filepath.Join(loaded.Dir, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err = s.registryItems("skills", c)
	if err != nil || items[0].Status != "conflict" {
		t.Fatalf("local edit not detected: %v", items)
	}
	if _, err = restoreSnapshot(s.skillsLibDir(), id); err != nil {
		t.Fatal(err)
	}
	if s.skillPath("example") != filepath.Join(s.skillDir, "example") {
		t.Fatal("rollback did not restore fallback")
	}
}

func TestRegistryDownloadFailureKeepsInstalledSkill(t *testing.T) {
	s := newToolLibServer(t)
	s.skillDir = t.TempDir()
	old := writeSkill(t, s.skillDir, "example", "old")
	item, b := registryFixture(t, s, "skills", "example", "1")
	registryMock(t, map[string][]byte{item.Assets[0].URL: b}, nil)
	a := item.Assets[0]
	a.SHA256 = strings.Repeat("0", 64)
	if _, err := s.stageRegistry(context.Background(), "skills", item, a, "v1.0.0"); err == nil {
		t.Fatal("bad checksum installed")
	}
	a = item.Assets[0]
	a.ContentSHA256 = strings.Repeat("0", 64)
	if _, err := s.stageRegistry(context.Background(), "skills", item, a, "v1.0.0"); err == nil {
		t.Fatal("bad content installed")
	}
	if s.skillPath("example") != old {
		t.Fatal("failed install changed active version")
	}
	a = item.Assets[0]
	a.URL += ".missing"
	if _, err := s.stageRegistry(context.Background(), "skills", item, a, "v1.0.0"); err == nil {
		t.Fatal("network interruption accepted")
	}
}

func TestRegistryActivityBarrier(t *testing.T) {
	s := newToolLibServer(t)
	s.engine = &Engine{}
	s.chatBusy = map[string]bool{"chat": true}
	if _, err := s.lockLibraryIdle(); err == nil {
		t.Fatal("busy chat accepted")
	}
	delete(s.chatBusy, "chat")
	if !s.engine.beginTaskOperation("1") {
		t.Fatal("task admission")
	}
	if _, err := s.lockLibraryIdle(); err == nil {
		t.Fatal("inflight operation accepted")
	}
	s.engine.decInflight("1")
	unlock, err := s.lockLibraryIdle()
	if err != nil {
		t.Fatal(err)
	}
	if s.chatMu.TryLock() {
		s.chatMu.Unlock()
		t.Fatal("chat admission barrier missing")
	}
	if s.engine.deleteMu.TryRLock() {
		s.engine.deleteMu.RUnlock()
		t.Fatal("task admission barrier missing")
	}
	if _, err = s.lockLibraryIdle(); err == nil {
		t.Fatal("concurrent application accepted")
	}
	unlock()
}

func TestRegistryCatalogValidation(t *testing.T) {
	s := newToolLibServer(t)
	item, _ := registryFixture(t, s, "tools", "example", "1")
	c := registryCatalog{Release: "v1.0.0", Items: []registryItem{item}}
	if err := validateRegistryCatalog(c, c.Release, "tools"); err != nil {
		t.Fatal(err)
	}
	c.Items[0].Assets[0].URL = "https://github.com/other/repo/releases/download/v1.0.0/a.zip"
	if err := validateRegistryCatalog(c, c.Release, "tools"); err == nil {
		t.Fatal("wrong repository accepted")
	}
	if registryPlatformAsset(registryItem{Assets: []registryAsset{{Platform: "unsupported/arch"}}}) != nil {
		t.Fatal("unsupported platform selected")
	}
}

func TestRegistryAutoCheckSettingPersists(t *testing.T) {
	s := newToolLibServer(t)
	w := httptest.NewRecorder()
	s.registrySettings(w, httptest.NewRequest("PUT", "/", strings.NewReader(`{"disabled":true}`)))
	if w.Code != 200 || !s.registry.Disabled {
		t.Fatalf("setting failed: %s", w.Body.String())
	}
	b, err := os.ReadFile(s.registryStatePath())
	if err != nil {
		t.Fatal(err)
	}
	var state registryState
	if err = json.Unmarshal(b, &state); err != nil || !state.Disabled {
		t.Fatal("setting was not persisted")
	}
	w = httptest.NewRecorder()
	s.registrySettings(w, httptest.NewRequest("PUT", "/", strings.NewReader(`{"disabled":false}`)))
	if w.Code != 200 || s.registry.Disabled {
		t.Fatal("auto check could not be enabled")
	}
}

func TestRegistryBundledSkillsAreNotConflicts(t *testing.T) {
	t.Setenv("ARTEX_SKILL_DIR", "")
	s := newToolLibServer(t)
	var err error
	s.skillDir, err = filepath.Abs("../skills")
	if err != nil {
		t.Fatal(err)
	}
	catalog := registryCatalog{Release: "v1.0.0"}
	for _, name := range []string{"api-recon", "playwright-cli", "scopesentry"} {
		catalog.Items = append(catalog.Items, registryItem{Name: name, Version: "1.0.0", Assets: []registryAsset{{Platform: "any"}}})
	}
	items, err := s.registryItems("skills", catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status != "update" || item.LocalVersion != "内置" {
			t.Fatalf("unmodified bundled %s reported %s: %s", item.Name, item.Status, item.Reason)
		}
	}
}

func TestRegistrySelectionAndConflictSkip(t *testing.T) {
	s := newToolLibServer(t)
	s.skillDir = t.TempDir()
	writeSkill(t, s.skillDir, "example", "custom")
	item, _ := registryFixture(t, s, "skills", "example", "1")
	s.registry.Releases = map[string]registryRelease{"v1.0.0": {Skills: registryCatalog{Release: "v1.0.0", Items: []registryItem{item}}}}
	hits := []string{}
	registryMock(t, map[string][]byte{}, &hits)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	s.syncRegistry(w, r, "skills", librarySyncRequest{Names: []string{registrySource}, Items: []string{"example"}, Release: "v1.0.0"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"changed":0`) || len(hits) != 0 {
		t.Fatalf("conflict was not skipped: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.syncRegistry(w, r, "skills", librarySyncRequest{Names: []string{"custom"}, Items: []string{"example"}, Release: "v1.0.0"})
	if w.Code != 400 {
		t.Fatal("names source semantics changed")
	}
}

func TestRegistryToolPreservesDatabaseBindings(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("isolated PostgreSQL required: %v", err)
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Skipf("isolated PostgreSQL required: %v", err)
	}
	defer d.Close()
	s := newToolLibServer(t)
	s.m.pg = d
	name := "registry_fixture_tool"
	defer d.Exec("DELETE FROM tools WHERE key=$1", name)
	d.Exec("DELETE FROM tools WHERE key=$1", name)
	item, b := registryFixture(t, s, "tools", name, "1.0.0")
	registryMock(t, map[string][]byte{item.Assets[0].URL: b}, nil)
	p, err := s.stageRegistry(context.Background(), "tools", item, item.Assets[0], "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.commitRegistry("tools", []registryStaged{p}, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	row, err := d.GetTool(name)
	if err != nil || row == nil || row.Enabled {
		t.Fatalf("new tool should be disabled: %v %v", row, err)
	}
	if _, err = d.Exec(`UPDATE tools SET enabled=true, deferred=true, agents='["custom-agent"]' WHERE key=$1`, name); err != nil {
		t.Fatal(err)
	}
	p, err = s.stageRegistry(context.Background(), "tools", item, item.Assets[0], "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.commitRegistry("tools", []registryStaged{p}, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	row, err = d.GetTool(name)
	if err != nil || !row.Enabled || !row.Deferred || len(row.Agents) != 1 || row.Agents[0] != "custom-agent" {
		t.Fatalf("binding changed: %v %v", row, err)
	}
	m, err := s.loadToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	_, entry := findToolEntry(m, name)
	if entry.SHA256 == entry.Registry.PackageSHA256 || entry.Registry.ContentSHA256 == "" {
		t.Fatal("package and file digests conflated")
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		items, err := s.registryItems("tools", registryCatalog{Items: []registryItem{item}})
		if err != nil || items[0].Status != "keep" {
			t.Fatalf("repeat check: %v %v", items, err)
		}
	}
	s.toolLibMu.Lock()
	w := httptest.NewRecorder()
	s.tlSync(w, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)))
	s.toolLibMu.Unlock()
	if w.Code != 409 {
		t.Fatalf("concurrent update not rejected: %d", w.Code)
	}
	before := row.Description
	row.Description = "should roll back"
	if err = d.ApplyLibraryTools([]*db.Tool{row}, func() error { return errors.New("manifest failure") }); err == nil {
		t.Fatal("publish failure ignored")
	}
	after, err := d.GetTool(name)
	if err != nil || after.Description != before {
		t.Fatal("database transaction not rolled back")
	}
}

func TestRegistryRejectsUnsafeArchivesAndWrongArchitecture(t *testing.T) {
	s := newToolLibServer(t)
	item, b := registryFixture(t, s, "tools", "example", "1")
	files := map[string][]byte{item.Assets[0].URL: b}
	registryMock(t, files, nil)
	a := item.Assets[0]
	a.Platform = "linux/arm64"
	if _, err := s.stageRegistry(context.Background(), "tools", item, a, "v1.0.0"); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	for _, names := range [][]string{{"../escape"}, {"same", "same"}, {"same", "SAME"}} {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for _, name := range names {
			w, _ := z.Create(name)
			_, _ = w.Write([]byte("x"))
		}
		_ = z.Close()
		files[a.URL] = buf.Bytes()
		a.SHA256 = hashSourceBytes(buf.Bytes())
		if _, err := s.stageRegistry(context.Background(), "tools", item, a, "v1.0.0"); err == nil {
			t.Fatalf("unsafe archive accepted: %v", names)
		}
	}
}

func TestRegistrySkillRuntimeRefresh(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL required")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := newToolLibServer(t)
	s.m.pg = d
	s.skillDir = t.TempDir()
	name := "registry-runtime"
	oldDir := writeSkill(t, s.skillDir, name, "old runtime content")
	if err := os.WriteFile(filepath.Join(oldDir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: old\n---\nold runtime content"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := d.GetAgentByKey("planner")
	if err != nil || a == nil {
		t.Fatal("planner missing")
	}
	orig, _ := d.AgentSkillNames(a.ID)
	defer d.SetAgentSkillVisibility(a.ID, orig)
	if err = d.SetAgentSkillVisibility(a.ID, []string{name}); err != nil {
		t.Fatal(err)
	}
	previous := agent.ToolAugment
	defer func() { agent.ToolAugment = previous }()
	wireAgentAugment(d, s.skillRoots, nil)
	read := func() string {
		extra, _, cleanup := agent.ToolAugment(context.Background(), "planner")
		defer cleanup()
		for _, tool := range extra {
			if tool.Name() == "Skill" {
				b, _ := json.Marshal(map[string]string{"name": name})
				result, err := tool.Call(context.Background(), b, nil)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(result)
				return string(data)
			}
		}
		t.Fatal("Skill tool missing")
		return ""
	}
	if out := read(); !strings.Contains(out, "old runtime content") {
		t.Fatalf("old Skill not loaded: %s", out)
	}
	item, b := registryFixture(t, s, "skills", name, "new runtime content")
	registryMock(t, map[string][]byte{item.Assets[0].URL: b}, nil)
	p, err := s.stageRegistry(context.Background(), "skills", item, item.Assets[0], "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.commitRegistry("skills", []registryStaged{p}, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if out := read(); !strings.Contains(out, "new runtime content") {
		t.Fatalf("updated Skill not loaded: %s", out)
	}
	if _, err = restoreSnapshot(s.skillsLibDir(), id); err != nil {
		t.Fatal(err)
	}
	if out := read(); !strings.Contains(out, "old runtime content") {
		t.Fatalf("rollback Skill not loaded: %s", out)
	}
}

// Opt-in public-release round trip. Ordinary CI remains deterministic/offline.
func TestRegistryLiveRelease(t *testing.T) {
	if os.Getenv("ARTEX_TEST_REGISTRY_LIVE") != "1" {
		t.Skip("set ARTEX_TEST_REGISTRY_LIVE=1 for public release validation")
	}
	dsn, _, err := db.DSN()
	if err != nil {
		t.Fatal("isolated PostgreSQL required")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := newToolLibServer(t)
	s.m.pg = d
	s.skillDir = t.TempDir()
	if err = s.checkRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := s.registry.Release
	c, err := s.registryPinned(release, "skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Items) != 3 {
		t.Fatalf("expected 3 skills: %d", len(c.Items))
	}
	apply := func() map[string]any {
		body, _ := json.Marshal(librarySyncRequest{Names: []string{registrySource}, Items: []string{"api-recon"}, Release: release})
		w := httptest.NewRecorder()
		s.skSync(w, httptest.NewRequest("POST", "/api/skills/sync", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("apply HTTP %d: %s", w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := apply()
	if first["changed"] != float64(1) {
		t.Fatalf("first install: %v", first)
	}
	if len(loadAllSkills(s.skillRoots())) != 1 {
		t.Fatal("selection installed unexpected skills")
	}
	if err = s.checkRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second := apply(); second["changed"] != float64(0) {
		t.Fatalf("repeat install: %v", second)
	}
	body, _ := json.Marshal(map[string]any{"id": first["snapshot"]})
	w := httptest.NewRecorder()
	s.skRollback(w, httptest.NewRequest("POST", "/api/skills/rollback", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("rollback HTTP %d: %s", w.Code, w.Body.String())
	}
	if len(loadAllSkills(s.skillRoots())) != 0 {
		t.Fatal("rollback left managed Skill active")
	}
	t.Logf("public %s: check, selected install, repeat check/no change, API rollback passed", release)
}
