package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

var (
	// ErrCrossHost 表示 Destination 指向了其他主机。
	ErrCrossHost = errors.New("netsec: destination host differs from request host")
	// ErrCrossTenant 表示 Destination 落在了当前租户命名空间之外。
	ErrCrossTenant = errors.New("netsec: destination escapes tenant namespace")
	// ErrTraversal 表示路径中包含点点穿越段（含百分号编码形式）。
	ErrTraversal = errors.New("netsec: path contains dot-dot traversal")
	// ErrBadDestination 表示 Destination 头无法解析或不是绝对路径。
	ErrBadDestination = errors.New("netsec: malformed Destination header")
)

// ValidateDestination 校验并规范化 Destination 头。
//
// raw 可以是绝对 URI（"https://host/path"）或绝对路径（"/path"）。
// 绝对 URI 的主机必须与 reqHost（请求 Host 头）一致；百分号解码后
// 拒绝任何 "." / ".." 段、反斜杠与 NUL；最终路径必须位于 tenantRoot 之内。
// 返回规范化后的绝对路径。
func ValidateDestination(raw, reqHost, tenantRoot string) (string, error) {
	rawPath := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", ErrBadDestination
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", ErrBadDestination
		}
		if !sameHost(u.Host, u.Scheme, reqHost) {
			return "", ErrCrossHost
		}
		rawPath = u.EscapedPath()
	}
	if !strings.HasPrefix(rawPath, "/") {
		return "", ErrBadDestination
	}
	return CanonicalTenantPath(rawPath, tenantRoot)
}

// CanonicalTenantPath 对（可能含百分号编码的）绝对路径做解码与规范化，
// 并确认其位于 tenantRoot 之内。源路径与目标路径共用此校验。
func CanonicalTenantPath(rawPath, tenantRoot string) (string, error) {
	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("%w: invalid percent-encoding", ErrBadDestination)
	}
	if !strings.HasPrefix(decoded, "/") {
		return "", ErrBadDestination
	}
	for _, seg := range strings.Split(decoded, "/") {
		if seg == ".." {
			return "", ErrTraversal
		}
		if strings.ContainsAny(seg, "\\\x00") {
			return "", ErrBadDestination
		}
	}
	clean := path.Clean(decoded)
	root := path.Clean(tenantRoot)
	if root != "/" && clean != root && !strings.HasPrefix(clean, root+"/") {
		return "", ErrCrossTenant
	}
	return clean, nil
}

// sameHost 比较 Destination 主机与请求 Host，忽略大小写与默认端口。
func sameHost(destHost, scheme, reqHost string) bool {
	norm := func(host, scheme string) string {
		host = strings.ToLower(host)
		h, p, err := net.SplitHostPort(host)
		if err != nil {
			// 无端口或解析失败，去掉可能的尾部点即可。
			return strings.TrimSuffix(host, ".")
		}
		if (scheme == "http" && p == "80") || (scheme == "https" && p == "443") {
			return strings.TrimSuffix(h, ".")
		}
		return strings.TrimSuffix(h, ".") + ":" + p
	}
	// 请求 Host 不携带 scheme，两种默认端口都视为匹配。
	return norm(destHost, scheme) == norm(reqHost, "http") ||
		norm(destHost, scheme) == norm(reqHost, "https")
}
