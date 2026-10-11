package server

import (
	"net/url"
	"testing"
)

// 源同步的地址推导与白名单是纯函数，用例不需要数据库。

func TestSourceHostsPerSource(t *testing.T) {
	gh, err := sourceHosts(libSource{Type: "github", Repo: "org/tools"})
	if err != nil {
		t.Fatal(err)
	}
	if !gh["raw.githubusercontent.com"] || !gh["objects.githubusercontent.com"] {
		t.Fatalf("github 源应放行 GitHub 系域名: %v", gh)
	}
	if gh["dl.example.com"] {
		t.Fatal("github 源不该放行无关域名")
	}
	// 白名单是**按源**派生的：http 源只认自己那一个 host（与 selfupdate 的固定名单不同）。
	h, err := sourceHosts(libSource{Type: "http", URL: "https://dl.example.com/tools/manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	if !h["dl.example.com"] || len(h) != 1 {
		t.Fatalf("http 源应只放行自己的 host: %v", h)
	}

	for _, bad := range []libSource{
		{Type: "github"}, // 缺 repo
		{Type: "http"},   // 缺 url
		{Type: "http", URL: "http://dl.example.com/m.json"}, // 非 https
		{Type: "ftp", URL: "https://dl.example.com/m.json"}, // 未知类型
	} {
		if _, err := sourceHosts(bad); err == nil {
			t.Errorf("%+v 应当报错", bad)
		}
	}
	// git 型（技能库用）的 host 白名单按其 URL 派生。
	if _, err := sourceHosts(libSource{Type: "git", URL: "https://github.com/org/skills.git"}); err != nil {
		t.Fatalf("git 型源应可解析: %v", err)
	}
	if err := sourceTypeCanSyncTools(libSource{Type: "git", URL: "https://github.com/org/x.git"}); err == nil {
		t.Fatal("git 型不得参与工具清单同步")
	}
}

func TestSourceManifestURL(t *testing.T) {
	u, err := sourceManifestURL(libSource{Type: "github", Repo: "org/tools"})
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://raw.githubusercontent.com/org/tools/main/manifest.json" {
		t.Fatalf("github 默认清单地址: %q", u)
	}
	u, err = sourceManifestURL(libSource{Type: "github", Repo: "org/tools", Branch: "dev", Path: "packaging/manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://raw.githubusercontent.com/org/tools/dev/packaging/manifest.json" {
		t.Fatalf("github 自定义清单地址: %q", u)
	}
	// 清单路径同样是不可信输入。
	if _, err := sourceManifestURL(libSource{Type: "github", Repo: "org/tools", Path: "../secret"}); err == nil {
		t.Fatal("清单路径穿越应当报错")
	}
	u, err = sourceManifestURL(libSource{Type: "http", URL: "https://dl.example.com/a/b.json"})
	if err != nil || u != "https://dl.example.com/a/b.json" {
		t.Fatalf("http 清单地址应原样返回: %q %v", u, err)
	}
	if _, err := sourceManifestURL(libSource{Type: "git", URL: "https://x/y.git"}); err == nil {
		t.Fatal("git 型没有清单文件")
	}
}

func TestSourceAssetURL(t *testing.T) {
	src := libSource{Type: "github", Repo: "org/tools", Path: "packaging/manifest.json"}
	manifest, err := sourceManifestURL(src)
	if err != nil {
		t.Fatal(err)
	}
	// 缺省资产根 = 清单所在目录（同仓库同分支）。
	got, err := sourceAssetURL(src, manifest, "", "fscan")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://raw.githubusercontent.com/org/tools/main/packaging/fscan" {
		t.Fatalf("缺省资产地址: %q", got)
	}
	// asset_base 覆盖。
	src.AssetBase = "https://dl.example.com/tools"
	got, err = sourceAssetURL(src, manifest, "", "fscan")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://dl.example.com/tools/fscan" {
		t.Fatalf("asset_base 覆盖失败: %q", got)
	}
	// 条目自带的绝对地址优先（发布资产常和清单不同域）。
	got, err = sourceAssetURL(src, manifest, "https://objects.githubusercontent.com/x/fscan", "fscan")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://objects.githubusercontent.com/x/fscan" {
		t.Fatalf("条目 url 应当优先: %q", got)
	}
	// 资产路径同样过 skillRelPath。
	if _, err := sourceAssetURL(src, manifest, "", "../../etc/passwd"); err == nil {
		t.Fatal("资产路径穿越应当报错")
	}
	if _, err := sourceAssetURL(src, manifest, "", ""); err == nil {
		t.Fatal("缺 path 应当报错")
	}
}

