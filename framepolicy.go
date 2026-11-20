package netsec

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 判定依据标识，便于网关出口记录审计日志。这些字符串是稳定的 API。
const (
	// ReasonNoPolicy 表示响应没有任何有效的嵌入控制头，按“未声明即允许”处理。
	ReasonNoPolicy = "no-framing-policy"
	// ReasonCSP 表示由 Content-Security-Policy 的 frame-ancestors 决定。
	ReasonCSP = "csp-frame-ancestors"
	// ReasonXFO 表示由 X-Frame-Options 决定（此时不存在生效的 frame-ancestors）。
	ReasonXFO = "x-frame-options"
)

// Decision 是一次嵌入判定的结果。
type Decision struct {
	// Allowed 为 true 时响应页面允许被 ancestors 描述的嵌套链嵌入。
	Allowed bool
	// Mechanism 取值为 ReasonCSP / ReasonXFO / ReasonNoPolicy，标识最终依据。
	Mechanism string
	// Reason 给出判定的具体原因，适合写入日志，不建议作为分支条件。
	Reason string
}

// Evaluate 依据响应头判定页面是否允许被给定的祖先链嵌入。
//
// resource 是被嵌入页面自身的 URL，用于解析 frame-ancestors 中的 'self' 与
// X-Frame-Options: SAMEORIGIN；为 nil 时这类同源比较一律不匹配。
// ancestors 按从外到内的顺序给出嵌套祖先，最后一个元素必须是直接父页面；
// 空切片（或 nil）表示顶层加载，天然允许嵌入。
//
// 判定规则：
//   - 存在生效的 frame-ancestors（仅 Content-Security-Policy，Report-Only 不参与）
//     时完全忽略 X-Frame-Options；多条 CSP 的 frame-ancestors 取交集。
//   - frame-ancestors 支持 'none'、'self'、方案来源、主机来源（可带 *. 通配前缀与
//     端口）；'none' 与其他来源共存时 'none' 被忽略，其余来源照常生效。
//   - X-Frame-Options 只接受恰好一个 DENY 或 SAMEORIGIN（大小写不敏感）；多值
//     （含逗号分隔）或重复头一律视为无效，按拒绝嵌入处理。
//   - 祖先链上每一层都必须通过来源匹配，任一层不匹配即拒绝。
func Evaluate(headers http.Header, resource *url.URL, ancestors []*url.URL) Decision {
	if headers == nil {
		headers = http.Header{}
	}

	policies := collectFrameAncestors(headers)
	if len(policies) > 0 {
		return evaluateCSP(policies, resource, ancestors)
	}
	if len(headers.Values(xfoHeader)) == 0 {
		// 没有 frame-ancestors 也没有 X-Frame-Options：未声明嵌入限制，允许嵌入。
		return Decision{Allowed: true, Mechanism: ReasonNoPolicy, Reason: "no framing policy declared"}
	}
	return evaluateXFO(headers, resource, ancestors)
}

// Allowed 是只需要布尔结论时的便捷封装。
func Allowed(headers http.Header, resource *url.URL, ancestors []*url.URL) bool {
	return Evaluate(headers, resource, ancestors).Allowed
}

// cspSource 表示 frame-ancestors 指令中的一个已解析来源。
type cspSource struct {
	// self 为 true 表示关键字 'self'，匹配时结合被嵌入页面的源解析。
	self bool
	// scheme 为小写、不含冒号的方案；schemeOnly 来源的 host 为空。
	scheme string
	// host 为小写主机名或 IP；schemeOnly 来源为空。
	host string
	// port 为显式数字端口；未显式给出时为空，此时按方案默认端口匹配。
	port string
	// wildcard 为 true 表示主机以 *. 开头（或单独的 *），按 CSP3 可匹配基线主机
	// 本身及其任意子域。
	wildcard bool
	// portWildcard 为 true 表示形如 https://example.com:*，任意端口均可。
	portWildcard bool
	// schemeOnly 为 true 表示纯方案来源，如 https:。
	schemeOnly bool
}

// frameAncestorPolicy 是一条 frame-ancestors 指令解析后的来源集合。
type frameAncestorPolicy struct {
	// noneOnly 表示有效来源只有 'none'，等价于拒绝一切嵌入。
	noneOnly bool
	sources  []cspSource
}

const (
	cspEnforceHeader    = "Content-Security-Policy"
	cspReportOnlyHeader = "Content-Security-Policy-Report-Only"
	xfoHeader           = "X-Frame-Options"
)

// 主机来源解析失败的哨兵错误。
var errMalformedHost = &net.ParseError{Type: "host", Text: "malformed frame-ancestors host"}

// collectFrameAncestors 解析所有强制 CSP 头，返回各自的 frame-ancestors 指令。
// Report-Only 头在此处被直接排除；不含 frame-ancestors 的策略不产生限制，也不
// 参与交集，因此同样不出现在结果中。
func collectFrameAncestors(headers http.Header) []frameAncestorPolicy {
	// 显式确认 Report-Only 不参与判定：其值不读取、不解析。
	_ = headers.Values(cspReportOnlyHeader)

	var policies []frameAncestorPolicy
	for _, raw := range headers.Values(cspEnforceHeader) {
		policy, ok := parseFrameAncestors(raw)
		if ok {
			policies = append(policies, policy)
		}
	}
	return policies
}

