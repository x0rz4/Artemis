package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 工具库的逻辑几乎全在文件系统上（清单读写、命令模板、zip 布局判定），所以这些用例
// 只需要一个带 data 目录的 Server，不需要数据库。

func newToolLibServer(t *testing.T) *Server {
	t.Helper()
	return &Server{m: &Manager{dir: t.TempDir()}}
}

func TestToolKeyFromName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fscan", "fscan"},
		{"dirsearch-py", "dirsearch_py"},
		{"  Nmap  ", "nmap"},
		{"7zip", "t_7zip"},
		{"a.b c", "a_b_c"},
		{"gogo__v2", "gogo_v2"},
		// 全非 ASCII → 推导不出 key，清单里必须显式写 key
		{"中文工具", ""},
	}
	for _, c := range cases {
		if got := toolKeyFromName(c.in); got != c.want {
			t.Errorf("toolKeyFromName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEntryKeyExplicitWins(t *testing.T) {
	key, err := entryKey(toolEntry{Name: "中文工具", Key: "zh_tool"})
	if err != nil || key != "zh_tool" {
		t.Fatalf("显式 key 应生效: %q %v", key, err)
	}
	if _, err := entryKey(toolEntry{Name: "中文工具"}); err == nil {
		t.Fatal("推导不出 key 时应当报错")
	}
	if _, err := entryKey(toolEntry{Name: "x", Key: "Bad-Key"}); err == nil {
		t.Fatal("非法显式 key 应当报错")
	}
}

func TestToolCommandShapes(t *testing.T) {
	s := newToolLibServer(t)
	dir := filepath.ToSlash(s.toolsDir())

	native, err := s.toolCommand(toolEntry{Name: "fscan", Path: "fscan", Kind: "binary"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(native, "'"+dir+"/fscan'") || !strings.HasSuffix(native, "{args}") {
		t.Fatalf("原生二进制命令形状不对: %q", native)
	}

	py, err := s.toolCommand(toolEntry{Name: "dirsearch", Path: "dirsearch/dirsearch.py", Kind: "script", Runtime: "python3"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(py, "python3 '") || !strings.Contains(py, "/dirsearch/dirsearch.py'") {
		t.Fatalf("python 脚本命令形状不对: %q", py)
	}

	jar, err := s.toolCommand(toolEntry{Name: "mytool", Path: "jars/mytool.jar", Kind: "jar"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(jar, "java -jar '") {
		t.Fatalf("jar 命令形状不对: %q", jar)
	}

	// kind=dir 必须有入口文件：没有它连"跑哪个文件"都不确定。
	if _, err := s.toolCommand(toolEntry{Name: "peass", Path: "peass", Kind: "dir"}); err == nil {
		t.Fatal("kind=dir 缺 entry 应当报错")
	}
	dirTool, err := s.toolCommand(toolEntry{Name: "peass", Path: "peass", Kind: "dir", Entry: "linpeas.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dirTool, "/peass/linpeas.sh'") {
		t.Fatalf("目录型入口路径不对: %q", dirTool)
	}

	// script 必然要有解释器，否则生成的命令只能靠执行位，脚本文件通常没有。
	if _, err := s.toolCommand(toolEntry{Name: "x", Path: "x.py", Kind: "script"}); err == nil {
		t.Fatal("kind=script 缺 runtime 应当报错")
	}
	if _, err := s.toolCommand(toolEntry{Name: "x", Path: "x.py", Kind: "script", Runtime: "cobol"}); err == nil {
		t.Fatal("未知 runtime 应当报错")
	}
	// 路径穿越：清单是本地文件，但手工改过的清单不该能指到 data/tools 之外。
	if _, err := s.toolCommand(toolEntry{Name: "evil", Path: "../etc/passwd"}); err == nil {
		t.Fatal("非法 path 应当报错")
	}
}

func TestToolEntryToRowDefaults(t *testing.T) {
	s := newToolLibServer(t)
	row, err := s.toolEntryToRow(toolEntry{
		Name: "fscan", Path: "fscan", Kind: "binary", Purpose: "内网综合扫描", Version: "2.2.2",
	}, []string{"auto", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if row.Key != "fscan" || row.Kind != "command" || !row.Enabled || row.System {
		t.Fatalf("工具行默认值不对: %+v", row)
	}
	if !strings.Contains(row.Description, "内网综合扫描") || !strings.Contains(row.Description, "2.2.2") {
		t.Fatalf("描述应带用途与版本: %q", row.Description)
	}
	// 清单不写 agents 时默认绑全部 agent —— 否则导入完没有任何 agent 能看见它。
	if len(row.Agents) != 2 {
		t.Fatalf("缺省绑定应为全部 agent: %v", row.Agents)
	}
	if !strings.Contains(string(row.Exec), "{args}") {
		t.Fatalf("exec 应当保留 {args} 占位: %s", row.Exec)
	}
}

func TestResolveZipLayout(t *testing.T) {
	single, err := resolveZipLayout(toolEntry{Name: "fscan"}, []string{"fscan"})
	if err != nil {
		t.Fatal(err)
	}
	if single.Kind != "binary" || single.Path != "fscan/fscan" || single.Entry != "" {
		t.Fatalf("单文件包应落成 binary: %+v", single)
	}

	if _, err := resolveZipLayout(toolEntry{Name: "multi"}, []string{"a.sh", "b.txt"}); err == nil {
		t.Fatal("多文件包缺 entry 应当报错")
	}
	dirTool, err := resolveZipLayout(toolEntry{Name: "multi", Entry: "a.sh"}, []string{"a.sh", "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if dirTool.Kind != "dir" || dirTool.Path != "multi" || dirTool.Entry != "a.sh" {
		t.Fatalf("目录型布局不对: %+v", dirTool)
	}
	if _, err := resolveZipLayout(toolEntry{Name: "multi", Entry: "missing.sh"}, []string{"a.sh"}); err == nil {
		t.Fatal("entry 不在包内应当报错")
	}
	if _, err := resolveZipLayout(toolEntry{Name: "s", Kind: "script", Runtime: "python3"}, []string{"a.py", "b.py"}); err == nil {
		t.Fatal("kind=script 多文件应当报错")
	}
}

func TestToolManifestRoundTrip(t *testing.T) {
	s := newToolLibServer(t)

	m, err := s.loadToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tools) != 0 || m.Version == "" {
		t.Fatalf("首次读取应生成带版本的空骨架: %+v", m)
	}
	if _, err := os.Stat(s.toolsManifestPath()); err != nil {
		t.Fatalf("骨架应当落盘: %v", err)
	}

	if err := appendOrReplaceEntry(m, toolEntry{Name: "fscan", Path: "fscan", Kind: "binary"}); err != nil {
		t.Fatal(err)
	}
	// 同名条目只留一条（再次导入是刷新，不是追加）。
	if err := appendOrReplaceEntry(m, toolEntry{Name: "fscan", Path: "fscan", Kind: "binary", Version: "2.2.2"}); err != nil {
		t.Fatal(err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Version != "2.2.2" {
		t.Fatalf("同名条目应被替换: %+v", m.Tools)
	}
	if err := s.saveToolManifest(m); err != nil {
		t.Fatal(err)
	}

	again, err := s.loadToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Tools) != 1 || again.Tools[0].Version != "2.2.2" {
		t.Fatalf("写回后再读应一致: %+v", again.Tools)
	}
	// 第二次写入会留下上一版备份，手工改坏清单时还有得救。
	if err := s.saveToolManifest(again); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.toolsManifestPath() + toolsLibBackupExt); err != nil {
		t.Fatalf("应保留 manifest.json.bak: %v", err)
	}
}

func TestFindToolEntryByNameOrKey(t *testing.T) {
	m := &toolManifest{Tools: []toolEntry{{Name: "dirsearch-py", Path: "x"}}}
	if i, e := findToolEntry(m, "dirsearch-py"); i != 0 || e == nil {
		t.Fatal("按名称应能定位")
	}
	if i, e := findToolEntry(m, "dirsearch_py"); i != 0 || e == nil {
		t.Fatal("按推导出的 key 也应能定位")
	}
	if _, e := findToolEntry(m, "nope"); e != nil {
		t.Fatal("不存在的名称不应命中")
	}
}

func TestCheckToolLibStates(t *testing.T) {
	s := newToolLibServer(t)
	tools := s.toolsDir()
	if err := os.MkdirAll(filepath.Join(tools, "peass"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "peass", "linpeas.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "fscan"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	goodSum, err := toolFileSHA256(filepath.Join(tools, "fscan"))
	if err != nil {
		t.Fatal(err)
	}

	m, err := s.loadToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	m.Tools = []toolEntry{
		// 摘要对得上
		{Name: "fscan", Path: "fscan", Kind: "binary", SHA256: goodSum},
		// 文件不存在
		{Name: "kscan", Path: "kscan", Kind: "binary"},
		// 摘要不符
		{Name: "nuclei", Path: "fscan", Kind: "binary", SHA256: strings.Repeat("0", 64)},
		// 未钉摘要
		{Name: "gogo", Path: "fscan", Kind: "binary"},
		// 目录型条目：跳过单文件摘要
		{Name: "peass", Path: "peass", Kind: "dir", Entry: "linpeas.sh"},
		// 未知 runtime
		{Name: "nodebased", Path: "fscan", Kind: "binary", Runtime: "cobol"},
	}
	if err := s.saveToolManifest(m); err != nil {
		t.Fatal(err)
	}

	sum := s.checkToolLib()
	if sum.Total != 6 {
		t.Fatalf("条目数: %d", sum.Total)
	}
	if sum.Missing != 1 || sum.Mismatch != 1 || sum.Unpinned != 1 || sum.RuntimeMissing != 1 {
		t.Fatalf("自检计数不对: missing=%d mismatch=%d unpinned=%d runtimeMissing=%d",
			sum.Missing, sum.Mismatch, sum.Unpinned, sum.RuntimeMissing)
	}
	byName := map[string]toolEntryStatus{}
	for _, st := range sum.Items {
		byName[st.Name] = st
	}
	if byName["fscan"].SHA256State != "ok" || !byName["fscan"].Exists {
		t.Fatalf("fscan 应校验通过: %+v", byName["fscan"])
	}
	if byName["peass"].SHA256State != "skipped" || !byName["peass"].Exists {
		t.Fatalf("目录型条目应跳过摘要校验: %+v", byName["peass"])
	}
	if byName["nodebased"].RuntimeOK {
		t.Fatal("未知 runtime 应标记不可用")
	}
	// 自检只报告不阻断：所有条目都必须留在结果里。
	if len(sum.Items) != 6 {
		t.Fatalf("自检结果应包含全部条目: %d", len(sum.Items))
	}
}
