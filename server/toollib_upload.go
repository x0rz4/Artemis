package server

import (
	"archive/zip"
	"compress/bzip2"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// 工具安装（上传与多源同步共用）。
//
// 落盘位置与清单条目一一对应：data/tools/<name>（单文件）或 data/tools/<name>/...（zip）。
// 安装成功后调用方负责写清单与注册工具行，Agent 无需等待即可调用——但工具调用依旧要过
// 拦截规则与人工审批，安装动作本身不授予任何额外执行权限。
//
// 元数据优先级：调用方给的期望值（表单 / 源条目）> 包内 manifest-entry.json > 按内容推断。

const (
	// 单个上传文件上限。渗透工具二进制常有几十 MB，给足余量；上限同时约束 zip 内单个文件。
	maxToolUploadBytes = 512 << 20
	maxToolZipEntries  = 4000
	maxToolZipTotal    = 1 << 30
	// toolZipMetaName 是 zip 内可选元数据文件名（与 artex-ymh 的约定一致）。
	toolZipMetaName = "manifest-entry.json"
)

// validToolRuntime 报告 runtime 是否为受支持的值（"" = 原生二进制）。
// 在安装阶段就拦住错值：等导入时才发现的话，文件已经落盘、清单也写进去了。
func validToolRuntime(rt string) bool {
	switch strings.ToLower(strings.TrimSpace(rt)) {
	case "", "python3", "python", "node", "php", "java":
		return true
	}
	return false
}

// validToolName 校验工具名（作为 data/tools 下的一级目录/文件名使用）。
func validToolName(name string) string {
	if name == "" {
		return "名称为空"
	}
	clean, msg := skillRelPath(name)
	if msg != "" {
		return msg
	}
	if clean != name || strings.Contains(clean, "/") {
		return "名称不能包含路径分隔符"
	}
	return ""
}

// overlayToolEntry 用非空字段覆盖基础条目（"调用方 > 包内元数据 > 内容推断"）。
func overlayToolEntry(base *toolEntry, over toolEntry) {
	if over.Key != "" {
		base.Key = over.Key
	}
	if over.Purpose != "" {
		base.Purpose = over.Purpose
	}
	if over.Version != "" {
		base.Version = over.Version
	}
	if over.Platform != "" {
		base.Platform = over.Platform
	}
	if over.SHA256 != "" {
		base.SHA256 = over.SHA256
	}
	if over.URL != "" {
		base.URL = over.URL
	}
	if over.Kind != "" {
		base.Kind = over.Kind
	}
	if over.Runtime != "" {
		base.Runtime = over.Runtime
	}
	if over.Entry != "" {
		base.Entry = over.Entry
	}
	if over.Category != "" {
		base.Category = over.Category
	}
	if over.Source != "" {
		base.Source = over.Source
	}
	if len(over.Schema) > 0 {
		base.Schema = over.Schema
	}
	if len(over.Agents) > 0 {
		base.Agents = over.Agents
	}
	if over.Enabled != nil {
		base.Enabled = over.Enabled
	}
	if over.Deferred {
		base.Deferred = true
	}
}

// swapDir 把 stage 目录落地到 target。目标已存在且允许覆盖时，先把旧目录改名到
// <target>.old-sync，新目录落地失败就把它改回来 —— 覆盖更新不该以"原有工具消失"为代价。
func swapDir(src, target string, overwrite bool) error {
	if _, err := os.Stat(target); err == nil {
		if !overwrite {
			return fmt.Errorf("已存在同名工具 %s", filepath.Base(target))
		}
		old := target + ".old-sync"
		_ = os.RemoveAll(old)
		if err := os.Rename(target, old); err != nil {
			return err
		}
		if err := os.Rename(src, target); err != nil {
			_ = os.Rename(old, target)
			return err
		}
		_ = os.RemoveAll(old)
		return nil
	}
	return os.Rename(src, target)
}

// openToolZip 打开磁盘上的 zip 并注册额外解压器（与 skill 上传同一套：bzip2 / zstd）。
// 用 zip.OpenReader 而不是读进内存：工具包可能有几百 MB。
func openToolZip(p string) (*zip.ReadCloser, error) {
	rc, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("无法解析压缩包(需为 zip 格式)：%w", err)
	}
	rc.RegisterDecompressor(zipMethodBzip2, func(r io.Reader) io.ReadCloser {
		return io.NopCloser(bzip2.NewReader(r))
	})
	zdec := zstd.ZipDecompressor(zstd.WithDecoderConcurrency(1))
	rc.RegisterDecompressor(zipMethodZstd, zdec)
	rc.RegisterDecompressor(zipMethodZstdPKW, zdec)
	return rc, nil
}

