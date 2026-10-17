package secret

import (
	"os"
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", MaskedPrefix},
		{"abc", MaskedPrefix},
		{"abcdef", MaskedPrefix}, // 恰好 6 位：不暴露任何字符
		{"abcdefg", MaskedPrefix + ":…bcdefg"},
		{"sk-live-0123456789", MaskedPrefix + ":…456789"},
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskNeverContainsWholeShortValue(t *testing.T) {
	const short = "s3cr3t"
	if strings.Contains(Mask(short), short) {
		t.Fatalf("Mask(%q) leaks the value: %q", short, Mask(short))
	}
}

func TestFingerprintDistinguishesKind(t *testing.T) {
	a := Fingerprint(KindPassword, "same")
	b := Fingerprint(KindAPIKey, "same")
	if a == b {
		t.Fatal("fingerprint ignored kind")
	}
	if a != Fingerprint(KindPassword, "same") {
		t.Fatal("fingerprint is not deterministic")
	}
	if a == Fingerprint(KindPassword, "same ") {
		t.Fatal("fingerprint ignored a trailing byte")
	}
	// 分隔符必须阻止拼接碰撞。
	if Fingerprint("a", "b") == Fingerprint("ab", "") {
		t.Fatal("fingerprint concatenates without separator")
	}
}

func TestNormalizeKind(t *testing.T) {
	if got, err := NormalizeKind(""); err != nil || got != KindOther {
		t.Fatalf("NormalizeKind(\"\") = %q, %v", got, err)
	}
	if got, err := NormalizeKind(" token "); err != nil || got != KindToken {
		t.Fatalf("NormalizeKind(token) = %q, %v", got, err)
	}
	if _, err := NormalizeKind("bogus"); err == nil {
		t.Fatal("NormalizeKind accepted an unknown kind")
	}
}

// TestKindsMatchSchema 防止 secret.Kinds 与 schema.sql 的 CHECK 约束漂移：
// 两边任一新增类型而另一边没跟上时，运行期会以约束错误而不是清晰提示暴露。
func TestKindsMatchSchema(t *testing.T) {
	raw, err := os.ReadFile("../db/schema.sql")
	if err != nil {
		t.Skipf("schema.sql unavailable: %v", err)
	}
	schema := string(raw)
	idx := strings.Index(schema, "CREATE TABLE IF NOT EXISTS secrets")
	if idx < 0 {
		t.Fatal("secrets table missing from schema.sql")
	}
	end := strings.Index(schema[idx:], ");")
	if end < 0 {
		t.Fatal("unterminated secrets table definition")
	}
	block := schema[idx : idx+end]
	for _, k := range Kinds() {
		if !strings.Contains(block, "'"+k+"'") {
			t.Errorf("kind %q missing from schema CHECK", k)
		}
	}
}
