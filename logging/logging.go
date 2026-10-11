// Package logging 是后端的结构化日志入口：级别与标签由调用方显式给出，不再让
// 日志汇靠消息里的关键词猜（旧行为见 server/logsink.go 的 levelOf）。
//
// 输出保持「[tag] 消息 key=value …」单行文本，而不是 JSON：「系统 → 日志」页面与
// server/logsink.go 都按文本行消费，换格式会让现有分级与标签展示整体失效。
//
// 事件字段约定见 spec/PROJECT.md：tag 标出来源（task/deadline/worker/planner/
// tool/decision 等），task、intent、tool、decision 是关联标识，err 是错误。
// 历史调用点仍用标准库 log，级别靠关键词猜，属过渡状态。
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// tagAttr 是标签字段名：With("tag", "worker") 之后整条日志按该标签归类。
const tagAttr = "tag"

// Sink 是日志汇。server 的日志捕获器实现它，把每条日志同时送去 stderr、内存环、
// 数据库与 SSE 订阅者。
type Sink interface {
	Emit(level, tag, text string)
}

// stderrSink 是未安装日志汇时的兜底输出，避免单测里日志凭空消失。
type stderrSink struct{}

func (stderrSink) Emit(_ string, _ string, text string) {
	fmt.Fprintln(os.Stderr, text)
}

var (
	mu     sync.RWMutex
	logger = slog.New(newTextHandler(stderrSink{}, ""))
)

// Setup 安装日志汇，只在启动时调用一次（cmd/artex 经 server.StartLogCapture 调用）。
func Setup(s Sink) {
	if s == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	logger = slog.New(newTextHandler(s, ""))
}

// Log 返回根 logger。
func Log() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return logger
}

// Task 返回带任务标识的 logger；tag 标出来源，例如 deadline、task、worker。
func Task(tag, taskID string) *slog.Logger {
	return Log().With(tagAttr, tag, "task", taskID)
}

// Intent 返回带任务与意图标识的 logger。
func Intent(tag, taskID string, intentID int64) *slog.Logger {
	return Log().With(tagAttr, tag, "task", taskID, "intent", intentID)
}

// Tool 返回带工具名的 logger。
func Tool(tag, tool string) *slog.Logger {
	return Log().With(tagAttr, tag, "tool", tool)
}

// Decision 返回决策事件的 logger；action 取 allow/deny/ask/timeout/model_fallback
// 等值，tool 可为空。
func Decision(action, tool string) *slog.Logger {
	args := []any{tagAttr, "decision", "decision", action}
	if tool != "" {
		args = append(args, "tool", tool)
	}
	return Log().With(args...)
}

// textHandler 把 slog 记录渲染成单行文本交给日志汇。
type textHandler struct {
	sink  Sink
	tag   string
	attrs []slog.Attr
}

func newTextHandler(s Sink, tag string) *textHandler {
	return &textHandler{sink: s, tag: tag}
}

// Enabled 目前只放过 info 及以上：日志级别开关尚未引入（见 PROJECT.md 的观测约定），
// 先把 debug 挡在门外。
func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }

// WithAttrs 累积字段；其中 tag 只影响标签归类，不重复出现在正文里。
func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	all := append(append([]slog.Attr{}, h.attrs...), attrs...)
	tag, kept := splitTag(all)
	if tag == "" {
		tag = h.tag
	}
	return &textHandler{sink: h.sink, tag: tag, attrs: kept}
}

// WithGroup 忽略分组名：文本行没有嵌套语义，硬套只会让字段名带上无法阅读的前缀。
func (h *textHandler) WithGroup(_ string) slog.Handler { return h }

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	tag := h.tag
	fields := make([]string, 0, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		fields = append(fields, formatAttr(a))
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == tagAttr {
			tag = valueString(a.Value)
			return true
		}
		fields = append(fields, formatAttr(a))
		return true
	})

	text := r.Message
	if tag != "" {
		text = "[" + tag + "] " + text
	}
	if len(fields) > 0 {
		text += " " + strings.Join(fields, " ")
	}
	h.sink.Emit(levelName(r.Level), tag, text)
	return nil
}

// splitTag 摘出 tag 字段，其余字段保持原顺序。
func splitTag(attrs []slog.Attr) (string, []slog.Attr) {
	tag := ""
	var kept []slog.Attr
	for _, a := range attrs {
		if a.Key == tagAttr {
			tag = valueString(a.Value)
			continue
		}
		kept = append(kept, a)
	}
	return tag, kept
}

// formatAttr 渲染一个字段。含空白的值加引号，保证「key=value」仍然可被逐对切分。
func formatAttr(a slog.Attr) string {
	return a.Key + "=" + valueString(a.Value)
}

func valueString(v slog.Value) string {
	v = v.Resolve()
	if err, ok := v.Any().(error); ok {
		return quoteIfNeeded(err.Error())
	}
	switch v.Kind() {
	case slog.KindString:
		return quoteIfNeeded(v.String())
	case slog.KindTime:
		return v.Time().Format("2006-01-02 15:04:05")
	default:
		return quoteIfNeeded(fmt.Sprint(v.Any()))
	}
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}

// levelName 把 slog 级别映射成日志汇与前端使用的三档字符串。
func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	default:
		return "info"
	}
}