// isZipFile 按魔数判断（不看扩展名，避免 .zip 改名/二进制恰好同名的误判）。
func isZipFile(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var sig [4]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil {
		return false
	}
	return sig[0] == 'P' && sig[1] == 'K' && (sig[2] == 3 || sig[2] == 5 || sig[2] == 7)
}

// extractToolZip 把 zip 解压到 dst（调用方保证 dst 不存在或已清空）。
// 返回解压出来的相对文件名（正斜杠）。zip slip 由 skillRelPath 逐条拦截。
func extractToolZip(zr *zip.Reader, dst string) ([]string, error) {
	entries := skillZipEntries(zr)
	if err := checkSkillZipMethods(entries); err != nil {
		return nil, err
	}
	if len(entries) > maxToolZipEntries {
		return nil, fmt.Errorf("压缩包文件过多（上限 %d）", maxToolZipEntries)
	}
	files := make([]string, 0, len(entries))
	var total int64
	for _, e := range entries {
		rel, msg := skillRelPath(e.name)
		if msg != "" {
			return nil, fmt.Errorf("压缩包含非法路径 %s：%s", e.name, msg)
		}
		if e.f.UncompressedSize64 > maxToolUploadBytes {
			return nil, fmt.Errorf("包内文件过大：%s", rel)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		rc, err := e.f.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return nil, err
		}
		n, err := io.Copy(out, io.LimitReader(rc, maxToolUploadBytes+1))
		out.Close()
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += n
		if total > maxToolZipTotal {
			return nil, errors.New("压缩包解压后过大")
		}
		files = append(files, rel)
	}
	if len(files) == 0 {
		return nil, errors.New("压缩包里没有文件")
	}
	return files, nil
}

// readToolZipMeta 读取 zip 内的 manifest-entry.json（可选）。
func readToolZipMeta(zr *zip.Reader) (*toolEntry, error) {
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Base(zipEntryName(f)), toolZipMetaName) {
			continue
		}
		b, err := readZipEntry(f)
		if err != nil {
			return nil, err
		}
		var e toolEntry
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, fmt.Errorf("解析 %s 失败：%w", toolZipMetaName, err)
		}
		return &e, nil
	}
	return nil, nil
}

// stripCommonTopDir 去掉所有条目共同的一级目录（打包整目录时常见），返回去掉的目录名。
func stripCommonTopDir(files []string) (string, bool) {
	top := ""
	for _, f := range files {
		i := strings.Index(f, "/")
		if i <= 0 {
			return "", false // 根下就有文件 → 没有共同的一级目录
		}
		if cur := f[:i]; top == "" {
			top = cur
		} else if cur != top {
			return "", false
		}
	}
	return top, top != ""
}

// resolveZipLayout 决定 zip 安装后的 path/kind/entry：
//   - 包内只有一个文件 → 该文件就是工具（kind 缺省 binary），path = <name>/<file>；
//   - 多个文件 → 目录型工具，必须有 entry（调用方或 manifest-entry.json），path = <name>。
func resolveZipLayout(e toolEntry, files []string) (toolEntry, error) {
	if !validToolRuntime(e.Runtime) {
		return e, fmt.Errorf("不支持的 runtime %q（可用：python3/node/php/java，原生二进制留空）", e.Runtime)
	}
	if e.Kind == "" {
		if len(files) == 1 && strings.TrimSpace(e.Entry) == "" {
			e.Kind = "binary"
		} else {
			e.Kind = "dir"
		}
	}
	if e.Kind == "dir" {
		if strings.TrimSpace(e.Entry) == "" {
			return e, errors.New("多文件压缩包需要入口文件：请在表单里填 entry，或在包内放 manifest-entry.json（也可用该文件声明 runtime/kind）")
		}
		entryClean, msg := skillRelPath(e.Entry)
		if msg != "" {
			return e, fmt.Errorf("entry %q 非法：%s", e.Entry, msg)
		}
		found := false
		for _, f := range files {
			if f == entryClean {
				found = true
				break
			}
		}
		if !found {
			return e, fmt.Errorf("压缩包里没有入口文件 %s", entryClean)
		}
		e.Entry = entryClean
		e.Path = e.Name
		return e, nil
	}
	if len(files) != 1 {
		return e, fmt.Errorf("kind=%s 只接受单文件，当前压缩包有 %d 个文件", e.Kind, len(files))
	}
	e.Path = e.Name + "/" + files[0]
	e.Entry = ""
	return e, nil
}

