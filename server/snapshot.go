package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 库快照：同步/上传/删除这类会改动磁盘的操作之前，把**将被改动的目标**连同清单一起复制
// 一份到 <库根>/.snapshots/<id>/，于是"一次同步把武器库搞坏"可以整库退回。
//
// 为什么不只靠 manifest.json.bak：清单回退只能还原"声称的版本"，磁盘上的文件还是新的；
// 反过来文件退回了、清单还是新的，同样对不上。快照把两者放在一起，回滚才自洽。
//
// 语义边界（有意为之）：
//   - 快照包含 manifest.json（若存在），回滚会把它一起还原；
//   - 回滚是"把快照里的条目放回去"，**不删除**快照之后新增的东西 —— 与同步的并集策略一致；
//   - 只保留最近 maxSnapshots 份，且不备份被删除的条目所在目录以外的文件。

const (
	snapshotsDirName = ".snapshots"
	maxSnapshots     = 5
	snapshotMetaFile = "meta.json"
	// libManifestName 是工具库与技能库共用的清单文件名。
	libManifestName = "manifest.json"
)

// snapshotInfo 是一份快照的元数据（列表接口直接返回）。
type snapshotInfo struct {
	SHA256    map[string]string `json:"sha256,omitempty"`
	ID        string            `json:"id"`
	CreatedAt string            `json:"created_at"`
	Reason    string            `json:"reason,omitempty"`
	Items     []string          `json:"items"`
	Bytes     int64             `json:"bytes"`
}

// snapshotRoot 是某个库的快照根（data/tools/.snapshots）。
func snapshotRoot(base string) string { return filepath.Join(base, snapshotsDirName) }

// copyTree 复制文件或目录（保留权限位——工具二进制的执行位必须跟着走）。
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		// 链接/设备文件不进快照：工具库里出现它们本身就是异常，跳过比跟着复制安全。
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}

// dirBytes 统计目录占用（元数据展示用，失败按 0 计）。
func dirBytes(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// takeSnapshot 在改动前复制 rels（相对 base 的路径）与清单文件。
//
// 没有任何东西可复制时返回 ""（例如首次安装，没有旧版本要保护）——调用方据此跳过回滚项。
// 任何一步出错都会清掉半截快照并返回错误：宁可没有快照，也不要一份不完整的。
func takeSnapshot(base, reason string, rels []string) (string, error) {
	if base == "" {
		return "", errors.New("库根为空")
	}
	items := []string{}
	for _, rel := range rels {
		clean, msg := skillRelPath(rel)
		if msg != "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(clean))); err == nil {
			items = append(items, clean)
		}
	}
	// 清单一起进快照：文件回退了、清单还在说新版本，是最难查的一类不一致。
	if _, err := os.Stat(filepath.Join(base, libManifestName)); err == nil {
		items = append(items, libManifestName)
	}
	if len(items) == 0 {
		return "", nil
	}

	root := snapshotRoot(base)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	id := time.Now().UTC().Format("20060102T150405.000000000")
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, rel := range items {
		src := filepath.Join(base, filepath.FromSlash(rel))
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := copyTree(src, dst); err != nil {
			cleanup()
			return "", fmt.Errorf("快照失败（%s）: %w", rel, err)
		}
	}
	meta := snapshotInfo{ID: id, CreatedAt: time.Now().UTC().Format(time.RFC3339), Reason: reason, Items: items, Bytes: dirBytes(dir)}
	meta.SHA256 = map[string]string{}
	if err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		sum, err := toolFileSHA256(p)
		if err != nil {
			return err
		}
		meta.SHA256[filepath.ToSlash(rel)] = sum
		return nil
	}); err != nil {
		cleanup()
		return "", err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		cleanup()
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, snapshotMetaFile), b, 0o644); err != nil {
		cleanup()
		return "", err
	}
	pruneSnapshots(base)
	return id, nil
}

// listSnapshots 列出快照（新的在前）。
func listSnapshots(base string) []snapshotInfo {
	out := []snapshotInfo{}
	root := snapshotRoot(base)
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		info := snapshotInfo{ID: e.Name(), Items: []string{}}
		if b, err := os.ReadFile(filepath.Join(dir, snapshotMetaFile)); err == nil {
			_ = json.Unmarshal(b, &info)
			info.ID = e.Name() // 目录名才是真相，meta 只补充说明
		}
		if len(info.Items) == 0 {
			// 没有 meta（或解析失败）时按内容推断：至少还能看出里面有什么。
			_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
				if err != nil || fi == nil || fi.IsDir() {
					return nil
				}
				if rel, rerr := filepath.Rel(dir, p); rerr == nil && rel != snapshotMetaFile {
					info.Items = append(info.Items, filepath.ToSlash(rel))
				}
				return nil
			})
		}
		if info.CreatedAt == "" {
			if fi, err := e.Info(); err == nil {
				info.CreatedAt = fi.ModTime().UTC().Format(time.RFC3339)
			}
		}
		if info.Bytes == 0 {
			info.Bytes = dirBytes(dir)
		}
		sort.Strings(info.Items)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// latestSnapshotID 返回最近一份快照的 id（"" = 没有）。
