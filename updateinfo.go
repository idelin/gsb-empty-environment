package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// TransportGitHubZsync 是唯一支持的更新源传输方式。
const TransportGitHubZsync = "gh-releases-zsync"

var (
	ErrBadUpdateInfo = errors.New("netsec: malformed update info")
	ErrBadTransport  = errors.New("netsec: unsupported update transport")
	ErrNotHTTPS      = errors.New("netsec: remote URL must use https")
)

var (
	ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	tagRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// UpdateInfo 是 .upd_info 节的解析结果：
// gh-releases-zsync|所有者|仓库|latest|文件名通配
type UpdateInfo struct {
	Owner   string
	Repo    string
	Tag     string
	Pattern string
}

// ParseUpdateInfo 解析并校验 .upd_info 内容。
func ParseUpdateInfo(raw string) (*UpdateInfo, error) {
	raw = strings.TrimRight(raw, "\x00\r\n\t ")
	parts := strings.Split(raw, "|")
	if len(parts) != 5 {
		return nil, fmt.Errorf("%w: want 5 fields, got %d", ErrBadUpdateInfo, len(parts))
	}
	if parts[0] != TransportGitHubZsync {
		return nil, fmt.Errorf("%w: %q", ErrBadTransport, parts[0])
	}
	info := &UpdateInfo{
		Owner:   parts[1],
		Repo:    parts[2],
		Tag:     parts[3],
		Pattern: parts[4],
	}
	if !ownerRepoRe.MatchString(info.Owner) || !ownerRepoRe.MatchString(info.Repo) {
		return nil, fmt.Errorf("%w: bad owner/repo", ErrBadUpdateInfo)
	}
	if !tagRe.MatchString(info.Tag) || strings.Contains(info.Tag, "..") {
		return nil, fmt.Errorf("%w: bad tag %q", ErrBadUpdateInfo, info.Tag)
	}
	if info.Pattern == "" || strings.ContainsAny(info.Pattern, "/\x00") {
		return nil, fmt.Errorf("%w: bad pattern %q", ErrBadUpdateInfo, info.Pattern)
	}
	return info, nil
}

// Match 判断文件名是否匹配通配模式：整串匹配，* 与 ? 均不跨 /。
func (u *UpdateInfo) Match(filename string) bool {
	return globMatch(u.Pattern, filename)
}

// DownloadURL 返回匹配文件的 https 下载地址。
func (u *UpdateInfo) DownloadURL(filename string) (string, error) {
	if !u.Match(filename) {
		return "", fmt.Errorf("%w: %q does not match %q", ErrBadUpdateInfo, filename, u.Pattern)
	}
	base := "https://github.com/" + u.Owner + "/" + u.Repo
	if u.Tag == "latest" {
		return base + "/releases/latest/download/" + filename, nil
	}
	return base + "/releases/download/" + u.Tag + "/" + filename, nil
}

// ZsyncURL 返回匹配文件的 https zsync 地址。
func (u *UpdateInfo) ZsyncURL(filename string) (string, error) {
	dl, err := u.DownloadURL(filename)
	if err != nil {
		return "", err
	}
	return dl + ".zsync", nil
}

// CheckRemoteURL 校验下载或 zsync 地址只许 https。
func CheckRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotHTTPS, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrNotHTTPS, raw)
	}
	return nil
}

// globMatch 整串匹配：* 匹配任意不含 / 的序列，? 匹配单个非 / 字符。
func globMatch(pattern, name string) bool {
	p, n := 0, 0
	star, starN := -1, 0
	for n < len(name) {
		if p < len(pattern) && (pattern[p] == '?' && name[n] != '/' || pattern[p] == name[n]) {
			p++
			n++
			continue
		}
		if p < len(pattern) && pattern[p] == '*' {
			star = p
			starN = n
			p++
			continue
		}
		if star >= 0 && name[starN] != '/' {
			starN++
			n = starN
			p = star + 1
			continue
		}
		return false
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
