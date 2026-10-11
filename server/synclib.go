package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// 库源同步的公共底座：工具库（data/tools）与技能库（data/skills）共用。
//
// 与 selfupdate 的白名单策略**刻意不同**：主程序自更新的源是写死的常量，所以它能用
// 一张固定的 GitHub 域名白名单；库源是用户自己配的，白名单只能按源派生——github 型源
// 放行 GitHub 系域名，http 型源只放行它自己那一个 host，且重定向不允许换 host。
//
// 令牌不进清单：清单会随库同步、可能被手工编辑或分享出去，凭据存 settings 表，
// 接口只回 "是否已配置"，不回值。

const (
	maxSourceManifestBytes = 8 << 20
	maxSourceAssetBytes    = 512 << 20
	sourceHTTPTimeout      = 5 * time.Minute
)

// libSource 是一个库源。github / http 型用于「清单 + 资产」同步，git 型用于整仓克隆。
type libSource struct {
	Name   string `json:"name"`
	Type   string `json:"type"`             // github | http | git
	Repo   string `json:"repo,omitempty"`   // github: owner/repo
	Branch string `json:"branch,omitempty"` // github: 分支（缺省 main）
	Path   string `json:"path,omitempty"`   // github: 清单文件在仓库里的路径（缺省 manifest.json）
	URL    string `json:"url,omitempty"`    // http: 清单 URL；git: 仓库地址
	// AssetBase 覆盖资产根：github 缺省「清单同目录的 raw 地址」，http 缺省「清单 URL 的目录」。
	AssetBase string `json:"asset_base,omitempty"`
	// AllowUnpinned 允许安装 sha256 为空的条目。缺省 false —— 同步链路必须钉摘要，
	// 否则被中间人换掉一个二进制，校验那一层等于不存在。
	AllowUnpinned bool `json:"allow_unpinned,omitempty"`

	// 同步状态（就地写回清单，页面上直接看得到）。
	LastSyncAt string `json:"last_sync_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func (src libSource) typeOrDefault() string {
	t := strings.ToLower(strings.TrimSpace(src.Type))
	if t == "" {
		return "github"
	}
	return t
}

func (src libSource) branchOrDefault() string {
	if b := strings.TrimSpace(src.Branch); b != "" {
		return b
	}
	return "main"
}

// sourceHosts 返回该源允许访问的域名集合（空集 = 配置不完整）。
func sourceHosts(src libSource) (map[string]bool, error) {
	hosts := map[string]bool{}
	switch src.typeOrDefault() {
	case "github":
		if strings.TrimSpace(src.Repo) == "" {
			return nil, errors.New("github 源缺少 repo（owner/repo）")
		}
		for _, h := range []string{
			"api.github.com", "codeload.github.com", "github.com", "raw.githubusercontent.com",
			"objects.githubusercontent.com", "release-assets.githubusercontent.com",
		} {
			hosts[h] = true
		}
	case "http", "git":
		raw := strings.TrimSpace(src.URL)
		if raw == "" {
			return nil, fmt.Errorf("%s 源缺少 url", src.typeOrDefault())
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("url 无法解析: %w", err)
		}
		if !strings.EqualFold(u.Scheme, "https") {
			return nil, errors.New("只允许 https 源")
		}
		hosts[strings.ToLower(u.Hostname())] = true
	default:
		return nil, fmt.Errorf("不支持的源类型 %q（可用：github / http / git）", src.Type)
	}
	return hosts, nil
}

// checkSourceURL 强制 https + 域名在源自己的白名单里。
func checkSourceURL(u *url.URL, hosts map[string]bool) error {
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("拒绝非 HTTPS 地址: %s", u.Scheme+"://"+u.Host)
	}
	if !hosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("拒绝源白名单外的域名: %s", u.Hostname())
	}
	return nil
}

// sourceClient 构造只认该源自身域名的 HTTP 客户端：任何一跳重定向换 host 都会失败。
func (s *Server) sourceClient(src libSource) (*http.Client, map[string]bool, error) {
	hosts, err := sourceHosts(src)
	if err != nil {
		return nil, nil, err
	}
	client := &http.Client{
		Timeout: sourceHTTPTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("重定向次数过多")
			}
			return checkSourceURL(req.URL, hosts)
		},
	}
	return client, hosts, nil
}

// sourceManifestURL 返回该源清单文件的地址（github 型按 raw 地址推导）。
func sourceManifestURL(src libSource) (string, error) {
	switch src.typeOrDefault() {
	case "github":
		repo := strings.TrimSpace(src.Repo)
		if repo == "" {
			return "", errors.New("github 源缺少 repo（owner/repo）")
		}
		p := strings.TrimSpace(src.Path)
		if p == "" {
			p = "manifest.json"
		}
		clean, msg := skillRelPath(p)
		if msg != "" {
			return "", fmt.Errorf("清单路径非法: %s", msg)
		}
		return "https://raw.githubusercontent.com/" + repo + "/" + src.branchOrDefault() + "/" + clean, nil
	case "http":
		if strings.TrimSpace(src.URL) == "" {
			return "", errors.New("http 源缺少 url")
		}
		return strings.TrimSpace(src.URL), nil
	}
	return "", fmt.Errorf("源类型 %q 没有清单文件", src.Type)
}

// sourceAssetURL 返回某个资产（相对路径）在该源上的地址。
// 优先级：条目自带的绝对 url > source.asset_base + 相对路径 > 清单所在目录 + 相对路径。
func sourceAssetURL(src libSource, manifestURL, entryURL, relPath string) (string, error) {
	if e := strings.TrimSpace(entryURL); e != "" {
		return e, nil
	}
	rel := strings.TrimSpace(relPath)
	if rel == "" {
		return "", errors.New("条目缺少 path")
	}
	clean, msg := skillRelPath(rel)
	if msg != "" {
		return "", fmt.Errorf("资产路径非法: %s", msg)
	}
	base := strings.TrimSpace(src.AssetBase)
	if base == "" {
		mu, err := url.Parse(manifestURL)
		if err != nil {
			return "", err
		}
		mu.Path = path.Dir(mu.Path) + "/"
		mu.RawQuery = ""
		base = mu.String()
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + clean, nil
}

// fetchSourceBytes 取一个源上的文件（大小受限，走源自己的白名单）。
func fetchSourceBytes(ctx context.Context, client *http.Client, hosts map[string]bool, rawURL string, limit int64, token string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("地址无法解析: %w", err)
	}
	if err := checkSourceURL(u, hosts); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "artemis-lib-sync")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("源拒绝访问（%d）：私有源请在页面上配置访问令牌", resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("源上不存在: %s", rawURL)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("源返回 %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("源文件超过大小上限（%d 字节）", limit)
	}
	return b, nil
}

// libSourceTokenKey 返回源令牌在 settings 表里的键。
func libSourceTokenKey(prefix, source string) string {
	return prefix + "_source_token:" + source
}

// sourceToken 读取源令牌（"" = 未配置）。
func (s *Server) sourceToken(prefix, source string) string {
	if s.m == nil || s.m.pg == nil || source == "" {
		return ""
	}
	v, ok, err := s.m.pg.GetSetting(libSourceTokenKey(prefix, source))
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// setSourceToken 写入或清除源令牌（空串 = 删除）。
func (s *Server) setSourceToken(prefix, source, token string) error {
	if s.m == nil || s.m.pg == nil {
		return errors.New("数据库未连接")
	}
	return s.m.pg.SetSetting(libSourceTokenKey(prefix, source), strings.TrimSpace(token))
}

// hashSourceBytes 返回字节流的 sha256（hex 小写）。
func hashSourceBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// installSourceFile 把源上的一个文件装到 dst：
//
//	下载 → 写 .part → 校验 sha256 → 原子替换。
//
// 校验不过时**保持原文件不动**：一个半截或被换掉的工具比没有更危险。wantSHA 为空时
// 只有显式允许（allowUnpinned）才落盘，否则报错——同步链路的默认立场是必须钉摘要。
func (s *Server) installSourceFile(ctx context.Context, client *http.Client, hosts map[string]bool, rawURL, dst, wantSHA, token string, allowUnpinned bool) (string, error) {
	if strings.TrimSpace(wantSHA) == "" && !allowUnpinned {
		return "", errors.New("条目未提供 sha256（如确认信任该源，可在源上开启 allow_unpinned）")
	}
	b, err := fetchSourceBytes(ctx, client, hosts, rawURL, maxSourceAssetBytes, token)
	if err != nil {
		return "", err
	}
	got := hashSourceBytes(b)
	if want := strings.ToLower(strings.TrimSpace(wantSHA)); want != "" && got != want {
		return "", fmt.Errorf("sha256 不符（清单 %s，实际 %s），已放弃安装", want, got)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	part := dst + ".part"
	if err := os.WriteFile(part, b, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(part, dst); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	return got, nil
}

// upsertLibSource 写入清单里的源（按名字覆盖），并清空上一次的错误状态。
func upsertLibSource(list []libSource, src libSource) []libSource {
	for i := range list {
		if list[i].Name == src.Name {
			src.LastSyncAt = list[i].LastSyncAt
			src.LastError = ""
			list[i] = src
			return list
		}
	}
	return append(list, src)
}

// putLibSourceState 就地记录一次同步的时间与错误。
func putLibSourceState(list []libSource, name, syncAt, syncErr string) {
	for i := range list {
		if list[i].Name == name {
			if syncAt != "" {
				list[i].LastSyncAt = syncAt
			}
			list[i].LastError = syncErr
			return
		}
	}
}