// parseFrameAncestors 从单条 CSP 文本中提取 frame-ancestors 指令。
// 返回 ok=false 表示该策略没有有效的 frame-ancestors 指令，不构成嵌入限制。
func parseFrameAncestors(cspText string) (frameAncestorPolicy, bool) {
	directive, found := "", false
	for _, segment := range strings.Split(cspText, ";") {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		if !strings.EqualFold(fields[0], "frame-ancestors") {
			continue
		}
		// 同一策略内指令重复时，首个 frame-ancestors 生效（与 CSP 指令去重规则一致）。
		if found {
			continue
		}
		found = true
		directive = strings.Join(fields[1:], " ")
	}
	if !found {
		return frameAncestorPolicy{}, false
	}

	policy := frameAncestorPolicy{}
	for _, token := range strings.Fields(directive) {
		switch {
		case strings.EqualFold(token, "'none'"):
			// 先登记 'none'；若随后出现其他来源，noneOnly 会被清除，
			// 从而实现“'none' 与其他来源共存时 'none' 无效、其他来源仍生效”。
			policy.noneOnly = true
		case strings.EqualFold(token, "'self'"):
			policy.noneOnly = false
			policy.sources = append(policy.sources, cspSource{self: true})
		default:
			src, ok := parseSource(token)
			if !ok {
				// 无法识别的畸形来源表达式按 CSP 解析规则忽略，其余来源继续生效。
				continue
			}
			policy.noneOnly = false
			policy.sources = append(policy.sources, src)
		}
	}
	if !policy.noneOnly && len(policy.sources) == 0 {
		// 指令存在但没有任何有效来源（例如全是畸形表达式），不产生可执行限制。
		return frameAncestorPolicy{}, false
	}
	return policy, true
}

// parseSource 解析 frame-ancestors 的单个来源 token。
func parseSource(token string) (cspSource, bool) {
	// 带引号的 token 只可能是 'none'/'self'，二者已在调用处处理；其余均非法。
	if strings.HasPrefix(token, "'") {
		return cspSource{}, false
	}

	// 纯方案来源：以冒号结尾且冒号前是合法 scheme，如 https:。
	if i := len(token) - 1; i >= 1 && token[i] == ':' && !strings.Contains(token, "/") {
		scheme := token[:i]
		if isScheme(scheme) {
			return cspSource{scheme: strings.ToLower(scheme), schemeOnly: true}, true
		}
		return cspSource{}, false
	}

	rest := token
	src := cspSource{}
	if i := strings.Index(token, "://"); i >= 0 {
		scheme := token[:i]
		if !isScheme(scheme) {
			return cspSource{}, false
		}
		src.scheme = strings.ToLower(scheme)
		rest = token[i+3:]
	}

	// 主机来源不允许携带用户信息；frame-ancestors 不使用路径，仅容忍结尾的单个 "/"。
	if strings.Contains(rest, "@") {
		return cspSource{}, false
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		if rest[slash+1:] != "" {
			return cspSource{}, false
		}
		rest = rest[:slash]
	}
	if rest == "" {
		return cspSource{}, false
	}

	host, port, hasPort, err := splitHostPort(rest)
	if err != nil || host == "" {
		return cspSource{}, false
	}

	wildcard := false
	if host == "*" {
		// 单独的 * 表示任意主机，没有可校验的基线主机名。
		wildcard = true
	} else {
		if strings.HasPrefix(host, "*.") {
			wildcard = true
			host = host[2:]
		}
		if !validHost(host) {
			return cspSource{}, false
		}
		src.host = strings.ToLower(host)
	}
	src.wildcard = wildcard

	if hasPort {
		switch {
		case port == "*":
			src.portWildcard = true
		case isDigits(port):
			src.port = port
		default:
			return cspSource{}, false
		}
	}
	return src, true
}

// splitHostPort 处理普通主机与 [IPv6] 主机，hasPort 表示是否显式给出端口。
func splitHostPort(hostPort string) (host, port string, hasPort bool, err error) {
	if strings.HasPrefix(hostPort, "[") {
		end := strings.IndexByte(hostPort, ']')
		if end < 1 {
			return "", "", false, errMalformedHost
		}
		host = hostPort[1:end]
		rest := hostPort[end+1:]
		if rest == "" {
			return host, "", false, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", "", false, errMalformedHost
		}
		return host, rest[1:], true, nil
	}

	if colon := strings.IndexByte(hostPort, ':'); colon >= 0 {
		return hostPort[:colon], hostPort[colon+1:], true, nil
	}
	return hostPort, "", false, nil
}

