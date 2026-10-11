package server

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/Autumn-27/artex/intercept"
)

// 自定义 command 工具的命令行渲染。
//
// 背景：guard 只在工具名为 Bash/shell_open/shell_send 时抽取命令文本，于是 kind=command
// 的自定义工具（页面上建的、以及工具库导入的）在审计里 command 字段为空，tool_input
// 拦截规则也看不到真正要执行的命令行——等于给自己留了个绕过拦截的口子。
//
// guard 在每次工具调用时都会问一次「这条调用要跑什么命令」，所以这里不能每次查库：
// 用一份 key → 命令模板的内存缓存，30 秒 TTL 兜底，工具 CRUD 与导入路径上主动失效。

const commandTplTTL = 30 * time.Second

// commandTplCache 缓存 kind=command 自定义工具的 exec.command 模板。
type commandTplCache struct {
	mu    sync.Mutex
	at    time.Time
	byKey map[string]string
}

var cmdTplCache = &commandTplCache{}

// invalidateCommandTemplates 丢弃缓存（自定义工具增删改、工具库导入后调用）。
func (s *Server) invalidateCommandTemplates() {
	cmdTplCache.mu.Lock()
	cmdTplCache.byKey = nil
	cmdTplCache.at = time.Time{}
	cmdTplCache.mu.Unlock()
}

// commandTemplate 返回某个自定义 command 工具的命令模板（ok=false 表示不是这类工具）。
func (s *Server) commandTemplate(toolName string) (string, bool) {
	if s == nil || s.m == nil || s.m.pg == nil || toolName == "" {
		return "", false
	}
	cmdTplCache.mu.Lock()
	defer cmdTplCache.mu.Unlock()
	if cmdTplCache.byKey == nil || time.Since(cmdTplCache.at) > commandTplTTL {
		rows, err := s.m.pg.ListCustomTools()
		if err != nil {
			// 查库失败就当作"没有这类工具"：审计少一条 command 而已，不能影响调用本身。
			return "", false
		}
		m := make(map[string]string, len(rows))
		for _, t := range rows {
			if t.Kind != "command" {
				continue
			}
			var spec commandExec
			if json.Unmarshal(t.Exec, &spec) != nil {
				continue
			}
			if tpl := spec.Command; tpl != "" {
				m[t.Key] = tpl
			}
		}
		cmdTplCache.byKey = m
		cmdTplCache.at = time.Now()
	}
	tpl, ok := cmdTplCache.byKey[toolName]
	return tpl, ok
}

// renderCustomToolCommand 渲染一次调用的真实命令行：与 runCommandTool 用同一套
// renderTemplate + shellQuote，所以审计里看到的就是将要执行的字符串。
func (s *Server) renderCustomToolCommand(toolName string, input []byte) string {
	tpl, ok := s.commandTemplate(toolName)
	if !ok {
		return ""
	}
	var params map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &params)
	}
	if params == nil {
		params = map[string]any{}
	}
	return renderTemplate(tpl, params, shellQuote)
}

// wireCommandRenderer 把渲染器接到拦截器上（审计 + 规则匹配 + 审批记录共用一条输入）。
func (s *Server) wireCommandRenderer() {
	if s == nil || s.m == nil || s.m.interceptor == nil {
		return
	}
	s.m.interceptor.SetCommandRenderer(s.renderCustomToolCommand)
}

// 编译期断言：渲染器签名与 intercept.CommandRenderer 一致。
var _ intercept.CommandRenderer = (&Server{}).renderCustomToolCommand