// chmodToolFile 给原生可执行文件补执行位（脚本/JAR 由解释器读，保持默认权限）。
func chmodToolFile(p, kind string) error {
	if kind == "script" || kind == "jar" {
		return nil
	}
	return os.Chmod(p, 0o755)
}

// splitAgents 解析表单里的逗号分隔 agent 列表。
func splitAgents(v string) []string {
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---------- 安装器（上传与同步共用） ----------

// installToolFile 安装单文件工具（二进制 / 脚本 / JAR）。
func (s *Server) installToolFile(tmpName, name string, want toolEntry, overwrite bool) (toolEntry, error) {
	if !validToolRuntime(want.Runtime) {
		return toolEntry{}, fmt.Errorf("不支持的 runtime %q（可用：python3/node/php/java，原生二进制留空）", want.Runtime)
	}
	kind := strings.ToLower(strings.TrimSpace(want.Kind))
	if kind == "" {
		kind = "binary"
	}
	switch kind {
	case "binary", "script", "jar":
	default:
		return toolEntry{}, fmt.Errorf("单文件安装的 kind 只能是 binary/script/jar（当前 %q）", kind)
	}
	target := filepath.Join(s.toolsDir(), name)
	if _, err := os.Stat(target); err == nil && !overwrite {
		return toolEntry{}, fmt.Errorf("已存在同名工具 %s（如需覆盖请确认后重试）", name)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return toolEntry{}, err
	}
	if err := chmodToolFile(target, kind); err != nil {
		return toolEntry{}, err
	}
	sum, err := toolFileSHA256(target)
	if err != nil {
		return toolEntry{}, err
	}
	e := toolEntry{Name: name, Source: "uploaded"}
	overlayToolEntry(&e, want)
	e.Path = name
	e.Entry = ""
	e.Kind = kind
	if e.SHA256 == "" {
		e.SHA256 = sum
	}
	return e, nil
}

// installToolZip 安装已落盘的 zip 包到 data/tools/<name>。
func (s *Server) installToolZip(tmpName, name string, want toolEntry, overwrite bool) (toolEntry, error) {
	zr, err := openToolZip(tmpName)
	if err != nil {
		return toolEntry{}, err
	}
	defer zr.Close()

	meta, err := readToolZipMeta(&zr.Reader)
	if err != nil {
		return toolEntry{}, err
	}

	stage, err := os.MkdirTemp(s.toolsDir(), ".extract-*")
	if err != nil {
		return toolEntry{}, err
	}
	defer os.RemoveAll(stage) // 落地成功后 stage 里的内容已被 rename 走
	files, err := extractToolZip(&zr.Reader, stage)
	if err != nil {
		return toolEntry{}, err
	}

	// 打包整目录时（fscan.zip 里是 fscan/fscan）去掉共同的一级目录，直接把那一层
	// rename 成安装目录：往父目录里 rename 同名目录在多数平台会失败，所以不动父目录。
	src := stage
	if top, ok := stripCommonTopDir(files); ok {
		src = filepath.Join(stage, top)
		stripped := make([]string, 0, len(files))
		for _, f := range files {
			stripped = append(stripped, strings.TrimPrefix(f, top+"/"))
		}
		files = stripped
	}

	// 元数据：包内声明 > 调用方覆盖。
	e := toolEntry{Name: name, Source: "uploaded"}
	if meta != nil {
		e = *meta
		e.Name = name
		if e.Source == "" {
			e.Source = "uploaded"
		}
	}
	overlayToolEntry(&e, want)

	entry, err := resolveZipLayout(e, files)
	if err != nil {
		return toolEntry{}, err
	}

	target := filepath.Join(s.toolsDir(), name)
	if err := swapDir(src, target, overwrite); err != nil {
		return toolEntry{}, fmt.Errorf("安装失败：%w", err)
	}
	if entry.Kind != "dir" {
		// 单文件包：原生可执行文件补执行位（脚本/JAR 交给解释器，不必可执行），并钉摘要。
		abs := filepath.Join(s.toolsDir(), filepath.FromSlash(entry.Path))
		_ = chmodToolFile(abs, entry.Kind)
		if sum, herr := toolFileSHA256(abs); herr == nil {
			entry.SHA256 = sum
		}
	}
	return entry, nil
}

// ---------- 上传接口 ----------

// tlUpload 安装一个工具，并立即注册成可调用的工具行。
func (s *Server) tlUpload(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if !libTryLock(&s.toolLibMu) {
		writeErr(w, 409, "工具库已有写操作在进行中，请稍后重试")
		return
	}
	defer s.toolLibMu.Unlock()

	r.Body = http.MaxBytesReader(w, r.Body, maxToolUploadBytes+1<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少上传文件(表单字段 file)或超出大小限制")
		return
	}
	defer file.Close()

	dir := s.toolsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 先落到同目录的临时文件：后面按磁盘路径解析 zip / 计算摘要，也保证 rename 是同盘操作。
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeErr(w, 400, "读取上传内容失败："+err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		base := path.Base(filepath.ToSlash(hdr.Filename))
		name = strings.TrimSuffix(base, path.Ext(base))
	}
	if msg := validToolName(name); msg != "" {
		writeErr(w, 400, "工具名称无效："+msg)
		return
	}
	if !validToolRuntime(r.FormValue("runtime")) {
		writeErr(w, 400, fmt.Sprintf("不支持的 runtime %q（可用：python3/node/php/java，原生二进制留空）", r.FormValue("runtime")))
		return
	}
	want := toolEntry{
		Name: name, Source: "uploaded",
		Kind:    strings.ToLower(strings.TrimSpace(r.FormValue("kind"))),
		Runtime: r.FormValue("runtime"), Entry: r.FormValue("entry"),
		Purpose: r.FormValue("purpose"), Version: r.FormValue("version"),
		Agents: splitAgents(r.FormValue("agents")),
	}
	overwrite := r.URL.Query().Get("overwrite") == "true"

	// 覆盖前留一份快照：上传是唯一由人手误触就能顶掉现有工具的入口，回滚得有个依据。
	if overwrite {
		if _, serr := takeSnapshot(s.toolsDir(), "upload:"+name, []string{name}); serr != nil {
			writeErr(w, 500, "快照失败，已放弃覆盖："+serr.Error())
			return
		}
	}

	var entry toolEntry
	if isZipFile(tmpName) {
		entry, err = s.installToolZip(tmpName, name, want, overwrite)
	} else {
		entry, err = s.installToolFile(tmpName, name, want, overwrite)
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	m, err := s.loadToolManifest()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := appendOrReplaceEntry(m, entry); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.saveToolManifest(m); err != nil {
		writeErr(w, 500, "写入工具清单失败："+err.Error())
		return
	}

	// 上传即注册：清单条目的 agents/enabled 优先，缺省绑全部 agent（见 toolEntryToRow）。
	row, err := s.toolEntryToRow(entry, s.allAgentKeys())
	if err != nil {
		writeErr(w, 500, "清单已写入，但注册工具失败："+err.Error())
		return
	}
	created, err := pg.UpsertLibraryTool(row, entry.overwriteBinding())
	if err != nil {
		writeErr(w, 500, "清单已写入，但注册工具失败："+err.Error())
		return
	}
	s.invalidateCommandTemplates()
	writeJSON(w, 201, map[string]any{
		"name": entry.Name, "key": row.Key, "path": entry.Path, "kind": entry.Kind,
		"runtime": entry.Runtime, "entry": entry.Entry, "created": created,
	})
}
