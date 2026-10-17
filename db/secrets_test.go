package db

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Autumn-27/artex/secret"
)

// openSecretTestDB opens the isolated test database and skips when unavailable.
// Callers must clean up rows they create: the suite may share one database.
func openSecretTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSecretCreateDedupesAndLinks(t *testing.T) {
	d := openSecretTestDB(t)
	ctx := context.Background()
	value := "s3cr3t-" + strconv.FormatInt(nowNanoForTest(), 10)

	first := &Secret{Name: "t-secret-dedupe", Kind: secret.KindPassword, Value: value, Notes: "first", CreatedBy: "agent"}
	id1, created1, err := d.CreateSecret(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !created1 {
		t.Fatal("first insert reported duplicate")
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM secrets WHERE id=$1`, id1) })

	// 同 kind+value 再写一次：should reuse the row and not overwrite the name.
	second := &Secret{Name: "t-secret-second", Kind: secret.KindPassword, Value: value, CreatedBy: "agent"}
	id2, created2, err := d.CreateSecret(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("duplicate insert created a new row")
	}
	if id2 != id1 {
		t.Fatalf("duplicate id = %d, want %d", id2, id1)
	}
	got, err := d.SecretByID(ctx, id1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "t-secret-dedupe" {
		t.Fatalf("name overwritten by duplicate: %q", got.Name)
	}
	if got.Value != value {
		t.Fatal("stored value changed")
	}
	if got.ValueHint != secret.Hint(value) {
		t.Fatalf("hint = %q, want %q", got.ValueHint, secret.Hint(value))
	}

	// A different kind with the same value must be a separate row.
	other := &Secret{Name: "t-secret-other-kind", Kind: secret.KindAPIKey, Value: value, CreatedBy: "agent"}
	id3, created3, err := d.CreateSecret(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if !created3 || id3 == id1 {
		t.Fatalf("kind not part of the fingerprint (id=%d created=%v)", id3, created3)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM secrets WHERE id=$1`, id3) })

	// Links are idempotent and counted.
	if ok, err := d.AddSecretLink(ctx, id1, "task", "42", "agent"); err != nil || !ok {
		t.Fatalf("add link = %v, %v", ok, err)
	}
	if ok, err := d.AddSecretLink(ctx, id1, "task", "42", "agent"); err != nil || ok {
		t.Fatalf("duplicate link should be a no-op (ok=%v err=%v)", ok, err)
	}
	if ok, err := d.AddSecretLink(ctx, id1, "asset", "7", "agent"); err != nil || !ok {
		t.Fatalf("add asset link = %v, %v", ok, err)
	}
	links, err := d.ListSecretLinks(ctx, id1)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2", len(links))
	}
	counts, err := d.CountSecretLinks(ctx, []int64{id1})
	if err != nil {
		t.Fatal(err)
	}
	if counts[id1] != 2 {
		t.Fatalf("link count = %d, want 2", counts[id1])
	}

	// Filtering by task sees the linked row and excludes the unrelated one.
	list, err := d.ListSecrets(ctx, SecretFilter{TaskID: 42, Q: "t-secret", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		if s.ID == id3 {
			t.Fatal("unlinked secret returned for task filter")
		}
		if s.ID == id1 {
			found = true
		}
	}
	if !found {
		t.Fatal("linked secret missing from task filter")
	}

	// Removing a link is scoped to the secret.
	if err := d.RemoveSecretLink(ctx, id1, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveSecretLink(ctx, id1, links[0].ID); err == nil {
		t.Fatal("removing a missing link should error")
	}
}

func TestSecretUpdateRejectsMaskedValue(t *testing.T) {
	d := openSecretTestDB(t)
	ctx := context.Background()
	value := "upd-" + strconv.FormatInt(nowNanoForTest(), 10)
	id, _, err := d.CreateSecret(ctx, &Secret{Name: "t-secret-update", Kind: secret.KindToken, Value: value, CreatedBy: "user"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM secrets WHERE id=$1`, id) })

	masked := &Secret{Name: "t-secret-update", Kind: secret.KindToken, Value: secret.Mask(value)}
	if err := d.UpdateSecret(ctx, id, masked, true); err == nil {
		t.Fatal("masked value accepted as a replacement")
	}

	// Metadata-only update leaves the value untouched.
	meta := &Secret{Name: "t-secret-renamed", Kind: secret.KindToken, Notes: "renamed"}
	if err := d.UpdateSecret(ctx, id, meta, false); err != nil {
		t.Fatal(err)
	}
	got, err := d.SecretByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "t-secret-renamed" || got.Notes != "renamed" || got.Value != value {
		t.Fatalf("unexpected after metadata update: name=%q notes=%q", got.Name, got.Notes)
	}

	// A real replacement rotates the fingerprint and hint.
	newValue := value + "-new"
	if err := d.UpdateSecret(ctx, id, &Secret{Name: got.Name, Kind: got.Kind, Value: newValue}, true); err != nil {
		t.Fatal(err)
	}
	got, err = d.SecretByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != newValue || got.Fingerprint != secret.Fingerprint(got.Kind, newValue) {
		t.Fatal("replacement not persisted")
	}

	if err := d.UpdateSecret(ctx, id+999999, &Secret{Name: "x"}, false); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing row update error = %v, want ErrSecretNotFound", err)
	}
}

func TestSecretAccessAuditAndDelete(t *testing.T) {
	d := openSecretTestDB(t)
	ctx := context.Background()
	value := "audit-" + strconv.FormatInt(nowNanoForTest(), 10)
	id, _, err := d.CreateSecret(ctx, &Secret{Name: "t-secret-audit", Kind: secret.KindAPIKey, Value: value, CreatedBy: "user"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM secrets WHERE id=$1`, id) })

	tid := int64(99)
	if err := d.LogSecretAccess(ctx, &SecretAccess{
		SecretID: &id, SecretName: "t-secret-audit", Action: "reveal",
		Actor: "agent", ActorKind: "agent", TaskID: &tid, Reason: "verify login",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.LogSecretAccess(ctx, &SecretAccess{SecretID: &id, Action: "bogus"}); err == nil {
		t.Fatal("invalid action accepted")
	}
	logs, err := d.ListSecretAccess(ctx, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Action != "reveal" || logs[0].Reason != "verify login" {
		t.Fatalf("unexpected audit rows: %+v", logs)
	}
	if logs[0].TaskID == nil || *logs[0].TaskID != 99 {
		t.Fatal("task attribution lost")
	}

	if err := d.TouchSecretUsed(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.SecretByID(ctx, id); got.LastUsedAt == nil {
		t.Fatal("last_used_at not set")
	}

	if err := d.DeleteSecret(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SecretByID(ctx, id); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("deleted secret still readable: %v", err)
	}
	// Audit survives deletion (secret_id set to NULL) so the trail is not erased.
	logs, err = d.ListSecretAccess(ctx, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		// The FK is ON DELETE SET NULL, so the lookup by secret_id finds nothing;
		// the row itself must still exist with the name preserved.
		t.Fatalf("unexpected rows for deleted id: %+v", logs)
	}
	var name string
	if err := d.QueryRowContext(ctx, `SELECT secret_name FROM secret_access_log WHERE action='reveal' AND reason='verify login' ORDER BY id DESC LIMIT 1`).Scan(&name); err != nil {
		t.Fatalf("audit row removed with the secret: %v", err)
	}
	if name != "t-secret-audit" {
		t.Fatalf("audit secret_name = %q", name)
	}
}

func nowNanoForTest() int64 {
	// 用时间戳避免与上次失败运行残留的行撞指纹；不引入随机源以保持测试可重现。
	return time.Now().UnixNano()
}
