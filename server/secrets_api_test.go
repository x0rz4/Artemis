package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// newSecretTestServer builds a server against the isolated PG. Returns nil when
// the database is unavailable so the caller can skip.
func newSecretTestServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { m.Close() })
	td := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(ctx, m, td, td, td)
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s, s.Handler(), tok
}

func secretDo(t *testing.T, h http.Handler, tok, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestSecretsRequireAuth(t *testing.T) {
	_, h, _ := newSecretTestServer(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/secrets"},
		{"POST", "/api/secrets"},
		{"GET", "/api/secrets/1"},
		{"POST", "/api/secrets/1/reveal"},
		{"GET", "/api/secrets/meta"},
	} {
		code, _ := secretDo(t, h, "", tc.method, tc.path, nil)
		if code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", tc.method, tc.path, code)
		}
	}
}

func TestSecretsCRUDNeverEchoesValue(t *testing.T) {
	s, h, tok := newSecretTestServer(t)
	const value = "sk-test-0123456789abcdef"
	name := fmt.Sprintf("t-api-secret-%d", time.Now().UnixNano())

	code, out := secretDo(t, h, tok, "POST", "/api/secrets", map[string]any{
		"name": name, "kind": "api_key", "value": value, "host": "example.com",
	})
	if code != http.StatusOK {
		t.Fatalf("create = %d %v", code, out)
	}
	id := int64(out["id"].(float64))
	t.Cleanup(func() { s.m.pg.DeleteSecret(context.Background(), id) })

	// create response must not contain the plaintext.
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), value) {
		t.Fatalf("create response leaked plaintext: %s", raw)
	}

	code, out = secretDo(t, h, tok, "GET", "/api/secrets?q="+name, nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %v", code, out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), value) {
		t.Fatalf("list response leaked plaintext: %s", raw)
	}
	if !strings.Contains(string(raw), "__masked__") {
		t.Fatalf("list response missing mask: %s", raw)
	}

	code, out = secretDo(t, h, tok, "GET", fmt.Sprintf("/api/secrets/%d", id), nil)
	if code != http.StatusOK {
		t.Fatalf("get = %d %v", code, out)
	}
	raw, _ = json.Marshal(out)
	if strings.Contains(string(raw), value) {
		t.Fatalf("detail response leaked plaintext: %s", raw)
	}

	// 掩码值不能被当作替换值写回。
	code, _ = secretDo(t, h, tok, "PATCH", fmt.Sprintf("/api/secrets/%d", id), map[string]any{
		"value": "__masked__:…abcdef", "replace_value": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("masked replacement = %d, want 400", code)
	}

	// 显式替换值才生效。
	const next = "sk-rotated-9876543210"
	code, out = secretDo(t, h, tok, "PATCH", fmt.Sprintf("/api/secrets/%d", id), map[string]any{
		"value": next, "replace_value": true,
	})
	if code != http.StatusOK {
		t.Fatalf("replace = %d %v", code, out)
	}
	stored, err := s.m.pg.SecretByID(context.Background(), id)
	if err != nil || stored.Value != next {
		t.Fatalf("replacement not stored: %v", err)
	}
}

func TestSecretRevealRequiresReasonAndAudits(t *testing.T) {
	s, h, tok := newSecretTestServer(t)
	const value = "reveal-me-abcdef123456"
	name := fmt.Sprintf("t-reveal-%d", time.Now().UnixNano())
	code, out := secretDo(t, h, tok, "POST", "/api/secrets", map[string]any{
		"name": name, "kind": "token", "value": value,
	})
	if code != http.StatusOK {
		t.Fatalf("create = %d %v", code, out)
	}
	id := int64(out["id"].(float64))
	t.Cleanup(func() { s.m.pg.DeleteSecret(context.Background(), id) })

	code, out = secretDo(t, h, tok, "POST", fmt.Sprintf("/api/secrets/%d/reveal", id), map[string]any{})
	if code != http.StatusBadRequest {
		t.Fatalf("reveal without reason = %d %v", code, out)
	}
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), value) {
		t.Fatal("failed reveal leaked plaintext")
	}

	code, out = secretDo(t, h, tok, "POST", fmt.Sprintf("/api/secrets/%d/reveal", id), map[string]any{
		"reason": "用该 token 调用接口验证越权",
	})
	if code != http.StatusOK {
		t.Fatalf("reveal = %d %v", code, out)
	}
	if out["value"] != value {
		t.Fatalf("reveal returned %v, want plaintext", out["value"])
	}

	code, out = secretDo(t, h, tok, "GET", fmt.Sprintf("/api/secrets/%d/access", id), nil)
	if code != http.StatusOK {
		t.Fatalf("access = %d %v", code, out)
	}
	rows, _ := out["access"].([]any)
	found := false
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok && m["action"] == "reveal" {
			found = true
			if m["reason"] != "用该 token 调用接口验证越权" {
				t.Fatalf("reason not recorded: %v", m["reason"])
			}
		}
	}
	if !found {
		t.Fatal("reveal not audited")
	}
}

