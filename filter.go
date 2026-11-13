package netsec

import (
	"net/url"
	"strings"
)

// pathKind 标识 data 元素上路径属性的种类。
type pathKind int

const (
	pathExact pathKind = iota
	pathPrefix
	pathSuffix
	pathPattern
)

// pathSpec 是一条路径匹配规则及其种类。
type pathSpec struct {
	kind  pathKind
	value string
}

// Data 对应 intent-filter 中的一个 data 元素。
type Data struct {
	Scheme      string
	Host        string
	Path        string
	PathPrefix  string
	PathPattern string
	PathSuffix  string
}

// Filter 对应一个 intent-filter，可包含多个 data 元素。
// 匹配时同一 filter 内所有 data 元素的 scheme、host 与路径规则
// 取笛卡尔积组合：任一 scheme、任一 host 与任一路径规则组合
// 成功即视为整个 filter 匹配。
type Filter struct {
	Name string
	Data []Data
}

// URL 是待判定的一条 URL（通常由攻击者构造）。
type URL struct {
	Scheme string
	Host   string
	Path   string
}

// ParseURL 解析 URL 字符串，提取 scheme、host（不含端口）与 path。
func ParseURL(raw string) (URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return URL{}, err
	}
	return URL{Scheme: u.Scheme, Host: u.Hostname(), Path: u.Path}, nil
}

// Match 是 filter 命中结果中的一组 scheme/host 组合。
type Match struct {
	Filter string
	Scheme string
	Host   string
}

// schemes 返回 filter 内所有 data 元素声明的去重 scheme。
func (f Filter) schemes() []string {
	return uniqueStrings(func(d Data) string { return d.Scheme }, f.Data)
}

// hosts 返回 filter 内所有 data 元素声明的去重 host。
func (f Filter) hosts() []string {
	return uniqueStrings(func(d Data) string { return d.Host }, f.Data)
}

// pathSpecs 返回 filter 内所有 data 元素声明的路径规则。
func (f Filter) pathSpecs() []pathSpec {
	var specs []pathSpec
	for _, d := range f.Data {
		switch {
		case d.Path != "":
			specs = append(specs, pathSpec{kind: pathExact, value: d.Path})
		case d.PathPrefix != "":
			specs = append(specs, pathSpec{kind: pathPrefix, value: d.PathPrefix})
		case d.PathPattern != "":
			specs = append(specs, pathSpec{kind: pathPattern, value: d.PathPattern})
		case d.PathSuffix != "":
			specs = append(specs, pathSpec{kind: pathSuffix, value: d.PathSuffix})
		}
	}
	return specs
}

func uniqueStrings(pick func(Data) string, data []Data) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range data {
		v := pick(d)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// matchScheme 比较 scheme，不区分大小写。
func matchScheme(pattern, scheme string) bool {
	return strings.EqualFold(pattern, scheme)
}

// matchHost 比较 host，不区分大小写；pattern 以 "*." 开头时匹配
// 任意前缀子域，即主机名以 ".后缀" 结尾（裸域名本身不匹配）。
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+suffix)
	}
	return pattern == host
}

// matchPath 判断路径是否命中 filter 声明的任一路径规则；
// 未声明任何路径规则时不构成约束。
func (f Filter) matchPath(path string) bool {
	specs := f.pathSpecs()
	if len(specs) == 0 {
		return true
	}
	for _, spec := range specs {
		if matchPathSpec(spec, path) {
			return true
		}
	}
	return false
}

// Matches 返回 filter 命中给定 URL 的所有 scheme/host 组合，
// 空切片表示不匹配。组合来自同一 filter 内全部 data 元素的
// 笛卡尔积；未声明 scheme 或 host 的 filter 不产生任何组合。
func (f Filter) Matches(u URL) []Match {
	if !f.matchPath(u.Path) {
		return nil
	}
	var out []Match
	for _, scheme := range f.schemes() {
		if !matchScheme(scheme, u.Scheme) {
			continue
		}
		for _, host := range f.hosts() {
			if !matchHost(host, u.Host) {
				continue
			}
			out = append(out, Match{Filter: f.Name, Scheme: scheme, Host: host})
		}
	}
	return out
}

// MatchesPath 返回 filter 中路径规则命中给定路径的所有
// scheme/host 组合，用于审查"这条路径会被谁接管"。
func (f Filter) MatchesPath(path string) []Match {
	if !f.matchPath(path) {
		return nil
	}
	var out []Match
	for _, scheme := range f.schemes() {
		for _, host := range f.hosts() {
			out = append(out, Match{Filter: f.Name, Scheme: scheme, Host: host})
		}
	}
	return out
}
