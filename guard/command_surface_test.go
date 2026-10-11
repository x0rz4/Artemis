package guard

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/hook"
)

// 这些用例只碰纯函数与渲染器回调，不需要数据库。

func TestWithCommandInputMergesRenderedCommand(t *testing.T) {
	in := []byte(`{"args":"-u http://t"}`)
	out := withCommandInput(in, `python3 '/data/tools/d.py' '-u http://t'`)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["_command"] != `python3 '/data/tools/d.py' '-u http://t'` {
		t.Fatalf("_command 未合并: %s", out)
	}
	if m["args"] != "-u http://t" {
		t.Fatalf("原参数应保留: %s", out)
	}
	// 已有 _command 不覆盖：渲染器只补空缺，不重写平台的字段。
	keep := []byte(`{"_command":"keep"}`)
	if got := string(withCommandInput(keep, "new")); got != string(keep) {
		t.Fatalf("已有 _command 不应被覆盖: %s", got)
	}
	// 非对象输入原样返回（不是所有工具都用 JSON 对象参数）。
	if got := withCommandInput([]byte(`"just-a-string"`), "x"); string(got) != `"just-a-string"` {
		t.Fatalf("非对象输入应原样返回: %s", got)
	}
	if got := withCommandInput(nil, "x"); len(got) != 0 {
		t.Fatalf("空输入应原样返回: %q", got)
	}
}

func TestCommandSurfaceForShellTools(t *testing.T) {
	g := newGuard(nil)
	cmd, input := g.commandSurface(hook.Event{ToolName: "Bash", Input: []byte(`{"command":"id"}`)})
	if cmd != "id" {
		t.Fatalf("Bash 命令应被抽出: %q", cmd)
	}
	if string(input) != `{"command":"id"}` {
		t.Fatalf("shell 工具的输入不应被改写: %s", input)
	}

	cmd, _ = g.commandSurface(hook.Event{ToolName: "shell_send", Input: []byte(`{"text":"whoami"}`)})
	if cmd != "whoami" {
		t.Fatalf("shell_send 文本应被抽出: %q", cmd)
	}
}

func TestCommandSurfaceForRenderedTool(t *testing.T) {
	// 没接渲染器时保持老行为：记录空命令，不改写输入。
	if cmd, input := newGuard(nil).commandSurface(hook.Event{ToolName: "fscan", Input: []byte(`{"args":"-h"}`)}); cmd != "" || string(input) != `{"args":"-h"}` {
		t.Fatalf("无渲染器时应保持原样: %q %s", cmd, input)
	}

	ic := intercept.New(nil)
	ic.SetCommandRenderer(func(tool string, input []byte) string {
		if tool != "fscan" {
			return ""
		}
		return "fscan -h " + string(input)
	})
	g := newGuard(ic)

	cmd, input := g.commandSurface(hook.Event{ToolName: "fscan", Input: []byte(`{"args":"10.0.0.1"}`)})
	if cmd != `fscan -h {"args":"10.0.0.1"}` {
		t.Fatalf("渲染出的命令应写进审计: %q", cmd)
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["_command"] != cmd {
		t.Fatalf("tool_input 规则应能看到同一条命令: %s", input)
	}

	// 非渲染型工具不受影响（渲染器返回空）。
	if cmd, input := g.commandSurface(hook.Event{ToolName: "Read", Input: []byte(`{"file":"a"}`)}); cmd != "" || string(input) != `{"file":"a"}` {
		t.Fatalf("非渲染型工具应保持原样: %q %s", cmd, input)
	}
}
