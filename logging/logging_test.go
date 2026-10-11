package logging

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// captureSink 记录每条日志的级别、标签与文本，供断言。
type captureSink struct {
	level, tag, text string
	lines            []string
}

func (c *captureSink) Emit(level, tag, text string) {
	c.level, c.tag, c.text = level, tag, text
	c.lines = append(c.lines, text)
}

// withCapture 把日志汇替换成捕获器，测试结束恢复，避免影响其它用例。
func withCapture(t *testing.T) *captureSink {
	t.Helper()
	sink := &captureSink{}
	Setup(sink)
	t.Cleanup(func() {
		mu.Lock()
		logger = slog.New(newTextHandler(stderrSink{}, ""))
		mu.Unlock()
	})
	return sink
}

func TestLevelAndTagComeFromCaller(t *testing.T) {
	sink := withCapture(t)

	// 级别显式给出：消息里带“失败”也不再靠关键词升级为 error。
	Task("deadline", "t-42").Info("收尾完成", "status", "timeout")
	if sink.level != "info" || sink.tag != "deadline" {
		t.Fatalf("级别/标签错误：level=%q tag=%q", sink.level, sink.tag)
	}
	if want := "[deadline] 收尾完成 task=t-42 status=timeout"; sink.text != want {
		t.Fatalf("文本 = %q，期望 %q", sink.text, want)
	}

	Task("deadline", "t-42").Warn("落终态失败", "err", errors.New("conn reset"))
	if sink.level != "warn" {
		t.Fatalf("Warn 级别 = %q", sink.level)
	}
	if want := `[deadline] 落终态失败 task=t-42 err="conn reset"`; sink.text != want {
		t.Fatalf("错误字段应带引号：%q", sink.text)
	}

	Task("deadline", "t-42").Error("收尾中止", "phase", "settle")
	if sink.level != "error" {
		t.Fatalf("Error 级别 = %q", sink.level)
	}
}

func TestEventConstructorsSetIdentifiers(t *testing.T) {
	sink := withCapture(t)

	Intent("worker", "t-7", 12).Info("领取意图")
	if want := "[worker] 领取意图 task=t-7 intent=12"; sink.text != want {
		t.Fatalf("intent 文本 = %q，期望 %q", sink.text, want)
	}

	Tool("activity", "httpx").Warn("写入重试成功", "attempt", 2)
	if want := "[activity] 写入重试成功 tool=httpx attempt=2"; sink.text != want {
		t.Fatalf("tool 文本 = %q，期望 %q", sink.text, want)
	}

	Decision("deny", "bash").Warn("拦截命中", "reason", "删除类接口路径")
	if want := "[decision] 拦截命中 decision=deny tool=bash reason=删除类接口路径"; sink.text != want {
		t.Fatalf("decision 文本 = %q，期望 %q", sink.text, want)
	}

	Decision("timeout", "").Warn("审批超时按兜底动作处理")
	if strings.Contains(sink.text, "tool=") {
		t.Fatalf("空工具名不应产生 tool 字段：%q", sink.text)
	}
}

func TestWithAttrsKeepsTagOutOfBody(t *testing.T) {
	sink := withCapture(t)

	Log().With("tag", "planner", "task", "t-1").With("round", 3).Info("规划完成")
	if want := "[planner] 规划完成 task=t-1 round=3"; sink.text != want {
		t.Fatalf("文本 = %q，期望 %q", sink.text, want)
	}
	if sink.tag != "planner" {
		t.Fatalf("标签被重复写进正文或丢失：tag=%q text=%q", sink.tag, sink.text)
	}
}

func TestQuotingKeepsValuesBoundary(t *testing.T) {
	sink := withCapture(t)

	Log().With("tag", "tool").Info("工具不可用", "reason", "缺少运行时 python3")
	// 含空格的值加引号，读者（与日志页的检索）才能看出值到哪里结束。
	if want := `[tool] 工具不可用 reason="缺少运行时 python3"`; sink.text != want {
		t.Fatalf("文本 = %q，期望 %q", sink.text, want)
	}

	// 不含空白的值不加引号，避免把正常日志刷成转义串。
	Log().With("tag", "tool").Info("已安装", "runtime", "python3")
	if want := "[tool] 已安装 runtime=python3"; sink.text != want {
		t.Fatalf("文本 = %q，期望 %q", sink.text, want)
	}
}

func TestSetupNilKeepsWorking(t *testing.T) {
	sink := withCapture(t)
	Setup(nil) // 不得 panic，也不得清空已安装的日志汇

	Log().With("tag", "task").Info("照常记录")
	if sink.level != "info" {
		t.Fatalf("Setup(nil) 后日志丢失：level=%q", sink.level)
	}
}

// resetLevel 把阈值恢复成缺省 info，避免用例之间互相影响。
func resetLevel(t *testing.T) {
	t.Helper()
	t.Setenv(envLogLevel, "")
	setMinFromEnv()
	t.Cleanup(func() {
		t.Setenv(envLogLevel, "")
		setMinFromEnv()
	})
}

func TestLogLevelFiltersStructuredLogs(t *testing.T) {
	sink := withCapture(t)
	resetLevel(t)
	t.Setenv(envLogLevel, "warn")
	setMinFromEnv()

	Log().With("tag", "task").Info("被过滤")
	Log().With("tag", "task").Warn("保留")
	if sink.text != "[task] 保留" || sink.level != "warn" {
		t.Fatalf("warn 阈值下应只留 warn：level=%q text=%q", sink.level, sink.text)
	}
	if !Allows("warn") || !Allows("error") || Allows("info") {
		t.Fatalf("Allows 判定错误：warn=%v error=%v info=%v", Allows("warn"), Allows("error"), Allows("info"))
	}
}

func TestLogLevelAcceptsDebugAndFallsBackOnUnknown(t *testing.T) {
	sink := withCapture(t)
	resetLevel(t)

	t.Setenv(envLogLevel, "DEBUG")
	setMinFromEnv()
	if MinLevel() != "debug" {
		t.Fatalf("级别名应大小写不敏感，得到 %q", MinLevel())
	}
	Log().With("tag", "task").Info("debug 阈值下保留")
	if sink.text == "" {
		t.Fatal("debug 阈值下 info 不应被过滤")
	}

	t.Setenv(envLogLevel, "verbose")
	setMinFromEnv()
	if MinLevel() != "info" {
		t.Fatalf("无效级别应回落 info，得到 %q", MinLevel())
	}
	// 认不出的级别（例如历史行分类异常）不丢，避免把诊断行静默吃掉。
	if !Allows("info") || !Allows("没见过") {
		t.Fatal("回落 info 后 info 与未知级别都应放行")
	}
}
