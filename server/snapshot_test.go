package server

import (
	"os"
	"path/filepath"
	"testing"
)

// 快照只碰文件系统，用例不需要数据库。

func writeLibFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotTakeAndRestore(t *testing.T) {
	base := t.TempDir()
	writeLibFile(t, filepath.Join(base, "fscan"), "v1", 0o755)
	writeLibFile(t, filepath.Join(base, "peass", "entry.sh"), "v1", 0o644)
	writeLibFile(t, filepath.Join(base, libManifestName), `{"tools":[{"name":"fscan"}]}`, 0o644)

	id, err := takeSnapshot(base, "sync:official", []string{"fscan", "peass"})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("有内容可存时应当生成快照")
	}
	items := snapshotItems(base, id)
	// 清单必须一起进快照：文件回退了、清单还在说新版本，是最难查的一类不一致。
	for _, want := range []string{"fscan", "peass", libManifestName} {
		if !contains(items, want) {
			t.Fatalf("快照缺少 %s: %v", want, items)
		}
	}
	if got := len(listSnapshots(base)); got != 1 {
		t.Fatalf("应有一份快照，实际 %d", got)
	}

	// 改动磁盘与清单，然后回滚。
	writeLibFile(t, filepath.Join(base, "fscan"), "v2", 0o755)
	writeLibFile(t, filepath.Join(base, libManifestName), `{"tools":[]}`, 0o644)
	if err := os.RemoveAll(filepath.Join(base, "peass")); err != nil {
		t.Fatal(err)
	}

	restored, err := restoreSnapshot(base, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 3 {
		t.Fatalf("应还原 3 项: %v", restored)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "fscan")); string(b) != "v1" {
		t.Fatalf("文件内容未回滚: %q", b)
	}
	// 工具二进制的执行位必须跟着回滚，否则回滚完工具就跑不起来了。
	if fi, err := os.Stat(filepath.Join(base, "fscan")); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("执行位未保留: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, libManifestName)); string(b) != `{"tools":[{"name":"fscan"}]}` {
		t.Fatalf("清单未回滚: %q", b)
	}
	if _, err := os.Stat(filepath.Join(base, "peass", "entry.sh")); err != nil {
		t.Fatalf("被删目录应被还原: %v", err)
	}
}

func TestSnapshotNoopWithoutContent(t *testing.T) {
	base := t.TempDir()
	id, err := takeSnapshot(base, "upload", []string{"nope"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("没有可保护的内容时不应生成快照: %q", id)
	}
	if _, err := os.Stat(snapshotRoot(base)); !os.IsNotExist(err) {
		t.Fatal("不该为一次空快照建目录")
	}
}

func TestRestoreSnapshotRejectsBadID(t *testing.T) {
	base := t.TempDir()
	for _, bad := range []string{"", "../escape", "a/b"} {
		if _, err := restoreSnapshot(base, bad); err == nil {
			t.Errorf("id %q 应当被拒绝", bad)
		}
	}
	if _, err := restoreSnapshot(base, "20260101T000000.000"); err == nil {
		t.Fatal("不存在的快照应当报错")
	}
}

func TestPruneSnapshotsKeepsLatest(t *testing.T) {
	base := t.TempDir()
	root := snapshotRoot(base)
	ids := []string{
		"20260101T000001.000", "20260101T000002.000", "20260101T000003.000",
		"20260101T000004.000", "20260101T000005.000", "20260101T000006.000",
		"20260101T000007.000",
	}
	for _, id := range ids {
		writeLibFile(t, filepath.Join(root, id, snapshotMetaFile), `{"id":"`+id+`"}`, 0o644)
	}
	pruneSnapshots(base)
	left := listSnapshots(base)
	if len(left) != maxSnapshots {
		t.Fatalf("应保留 %d 份，实际 %d", maxSnapshots, len(left))
	}
	// 最新的必须还在，最旧的必须被清掉。
	if left[0].ID != ids[len(ids)-1] {
		t.Fatalf("最新的快照应排在最前: %v", left[0].ID)
	}
	if _, err := os.Stat(filepath.Join(root, ids[0])); !os.IsNotExist(err) {
		t.Fatal("最旧的快照应被清理")
	}
}

func TestLibTryLockIsExclusive(t *testing.T) {
	base := t.TempDir()
	writeLibFile(t, filepath.Join(base, "fscan"), "v1", 0o755)
	s := &Server{}
	if !libTryLock(&s.toolLibMu) {
		t.Fatal("首次加锁应当成功")
	}
	if libTryLock(&s.toolLibMu) {
		t.Fatal("同一把锁不应被重复获取（并发写会互相覆盖清单）")
	}
	s.toolLibMu.Unlock()
	if !libTryLock(&s.toolLibMu) {
		t.Fatal("释放后应当能再次获取")
	}
	s.toolLibMu.Unlock()
	// 两个库的锁互相独立。
	if !libTryLock(&s.skillLibMu) {
		t.Fatal("技能库的锁应当独立于工具库")
	}
	s.skillLibMu.Unlock()
}
