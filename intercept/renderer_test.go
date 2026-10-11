package intercept

import "testing"

// 渲染器是纯回调，用例不需要数据库。

func TestRenderCommandWithoutRenderer(t *testing.T) {
	ic := New(nil)
	if got := ic.RenderCommand("Bash", []byte("{}")); got != "" {
		t.Fatalf("未接线时应返回空: %q", got)
	}
	var nilIC *Interceptor
	if got := nilIC.RenderCommand("Bash", []byte("{}")); got != "" {
		t.Fatalf("nil 拦截器应返回空: %q", got)
	}
	// nil 接收者的 Set 不应 panic（Manager 里拦截器可能还没建好）。
	nilIC.SetCommandRenderer(func(string, []byte) string { return "x" })
	nilIC.SetReviewer(nil)
}

func TestRenderCommandUsesRenderer(t *testing.T) {
	ic := New(nil)
	ic.SetCommandRenderer(func(tool string, input []byte) string {
		if tool == "fscan" {
			return "fscan -h " + string(input)
		}
		return ""
	})
	if got := ic.RenderCommand("fscan", []byte("10.0.0.1")); got != "fscan -h 10.0.0.1" {
		t.Fatalf("渲染结果不对: %q", got)
	}
	if got := ic.RenderCommand("Read", []byte("{}")); got != "" {
		t.Fatalf("非渲染型工具应返回空: %q", got)
	}
	// 传 nil 关掉渲染器后必须回到空结果，否则 guard 可能拿着过期模板去审计。
	ic.SetCommandRenderer(nil)
	if got := ic.RenderCommand("fscan", nil); got != "" {
		t.Fatalf("清空渲染器后应返回空: %q", got)
	}
}
