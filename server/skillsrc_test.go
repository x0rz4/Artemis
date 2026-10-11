package server

import (
	"os"
	"path/filepath"
	"testing"
)

// 技能库的多根解析只碰文件系统，用例不需要数据库。

// writeSkill 造一个最小可解析的 skill 目录（agentskills.io 约定的 frontmatter）。
func writeSkill(t *testing.T, root, name, desc string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSkillRootsAndWriteTarget(t *testing.T) {
	base := t.TempDir()
	builtin := filepath.Join(base, "builtin")
	dataDir := filepath.Join(base, "data")
	s := &Server{skillDir: builtin, m: &Manager{dir: dataDir}}

	roots := s.skillRoots()
	if len(roots) != 2 || roots[0] != builtin {
		t.Fatalf("扫描根应为主根 + 上传目录: %v", roots)
	}
	wantUpload := filepath.Join(dataDir, "skills", "uploaded")
	if roots[1] != wantUpload {
		t.Fatalf("第二个根应是 %s: %v", wantUpload, roots)
	}

	// git 同步下来的仓库目录出现后自动成为扫描根。
	if err := os.MkdirAll(filepath.Join(dataDir, "skills", "git", "official"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots = s.skillRoots()
	if len(roots) != 3 || roots[2] != filepath.Join(dataDir, "skills", "git", "official") {
		t.Fatalf("git 目录应进入扫描根: %v", roots)
	}

	// 已存在的 skill 按它实际所在的根解析；不存在的落到写入根。
	builtinSkill := writeSkill(t, builtin, "api-recon", "内置")
	if got := s.skillPath("api-recon"); got != builtinSkill {
		t.Fatalf("应定位到内置根: %q", got)
	}
	if got, want := s.skillPath("brand-new"), filepath.Join(wantUpload, "brand-new"); got != want {
		t.Fatalf("新 skill 应落到写入根: %q", got)
	}
	// 名字非法时不越权：直接回落到写入根下的同名路径（调用方的校验会挡在前面）。
	if got := s.skillOwnerRoot("../escape"); got != "" {
		t.Fatalf("非法名字不应命中任何根: %q", got)
	}
}

func TestLoadAllSkillsPrefersEarlierRoot(t *testing.T) {
	base := t.TempDir()
	builtin := filepath.Join(base, "builtin")
	uploaded := filepath.Join(base, "data", "skills", "uploaded")
	builtinDir := writeSkill(t, builtin, "dup", "内置同名")
	writeSkill(t, uploaded, "dup", "上传同名")
	writeSkill(t, uploaded, "only-uploaded", "只在上传里")

	all := loadAllSkills([]string{builtin, uploaded})
	if len(all) != 2 {
		t.Fatalf("应合并两个根: %v", all)
	}
	if got := all["dup"].Dir; got != builtinDir {
		t.Fatalf("同名 skill 应以靠前的根为准: %q", got)
	}
	if all["dup"].Description != "内置同名" {
		t.Fatalf("应取内置那份的元数据: %+v", all["dup"])
	}
	if _, ok := all["only-uploaded"]; !ok {
		t.Fatal("上传根独有的 skill 也应被加载")
	}
}

func TestLoadSkillRegistryFiltersByVisibility(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "skills")
	writeSkill(t, root, "visible", "可见")
	writeSkill(t, root, "hidden", "不可见")

	reg := loadSkillRegistry([]string{root}, map[string]bool{"visible": true})
	if reg == nil {
		t.Fatal("应当只剩可见 skill")
	}
	list := reg.List()
	if len(list) != 1 || filepath.Base(list[0].Dir) != "visible" {
		t.Fatalf("可见性过滤不对: %+v", list)
	}
	if got := loadSkillRegistry([]string{root}, map[string]bool{"nope": true}); got != nil {
		t.Fatalf("没有可见 skill 时应返回 nil: %+v", got)
	}
}

func TestLoadAllSkillsSkipsMissingRoots(t *testing.T) {
	all := loadAllSkills([]string{filepath.Join(t.TempDir(), "nope")})
	if len(all) != 0 {
		t.Fatalf("不存在的根应被跳过: %v", all)
	}
}