func TestCheckSourceURLEnforcesHTTPSAndHost(t *testing.T) {
	hosts := map[string]bool{"dl.example.com": true}
	good, _ := url.Parse("https://dl.example.com/a.zip")
	if err := checkSourceURL(good, hosts); err != nil {
		t.Fatalf("https + 白名单内应当放行: %v", err)
	}
	for _, raw := range []string{
		"http://dl.example.com/a.zip",           // 非 HTTPS
		"https://evil.com/a.zip",                // 白名单外
		"https://dl.example.com.evil.com/a.zip", // 后缀伪装
	} {
		u, _ := url.Parse(raw)
		if err := checkSourceURL(u, hosts); err == nil {
			t.Errorf("%s 应当被拒绝", raw)
		}
	}
}

func TestUpsertLibSourceKeepsState(t *testing.T) {
	list := []libSource{{Name: "official", Type: "github", Repo: "o/r", LastSyncAt: "2026-01-01T00:00:00Z", LastError: "旧错误"}}
	list = upsertLibSource(list, libSource{Name: "official", Type: "github", Repo: "o/r2"})
	if len(list) != 1 {
		t.Fatalf("同名源应当覆盖而不是追加: %v", list)
	}
	if list[0].Repo != "o/r2" {
		t.Fatalf("应刷新配置: %+v", list[0])
	}
	if list[0].LastSyncAt == "" {
		t.Fatal("上次同步时间应保留")
	}
	if list[0].LastError != "" {
		t.Fatal("重新保存应清掉上一次的错误")
	}
	list = append(list, libSource{Name: "second"})
	putLibSourceState(list, "second", "2026-02-02T00:00:00Z", "boom")
	if list[1].LastSyncAt != "2026-02-02T00:00:00Z" || list[1].LastError != "boom" {
		t.Fatalf("同步状态未写入: %+v", list[1])
	}
	// 不带时间戳时只更新错误（同步失败路径不能把上次成功时间抹掉）。
	putLibSourceState(list, "second", "", "again")
	if list[1].LastSyncAt != "2026-02-02T00:00:00Z" || list[1].LastError != "again" {
		t.Fatalf("失败路径不应清掉上次同步时间: %+v", list[1])
	}
}

func TestToolEntryChanged(t *testing.T) {
	local := &toolEntry{Name: "fscan", Path: "fscan", Kind: "binary", Version: "2.2.1", SHA256: "aa"}
	cases := []struct {
		name    string
		remote  toolEntry
		changed bool
	}{
		{"sha256 变化", toolEntry{Path: "fscan", SHA256: "bb"}, true},
		{"版本变化", toolEntry{Path: "fscan", Version: "2.2.2", SHA256: "aa"}, true},
		{"路径变化", toolEntry{Path: "bin/fscan", SHA256: "aa"}, true},
		{"类型变化", toolEntry{Path: "fscan", Kind: "script", SHA256: "aa"}, true},
		{"完全一致", toolEntry{Path: "fscan", Kind: "binary", Version: "2.2.1", SHA256: "aa"}, false},
		{"源未钉摘要也未给版本", toolEntry{Path: "fscan"}, true},
	}
	for _, c := range cases {
		got, _ := toolEntryChanged(local, c.remote)
		if got != c.changed {
			t.Errorf("%s: changed=%v, want %v", c.name, got, c.changed)
		}
	}
	// 本地缺失 → 必须安装。
	if got, why := toolEntryChanged(nil, toolEntry{Path: "x"}); !got || why == "" {
		t.Fatalf("本地缺失应当需要安装: %v %q", got, why)
	}
}

func TestOverlayToolEntryKeepsCallerFields(t *testing.T) {
	base := toolEntry{Name: "x", Path: "p", Source: "uploaded", Kind: "binary"}
	overlayToolEntry(&base, toolEntry{Source: "official", Runtime: "python3", Version: "1"})
	if base.Source != "official" || base.Runtime != "python3" || base.Version != "1" {
		t.Fatalf("非空字段应覆盖: %+v", base)
	}
	if base.Kind != "binary" || base.Name != "x" {
		t.Fatalf("空字段不应清掉基础值: %+v", base)
	}
}

func TestValidToolRuntime(t *testing.T) {
	for _, ok := range []string{"", "python3", "Python", "node", "php", "java"} {
		if !validToolRuntime(ok) {
			t.Errorf("%q 应当被接受", ok)
		}
	}
	for _, bad := range []string{"cobol", "go", "python2"} {
		if validToolRuntime(bad) {
			t.Errorf("%q 应当被拒绝", bad)
		}
	}
}