// validHost 接受域名（含单标签主机）与 IP 字面量，拒绝空标签与非法字符。
func validHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			// 域名标签允许字母、数字与连字符，连字符不得位于首尾（宽松放行首尾，
			// 安全方向上不会扩大可嵌入范围）。
			if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') &&
				!(c >= '0' && c <= '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func isScheme(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// evaluateCSP 对多条 frame-ancestors 策略取交集：单条策略内各来源为 OR 关系，
// 祖先链的每一层都必须被所有策略放行。
func evaluateCSP(policies []frameAncestorPolicy, resource *url.URL, ancestors []*url.URL) Decision {
	for depth, ancestor := range ancestors {
		for _, policy := range policies {
			if !policy.allows(ancestor, resource) {
				return Decision{
					Allowed:   false,
					Mechanism: ReasonCSP,
					Reason:    "frame-ancestors: ancestor at depth " + strconv.Itoa(depth) + " not in allowed source set",
				}
			}
		}
	}
	return Decision{Allowed: true, Mechanism: ReasonCSP, Reason: "all ancestors match frame-ancestors"}
}

func (p frameAncestorPolicy) allows(ancestor, resource *url.URL) bool {
	if p.noneOnly {
		return false
	}
	for _, src := range p.sources {
		if src.match(ancestor, resource) {
			return true
		}
	}
	return false
}

func (s cspSource) match(ancestor, resource *url.URL) bool {
	if ancestor == nil || ancestor.Host == "" || ancestor.Scheme == "" {
		return false
	}
	ancestorScheme := strings.ToLower(ancestor.Scheme)
	ancestorHost := strings.ToLower(ancestor.Hostname())
	ancestorPort := ancestor.Port()
	if ancestorPort == "" {
		ancestorPort = defaultPort(ancestorScheme)
	}

	if s.self {
		if resource == nil || resource.Host == "" {
			return false
		}
		return strings.EqualFold(ancestor.Scheme, resource.Scheme) &&
			strings.EqualFold(ancestor.Hostname(), resource.Hostname()) &&
			effectivePort(ancestor) == effectivePort(resource)
	}

	if s.schemeOnly {
		return ancestorScheme == s.scheme
	}

	if s.scheme != "" && ancestorScheme != s.scheme {
		return false
	}
	if !hostMatches(ancestorHost, s.host, s.wildcard) {
		return false
	}
	if s.portWildcard {
		return true
	}
	if s.port == "" {
		// 未指定端口：仅匹配该方案的默认端口。
		return ancestorPort == defaultPort(ancestorScheme)
	}
	return ancestorPort == s.port
}

// hostMatches 处理 *. 通配前缀。按 CSP3，通配主机可匹配基线主机本身及其任意子域。
func hostMatches(ancestorHost, baseHost string, wildcard bool) bool {
	if ancestorHost == baseHost {
		return true
	}
	if !wildcard {
		return false
	}
	if baseHost == "" {
		// 裸 * 通配主机：任意主机均可，方案与端口已在调用处另行校验。
		return ancestorHost != ""
	}
	return strings.HasSuffix(ancestorHost, "."+baseHost)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return defaultPort(strings.ToLower(u.Scheme))
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http", "ws":
		return "80"
	case "https", "wss":
		return "443"
	case "ftp":
		return "21"
	default:
		return ""
	}
}

// evaluateXFO 在没有生效 frame-ancestors 时执行 X-Frame-Options 判定。
func evaluateXFO(headers http.Header, resource *url.URL, ancestors []*url.URL) Decision {
	token, valid := singleXFOToken(headers.Values(xfoHeader))
	if !valid {
		// 重复头、逗号多值、空值或未知指令：本题规定一律按拒绝嵌入处理。
		return Decision{
			Allowed:   false,
			Mechanism: ReasonXFO,
			Reason:    "invalid or repeated X-Frame-Options header; failing closed",
		}
	}
	switch token {
	case "DENY":
		if len(ancestors) == 0 {
			return Decision{Allowed: true, Mechanism: ReasonXFO, Reason: "top-level load"}
		}
		return Decision{Allowed: false, Mechanism: ReasonXFO, Reason: "X-Frame-Options: DENY"}
	case "SAMEORIGIN":
		for depth, ancestor := range ancestors {
			if !sameOrigin(ancestor, resource) {
				return Decision{
					Allowed:   false,
					Mechanism: ReasonXFO,
					Reason:    "X-Frame-Options: SAMEORIGIN, ancestor at depth " + strconv.Itoa(depth) + " is cross-origin",
				}
			}
		}
		return Decision{Allowed: true, Mechanism: ReasonXFO, Reason: "X-Frame-Options: SAMEORIGIN"}
	default:
		// singleXFOToken 已限定取值，这里仅作防御性兜底。
		return Decision{Allowed: false, Mechanism: ReasonXFO, Reason: "unsupported X-Frame-Options token"}
	}
}

// singleXFOToken 校验 X-Frame-Options 头：只允许恰好一个头值，且去除空白后
// 恰好是 DENY 或 SAMEORIGIN。逗号分隔、空白混杂的多值以及重复头都判为无效。
func singleXFOToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	raw := values[0]
	if strings.Contains(raw, ",") {
		return "", false
	}
	fields := strings.Fields(raw)
	if len(fields) != 1 {
		return "", false
	}
	token := strings.ToUpper(fields[0])
	if token != "DENY" && token != "SAMEORIGIN" {
		return "", false
	}
	return token, true
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.Host == "" || b.Host == "" {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}