func TestSecretToolsAssociateRunInfoAndHideValue(t *testing.T) {
	s, _, _ := newSecretTestServer(t)
	if s.m.pg == nil {
		t.Skip("no database")
	}
	const value = "tool-secret-abcdef123"
	name := fmt.Sprintf("t-tool-%d", time.Now().UnixNano())
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: 12345, SessionID: "sess-x"})

	store := s.toolSecretStore()
	res, err := store.Call(ctx, json.RawMessage(fmt.Sprintf(
		`{"name":%q,"kind":"password","value":%q,"notes":"found in .env"}`, name, value)), nil)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Flatten()
	if strings.Contains(text, value) {
		t.Fatalf("secret_store result leaked plaintext: %s", text)
	}
	var out struct {
		ID        int64 `json:"id"`
		Duplicate bool  `json:"duplicate"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result not JSON: %v (%s)", err, text)
	}
	t.Cleanup(func() { s.m.pg.DeleteSecret(context.Background(), out.ID) })

	// Same value → same id, duplicate flagged.
	res2, _ := store.Call(ctx, json.RawMessage(fmt.Sprintf(
		`{"name":"another-name","kind":"password","value":%q}`, value)), nil)
	var out2 struct {
		ID        int64 `json:"id"`
		Duplicate bool  `json:"duplicate"`
	}
	_ = json.Unmarshal([]byte(res2.Flatten()), &out2)
	if out2.ID != out.ID || !out2.Duplicate {
		t.Fatalf("duplicate store = %+v, want id=%d duplicate", out2, out.ID)
	}

	links, err := s.m.pg.ListSecretLinks(context.Background(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	var hasTask, hasSession bool
	for _, l := range links {
		if l.TargetType == "task" && l.TargetRef == "12345" {
			hasTask = true
		}
		if l.TargetType == "conversation" && l.TargetRef == "sess-x" {
			hasSession = true
		}
	}
	if !hasTask || !hasSession {
		t.Fatalf("run attribution links missing: %+v", links)
	}

	// Search returns masks only.
	search := s.toolSecretSearch()
	res3, _ := search.Call(ctx, json.RawMessage(fmt.Sprintf(`{"q":%q}`, name)), nil)
	if strings.Contains(res3.Flatten(), value) {
		t.Fatalf("secret_search leaked plaintext: %s", res3.Flatten())
	}
	if !strings.Contains(res3.Flatten(), "__masked__") {
		t.Fatalf("secret_search missing mask: %s", res3.Flatten())
	}

	// Reveal requires a reason.
	reveal := s.toolSecretReveal()
	if r, _ := reveal.Call(ctx, json.RawMessage(fmt.Sprintf(`{"id":%d}`, out.ID)), nil); !r.IsError {
		t.Fatal("reveal without reason should be an error result")
	}
	r, _ := reveal.Call(ctx, json.RawMessage(fmt.Sprintf(`{"id":%d,"reason":"verify"}`, out.ID)), nil)
	if !strings.Contains(r.Flatten(), value) {
		t.Fatalf("reveal did not return plaintext: %s", r.Flatten())
	}
	rows, _ := s.m.pg.ListSecretAccess(context.Background(), out.ID, 20)
	audited := false
	for _, a := range rows {
		if a.Action == "reveal" && a.ActorKind == "agent" && a.TaskID != nil && *a.TaskID == 12345 {
			audited = true
		}
	}
	if !audited {
		t.Fatal("agent reveal not audited with task attribution")
	}
}

func TestSecretCreateRejectsMaskedValue(t *testing.T) {
	_, h, tok := newSecretTestServer(t)
	code, _ := secretDo(t, h, tok, "POST", "/api/secrets", map[string]any{
		"name": "masked", "kind": "token", "value": "__masked__:…abcdef",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("masked create = %d, want 400", code)
	}
}

func TestSecretNotFound(t *testing.T) {
	_, h, tok := newSecretTestServer(t)
	code, _ := secretDo(t, h, tok, "GET", "/api/secrets/999999999", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing secret = %d, want 404", code)
	}
}

// 保证测试辅助的类型与 db 包类型名不冲突，同时给出明确的意图。
var _ = db.ErrSecretNotFound
