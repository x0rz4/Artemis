package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// 工具库自检：只产出报告，不做任何阻断。
//
// 与 artex-ymh 的启动自检保持一致的口径——文件缺失、sha256 不符、runtime 没装都只标
// 在 problem 里，既不阻止启动也不阻止其它条目导入。武器库不全时项目仍然要能跑。
// 真正的执行风险由拦截规则与人工审批兜底，不靠"清单没配好"来拦。

// toolEntryStatus 是一条清单条目的自检结果。
type toolEntryStatus struct {
	Name        string `json:"name"`
	ToolKey     string `json:"tool_key"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Runtime     string `json:"runtime"`
	Resolved    string `json:"resolved,omitempty"` // 实际检查的绝对路径
	Exists      bool   `json:"exists"`
	IsDir       bool   `json:"is_dir,omitempty"`
	Size        int64  `json:"size,omitempty"`
	SHA256State string `json:"sha256_state"` // ok | mismatch | unpinned | missing | skipped
	RuntimeOK   bool   `json:"runtime_ok"`
	RuntimePath string `json:"runtime_path,omitempty"`
	Problem     string `json:"problem,omitempty"`
}

// toolLibSummary 是整库自检摘要（页面顶部展示，日志也打这一份）。
type toolLibSummary struct {
	Total          int               `json:"total"`
	Missing        int               `json:"missing"`
	Mismatch       int               `json:"mismatch"`
	Unpinned       int               `json:"unpinned"`
	RuntimeMissing int               `json:"runtime_missing"`
	Items          []toolEntryStatus `json:"items"`
}

// toolRuntimeBins 是各 runtime 依次探测的解释器候选。runtime 留空 = 原生二进制，
// 不探测任何解释器。
var toolRuntimeBins = map[string][]string{
	"python3": {"python3", "python"},
	"python":  {"python3", "python"},
	"node":    {"node"},
	"php":     {"php"},
	"java":    {"java"},
}

// detectRuntime 用 LookPath 找一个 runtime 的解释器绝对路径（"" = 没装或不认识）。
func detectRuntime(name string) string {
	for _, c := range toolRuntimeBins[strings.ToLower(strings.TrimSpace(name))] {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// runtimeProbe 在一次自检内缓存 runtime 解析结果：python 走用户配置的解释器（要查库），
// 不缓存的话每个条目都要打一次数据库。
type runtimeProbe struct{ cache map[string]string }

func newRuntimeProbe() *runtimeProbe { return &runtimeProbe{cache: map[string]string{}} }

// resolve 返回 runtime 对应的解释器路径。空 runtime（原生二进制）返回 ""，
// 且不写入 problem —— 调用方要区分"不需要解释器"和"没找到解释器"。
func (p *runtimeProbe) resolve(s *Server, name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	if p == nil {
		return s.resolveRuntimeOnce(n)
	}
	if v, ok := p.cache[n]; ok {
		return v
	}
	v := s.resolveRuntimeOnce(n)
	p.cache[n] = v
	return v
}

// resolveRuntimeOnce 解析单个 runtime：python 优先用系统配置的解释器（与自定义脚本
// 工具共用同一设置），其余直接探测。
func (s *Server) resolveRuntimeOnce(name string) string {
	switch name {
	case "python3", "python":
		// 配置里可能存着用户指定的解释器；数据库不可用时退回纯探测（自检不该因为
		// 读不到设置就报"runtime 未安装"）。
		if s.m != nil && s.m.pg != nil {
			if p := s.pythonInterpreter(); p != "" {
				return p
			}
		}
		return detectRuntime("python3")
	default:
		return detectRuntime(name)
	}
}

// toolFileSHA256 流式计算文件摘要（大二进制不进内存）。
func toolFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkToolLib 遍历清单做一次完整自检。
func (s *Server) checkToolLib() toolLibSummary {
	sum := toolLibSummary{Items: []toolEntryStatus{}}
	m, err := s.loadToolManifest()
	if err != nil {
		return sum
	}
	probe := newRuntimeProbe()
	for _, e := range m.Tools {
		st := s.checkToolEntry(probe, e)
		switch st.SHA256State {
		case "missing":
			sum.Missing++
		case "mismatch":
			sum.Mismatch++
		case "unpinned":
			sum.Unpinned++
		}
		if strings.TrimSpace(e.Runtime) != "" && !st.RuntimeOK {
			sum.RuntimeMissing++
		}
		sum.Items = append(sum.Items, st)
	}
	sum.Total = len(sum.Items)
	return sum
}

// checkToolEntry 检查一条条目：文件存在性、sha256、runtime 可用性。
func (s *Server) checkToolEntry(probe *runtimeProbe, e toolEntry) toolEntryStatus {
	key, _ := entryKey(e)
	st := toolEntryStatus{
		Name: e.Name, ToolKey: key, Path: e.Path,
		Kind: e.Kind, Runtime: e.Runtime,
		SHA256State: "skipped", RuntimeOK: true,
	}

	clean, msg := toolEntryPath(e)
	if msg != nil {
		st.SHA256State = "missing"
		st.Problem = msg.Error()
		return st
	}
	target := filepath.Join(s.toolsDir(), filepath.FromSlash(clean))
	if entry := strings.TrimSpace(e.Entry); entry != "" {
		if eclean, emsg := skillRelPath(entry); emsg == "" {
			target = filepath.Join(target, filepath.FromSlash(eclean))
		}
	}
	st.Resolved = filepath.ToSlash(target)

	info, err := os.Stat(target)
	switch {
	case err != nil:
		st.SHA256State = "missing"
		st.Problem = "文件不存在"
	default:
		st.Exists = true
		st.IsDir = info.IsDir()
		if !info.IsDir() {
			st.Size = info.Size()
		}
		switch {
		case info.IsDir():
			// 目录型工具（peass、impacket 之类）不做单文件摘要。
			st.SHA256State = "skipped"
		case strings.TrimSpace(e.SHA256) == "":
			st.SHA256State = "unpinned"
			st.Problem = "未钉 sha256（可运行，但不校验完整性）"
		default:
			got, herr := toolFileSHA256(target)
			if herr != nil {
				st.SHA256State = "mismatch"
				st.Problem = "计算摘要失败: " + herr.Error()
			} else if !strings.EqualFold(got, strings.TrimSpace(e.SHA256)) {
				st.SHA256State = "mismatch"
				st.Problem = "sha256 与清单不符"
			} else {
				st.SHA256State = "ok"
			}
		}
	}

	if runtime := strings.ToLower(strings.TrimSpace(e.Runtime)); runtime != "" {
		if _, known := toolRuntimeBins[runtime]; !known {
			st.RuntimeOK = false
			st.Problem = joinProblem(st.Problem, "不认识的 runtime: "+e.Runtime)
		} else if p := probe.resolve(s, runtime); p == "" {
			st.RuntimeOK = false
			st.Problem = joinProblem(st.Problem, "runtime 未安装: "+e.Runtime)
		} else {
			st.RuntimePath = p
		}
	}
	return st
}

// joinProblem 把新问题追加到已有问题上，用「；」分隔（自检要一次列全，不互相覆盖）。
func joinProblem(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "；" + add
}
