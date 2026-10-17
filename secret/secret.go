// Package secret 定义机密库的领域规则：类型枚举、去重指纹与掩码展示。
//
// 本包刻意不依赖 db 或 server：无论数据来自 PostgreSQL、未来的加密存储，
// 还是测试桩，掩码与指纹口径都必须唯一。明文存储是第一阶段的产品选择，
// 见 spec/changes/secret-vault/change.md 的「安全边界」。
package secret

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// 机密类型。与 db/schema.sql 中 secrets.kind 的 CHECK 约束保持一致；
// 新增类型必须同时修改两处（测试 TestKindsMatchSchema 会核对）。
const (
	KindAPIKey           = "api_key"
	KindPassword         = "password"
	KindToken            = "token"
	KindCookie           = "cookie"
	KindCertificate      = "certificate"
	KindPrivateKey       = "private_key"
	KindConnectionString = "connection_string"
	KindOther            = "other"
)

// MaskedPrefix 是掩码值的标记。管理接口回显时用它替换真实值；
// 与 notify 包使用同样的语义，但这里独立实现，避免领域之间互相依赖。
const MaskedPrefix = "__masked__"

var kinds = []string{
	KindAPIKey, KindPassword, KindToken, KindCookie,
	KindCertificate, KindPrivateKey, KindConnectionString, KindOther,
}

// Kinds 返回全部合法类型（顺序稳定，供 UI 渲染与校验）。
func Kinds() []string { return append([]string(nil), kinds...) }

// ValidKind 报告 kind 是否受支持；空串视为 other，由 NormalizeKind 处理。
func ValidKind(kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// NormalizeKind 把空串归一为 other，并拒绝未知类型。
func NormalizeKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return KindOther, nil
	}
	if !ValidKind(kind) {
		return "", fmt.Errorf("机密类型无效，可选：%s", strings.Join(kinds, " / "))
	}
	return kind, nil
}

// Mask 生成用于展示的掩码值：
//
//	"__masked__"            值太短，不给任何提示
//	"__masked__:…ab12cd"    带原值末 6 位，便于辨认但不泄漏
//
// 与 notify.MaskedValue 的口径一致；这是刻意的产品一致性，不是复用疏漏。
func Mask(value string) string {
	if len(value) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + value[len(value)-6:]
}

// IsMasked 报告 v 是否为掩码值。更新路径用它拒绝把掩码字面量写进库。
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// Hint 返回用于列表展示的末位提示（最多 6 位）；值过短时返回空串。
// 与 Mask 分开是因为列表只需要提示，不需要重复前缀。
func Hint(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 6 {
		return ""
	}
	return "…" + value[len(value)-6:]
}

// Fingerprint 计算去重指纹。用 kind 参与哈希，避免「同一个字符串既当密码
// 又当 API key」被意外合并；分隔符 \x00 保证 ("a","b") 与 ("ab","") 不碰撞。
func Fingerprint(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	return hex.EncodeToString(sum[:])
}
