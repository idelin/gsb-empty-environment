package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
)

// iodef 记录只允许 mailto 与 https 两种 scheme。
const (
	SchemeMailto = "mailto"
	SchemeHTTPS  = "https"
)

// Target 是一个通过校验的 iodef 上报目标。
type Target struct {
	Scheme  string
	Address string   // mailto: 收件人地址
	URL     *url.URL // https: 上报地址
}

// blockedNets 在 IsGlobalUnicast 之外额外排除的保留网段，
// 防止 iodef 指向内网或特殊用途地址。
var blockedNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",     // 本网络
		"100.64.0.0/10", // CGNAT
		"192.0.0.0/24",  // IETF 协议分配
		"198.18.0.0/15", // 基准测试
		"240.0.0.0/4",   // 保留
		"2001:db8::/32", // 文档用途
	}
	var nets []*net.IPNet
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// IsGlobalUnicast 报告 ip 是否为可路由的全局单播地址。
func IsGlobalUnicast(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if !ip.IsGlobalUnicast() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate() {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// ParseIodef 校验单条 iodef 记录，只接受 mailto 与 https。
func ParseIodef(raw string) (Target, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Target{}, fmt.Errorf("iodef: invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case SchemeMailto:
		return parseMailto(u)
	case SchemeHTTPS:
		return parseHTTPS(u)
	default:
		return Target{}, fmt.Errorf("iodef: unsupported scheme %q", u.Scheme)
	}
}

// parseMailto 校验 mailto 目标：不允许 ? 及其后的头字段，
// 只允许一个地址，本地部分不含控制字符。
func parseMailto(u *url.URL) (Target, error) {
	if u.ForceQuery || u.RawQuery != "" || strings.Contains(u.Opaque, "?") {
		return Target{}, errors.New("iodef: mailto header fields (?...) are not allowed")
	}
	if u.Host != "" {
		return Target{}, errors.New("iodef: malformed mailto target")
	}
	rest := u.Opaque
	if rest == "" {
		return Target{}, errors.New("iodef: empty mailto address")
	}
	addr, err := mail.ParseAddress(rest)
	if err != nil {
		return Target{}, fmt.Errorf("iodef: invalid mailto address: %w", err)
	}
	if addr.Name != "" {
		return Target{}, errors.New("iodef: mailto display names are not allowed")
	}
	local, _, ok := strings.Cut(addr.Address, "@")
	if !ok || local == "" {
		return Target{}, errors.New("iodef: mailto address missing local part")
	}
	for _, r := range local {
		if unicode.IsControl(r) {
			return Target{}, errors.New("iodef: control character in mailto local part")
		}
	}
	for _, r := range addr.Address {
		if r == '\r' || r == '\n' {
			return Target{}, errors.New("iodef: CRLF in mailto address")
		}
	}
	return Target{Scheme: SchemeMailto, Address: addr.Address}, nil
}

// parseHTTPS 校验 https 目标：必须为 443 端口且不含 userinfo。
func parseHTTPS(u *url.URL) (Target, error) {
	if u.User != nil {
		return Target{}, errors.New("iodef: userinfo is not allowed")
	}
	if u.Hostname() == "" {
		return Target{}, errors.New("iodef: missing host")
	}
	if p := u.Port(); p != "" && p != "443" {
		return Target{}, fmt.Errorf("iodef: only port 443 is allowed, got %q", p)
	}
	return Target{Scheme: SchemeHTTPS, URL: u}, nil
}