func latestSnapshotID(base string) string {
	snaps := listSnapshots(base)
	if len(snaps) == 0 {
		return ""
	}
	return snaps[0].ID
}

// snapshotItems 返回某份快照包含的相对路径（找不到返回 nil）。
func snapshotItems(base, id string) []string {
	for _, s := range listSnapshots(base) {
		if s.ID == id {
			return s.Items
		}
	}
	return nil
}

// restoreSnapshot 把一份快照里的条目放回库根，返回实际还原的相对路径。
// 按库条目恢复：目录内部以快照为准，未包含的其它库条目不动。
func restoreSnapshot(base, id string) ([]string, error) {
	if id == "" {
		return nil, errors.New("未指定快照")
	}
	if msg := validToolName(id); msg != "" {
		return nil, fmt.Errorf("快照 id 非法: %s", msg)
	}
	source := filepath.Join(snapshotRoot(base), id)
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return nil, errors.New("快照不存在: " + id)
	}
	stage, err := os.MkdirTemp(base, ".restore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	items := snapshotItems(base, id)
	sort.Slice(items, func(i, j int) bool {
		if items[i] == libManifestName {
			return false
		}
		if items[j] == libManifestName {
			return true
		}
		return items[i] < items[j]
	})
	rels := []string{}
	for _, rel := range items {
		clean, msg := skillRelPath(rel)
		if msg != "" {
			return nil, errors.New("快照包含非法路径")
		}
		nested := false
		for _, parent := range rels {
			if clean == parent || strings.HasPrefix(clean, parent+"/") {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		if err = copyTree(filepath.Join(source, filepath.FromSlash(clean)), filepath.Join(stage, "new", filepath.FromSlash(clean))); err != nil {
			return nil, err
		}
		rels = append(rels, clean)
	}
	type switchedItem struct {
		rel    string
		hadOld bool
	}
	if raw, readErr := os.ReadFile(filepath.Join(source, snapshotMetaFile)); readErr == nil {
		var meta snapshotInfo
		if err = json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
		for rel, want := range meta.SHA256 {
			clean, msg := skillRelPath(rel)
			if msg != "" {
				return nil, errors.New("快照校验路径无效")
			}
			got, hashErr := toolFileSHA256(filepath.Join(stage, "new", filepath.FromSlash(clean)))
			if hashErr != nil || got != want {
				return nil, fmt.Errorf("快照内容校验失败: %s", clean)
			}
		}
	}
	switched := []switchedItem{}
	rollback := func(cause error) error {
		for i := len(switched) - 1; i >= 0; i-- {
			item := switched[i]
			target := filepath.Join(base, filepath.FromSlash(item.rel))
			cause = errors.Join(cause, os.RemoveAll(target))
			if item.hadOld {
				cause = errors.Join(cause, os.Rename(filepath.Join(stage, "old", filepath.FromSlash(item.rel)), target))
			}
		}
		return cause
	}
	for _, rel := range rels {
		target := filepath.Join(base, filepath.FromSlash(rel))
		backup := filepath.Join(stage, "old", filepath.FromSlash(rel))
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, rollback(err)
		}
		hadOld := false
		if _, statErr := os.Lstat(target); statErr == nil {
			if err = os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
				return nil, rollback(err)
			}
			if err = os.Rename(target, backup); err != nil {
				return nil, rollback(err)
			}
			hadOld = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, rollback(statErr)
		}
		switched = append(switched, switchedItem{rel, hadOld})
		if err = os.Rename(filepath.Join(stage, "new", filepath.FromSlash(rel)), target); err != nil {
			return nil, rollback(err)
		}
	}
	restored := []string{}
	for _, rel := range rels {
		_ = filepath.Walk(filepath.Join(base, filepath.FromSlash(rel)), func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				r, _ := filepath.Rel(base, p)
				restored = append(restored, filepath.ToSlash(r))
			}
			return err
		})
	}
	sort.Strings(restored)
	return restored, nil
}

// pruneSnapshots 只保留最近 maxSnapshots 份（快照会占磁盘，工具包很大）。
func pruneSnapshots(base string) {
	snaps := listSnapshots(base)
	protected := ""
	if len(snaps) > 0 {
		protected = strings.TrimPrefix(snaps[0].Reason, "pre-rollback:")
	}
	remaining := len(snaps)
	for i := len(snaps) - 1; i >= 0 && remaining > maxSnapshots; i-- {
		if snaps[i].ID == protected {
			continue
		}
		_ = os.RemoveAll(filepath.Join(snapshotRoot(base), snaps[i].ID))
		remaining--
	}
}

// libTryLock 保证同一个库的写操作（同步/上传/导入/删除/回滚）不并发：两个同步同时写
// manifest.json 会互相覆盖，留下的 .bak 也说不清是哪一版。非阻塞 —— 让第二个请求拿到
// 明确的 409，而不是排在一个可能几分钟的下载后面。
func libTryLock(mu *sync.Mutex) bool { return mu.TryLock() }
