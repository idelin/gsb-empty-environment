package cors

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	EnforcementModern = "modern"
	EnforcementLegacy = "legacy"

	HeaderOrigin       = "Origin"
	HeaderACAO         = "Access-Control-Allow-Origin"
	HeaderACAC         = "Access-Control-Allow-Credentials"
	HeaderACAM         = "Access-Control-Allow-Methods"
	HeaderACAH         = "Access-Control-Allow-Headers"
	HeaderACEH         = "Access-Control-Expose-Headers"
	HeaderACMA         = "Access-Control-Max-Age"
	HeaderReqMethod    = "Access-Control-Request-Method"
	HeaderReqHeaders   = "Access-Control-Request-Headers"
	HeaderACAPN        = "Access-Control-Allow-Private-Network"
	defaultMaxAgeCap   = 7200
	maxRedirectHops    = 10
	opaqueOriginMarker = "null"
)

// 经典 CORS 安全方法集合：只有这三种方法的请求才可能跳过预检。
var safelistedMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true,
}

// 经典 CORS 安全请求头（按字节比较、大小写不敏感）。
var safelistedHeaders = map[string]bool{
	"accept":           true,
	"accept-language":  true,
	"content-language": true,
	"content-type":     true,
	"range":            true,
}

// content-type 安全值的媒体类型部分。
var safelistedContentTypes = map[string]bool{
	"application/x-www-form-urlencoded": true,
	"multipart/form-data":               true,
	"text/plain":                        true,
}

// Fetch 规范中的 forbidden request-header name：脚本无法设置，不参与预检判定。
var forbiddenRequestHeaders = map[string]bool{
	"accept-charset":                 true,
	"accept-encoding":                true,
	"access-control-request-headers": true,
	"access-control-request-method":  true,
	"connection":                     true,
	"content-length":                 true,
	"cookie":                         true,
	"cookie2":                        true,
	"date":                           true,
	"dnt":                            true,
	"expect":                         true,
	"host":                           true,
	"keep-alive":                     true,
	"origin":                         true,
	"referer":                        true,
	"set-cookie":                     true,
	"te":                             true,
	"trailer":                        true,
	"transfer-encoding":              true,
	"upgrade":                        true,
	"via":                            true,
}

// CORS 响应默认对脚本暴露的“CORS 安全响应头”。
var corsSafelistedResponseHeaders = map[string]bool{
	"cache-control":    true,
	"content-language": true,
	"content-length":   true,
	"content-type":     true,
	"expires":          true,
	"last-modified":    true,
	"pragma":           true,
}

// Origin 是规范化后的源：Scheme+Host（含端口），或不透明源。
type Origin struct {
	Scheme string
	Host   string
	Opaque bool
}

// ParseOrigin 解析 "scheme://host[:port]"，"null" 视为不透明源。
func ParseOrigin(raw string) (Origin, error) {
	if raw == opaqueOriginMarker {
		return Origin{Opaque: true}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Origin{}, fmt.Errorf("origin %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return Origin{}, fmt.Errorf("origin %q 必须是 scheme://host[:port] 形式", raw)
	}
	host := strings.ToLower(u.Host)
	// 归一化默认端口，使 https://a:443 与 https://a 互为同源。
	if h, port, err := net.SplitHostPort(host); err == nil {
		if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
			host = strings.ToLower(h)
		}
	}
	return Origin{Scheme: strings.ToLower(u.Scheme), Host: host}, nil
}

func (o Origin) String() string {
	if o.Opaque {
		return opaqueOriginMarker
	}
	return o.Scheme + "://" + o.Host
}

func (o Origin) sameAs(other Origin) bool {
	if o.Opaque || other.Opaque {
		return false
	}
	return o.Scheme == other.Scheme && o.Host == other.Host
}

// headerGet 大小写不敏感地取单个头。
func headerGet(headers map[string]string, name string) (string, bool) {
	lower := strings.ToLower(name)
	for k, v := range headers {
		if strings.ToLower(k) == lower {
			return v, true
		}
	}
	return "", false
}

// splitHeaderList 按 Fetch 的 ABNF 拆分逗号分隔头列表：去参数、trim、小写。
func splitHeaderList(value string) []string {
	var items []string
	for _, part := range strings.Split(value, ",") {
		item := strings.ToLower(strings.TrimSpace(part))
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

// NeedsPreflight 判断实际请求是否会触发预检（Fetch: CORS-preflight fetch）。
func NeedsPreflight(method string, headers map[string]string) bool {
	if !safelistedMethods[strings.ToUpper(method)] {
		return true
	}
	for name, value := range headers {
		lower := strings.ToLower(name)
		// 禁止修改的头由浏览器/协议接管，不参与 CORS 简单请求判定。
		if forbiddenRequestHeaders[lower] {
			continue
		}
		if !safelistedHeaders[lower] {
			return true
		}
		if lower == "content-type" {
			media := strings.TrimSpace(strings.ToLower(strings.Split(value, ";")[0]))
			if !safelistedContentTypes[media] {
				return true
			}
		}
	}
	return false
}

// PreflightVerdict 是一次 OPTIONS 预检的判定。
type PreflightVerdict struct {
	Fetched        bool     `json:"fetched"`
	CacheHit       bool     `json:"cache_hit"`
	Status         int      `json:"status,omitempty"`
	OK             bool     `json:"ok"`
	FailureReasons []string `json:"failure_reasons,omitempty"`
	MaxAgeSeconds  int64    `json:"max_age_seconds,omitempty"`
}

// ValidatePreflight 按 Fetch 现行规范校验预检响应。
func ValidatePreflight(status int, headers map[string]string, source Origin, method string,
	reqHeaders []string, credentials bool, enforcement string, maxAgeCap int64) PreflightVerdict {

	v := PreflightVerdict{Fetched: true, Status: status}

	if status < 200 || status > 299 {
		v.FailureReasons = append(v.FailureReasons,
			fmt.Sprintf("预检 HTTP 状态 %d 不是 2xx", status))
	}

	acao, hasACAO := headerGet(headers, HeaderACAO)
	allowsOrigin := hasACAO && originAllowed(acao, source)
	// 旧浏览器在非凭据预检中不强制 ACAO；现行规范始终强制。
	enforceACAO := enforcement != EnforcementLegacy || credentials
	if enforceACAO && !allowsOrigin {
		switch {
		case !hasACAO:
			v.FailureReasons = append(v.FailureReasons, "预检缺少 Access-Control-Allow-Origin")
		case credentials && strings.TrimSpace(acao) == "*":
			v.FailureReasons = append(v.FailureReasons,
				"预检 Access-Control-Allow-Origin 为 *，凭据模式下不允许通配")
		default:
			v.FailureReasons = append(v.FailureReasons,
				fmt.Sprintf("预检 Access-Control-Allow-Origin %q 不匹配发起源 %s", acao, source))
		}
	}

	acac, _ := headerGet(headers, HeaderACAC)
	if credentials && !truthyHeader(acac) {
		v.FailureReasons = append(v.FailureReasons,
			"凭据预检要求 Access-Control-Allow-Credentials: true")
	}

	if !methodListed(headerValue(headers, HeaderACAM), method) {
		v.FailureReasons = append(v.FailureReasons,
			fmt.Sprintf("预检 Access-Control-Allow-Methods 未列出方法 %s", strings.ToUpper(method)))
	}

	allowedHeaderSet := headerSet(headers, HeaderACAH)
	headerWildcard := allowedHeaderSet["*"]
	for _, requested := range reqHeaders {
		if !allowedHeaderSet[requested] && !headerWildcard {
			v.FailureReasons = append(v.FailureReasons,
				fmt.Sprintf("预检 Access-Control-Allow-Headers 未列出头 %s", requested))
		}
	}

	// 现行规范（Chrome 144+ 行为）：凭据预检中 ACAM/ACAH 的通配符不能代替显式列举。
	if credentials {
		if usesWildcard(headers, HeaderACAM) {
			v.FailureReasons = append(v.FailureReasons,
				"凭据预检中 Access-Control-Allow-Methods 的 * 不能覆盖具体方法")
		}
		if usesWildcard(headers, HeaderACAH) {
			v.FailureReasons = append(v.FailureReasons,
				"凭据预检中 Access-Control-Allow-Headers 的 * 不能覆盖具体头")
		}
	}

	if raw, ok := headerGet(headers, HeaderACMA); ok {
		if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && seconds >= 0 {
			if maxAgeCap > 0 && seconds > maxAgeCap {
				seconds = maxAgeCap
			}
			v.MaxAgeSeconds = seconds
		}
	}

	v.OK = len(v.FailureReasons) == 0
	return v
}

// ActualVerdict 是实际响应 CORS 检查（决定脚本能读回什么）。
type ActualVerdict struct {
	Status         int      `json:"status"`
	Readable       bool     `json:"readable"`
	FailureReasons []string `json:"failure_reasons,omitempty"`
	ExposedHeaders []string `json:"exposed_headers,omitempty"`
	BodyReadable   bool     `json:"body_readable"`
}

// ValidateActual 校验实际（非预检）响应并计算可读头集合。
func ValidateActual(status int, headers map[string]string, source Origin,
	credentials bool, requestedHeaders map[string]string) ActualVerdict {

	v := ActualVerdict{Status: status}

	acao, hasACAO := headerGet(headers, HeaderACAO)
	if !hasACAO || !originAllowed(acao, source) {
		if !hasACAO {
			v.FailureReasons = append(v.FailureReasons, "响应缺少 Access-Control-Allow-Origin")
		} else if credentials && strings.TrimSpace(acao) == "*" {
			v.FailureReasons = append(v.FailureReasons,
				"凭据请求的响应 Access-Control-Allow-Origin 为 *，通配源不能配合凭据")
		} else {
			v.FailureReasons = append(v.FailureReasons,
				fmt.Sprintf("响应 Access-Control-Allow-Origin %q 不匹配发起源 %s", acao, source))
		}
	}

	acac, _ := headerGet(headers, HeaderACAC)
	if credentials && !truthyHeader(acac) {
		v.FailureReasons = append(v.FailureReasons,
			"凭据请求要求响应携带 Access-Control-Allow-Credentials: true")
	}

	v.Readable = len(v.FailureReasons) == 0
	v.BodyReadable = v.Readable

	if v.Readable {
		exposed := map[string]bool{}
		for name := range corsSafelistedResponseHeaders {
			exposed[name] = true
		}
		wildcardExpose := false
		for _, item := range headerList(headers, HeaderACEH) {
			if item == "*" {
				wildcardExpose = true
				continue
			}
			exposed[item] = true
		}
		for name := range requestedHeaders {
			lower := strings.ToLower(name)
			if wildcardExpose && !isAuthHeader(lower) {
				exposed[lower] = true
			}
		}
		for name := range exposed {
			v.ExposedHeaders = append(v.ExposedHeaders, name)
		}
	}
	return v
}

func originAllowed(acao string, source Origin) bool {
	value := strings.TrimSpace(acao)
	if value == "*" {
		// 通配符不匹配不透明源（"null" 必须显式回显 "null"）。
		return !source.Opaque
	}
	if value == opaqueOriginMarker {
		// 服务端显式回显字符串 "null" 时，匹配不透明源。
		return source.Opaque
	}
	allowed, err := ParseOrigin(value)
	if err != nil {
		return false
	}
	return allowed.sameAs(source)
}

func truthyHeader(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

func headerValue(headers map[string]string, name string) string {
	v, _ := headerGet(headers, name)
	return v
}

func headerList(headers map[string]string, name string) []string {
	if v, ok := headerGet(headers, name); ok {
		return splitHeaderList(v)
	}
	return nil
}

func headerSet(headers map[string]string, name string) map[string]bool {
	set := map[string]bool{}
	for _, item := range headerList(headers, name) {
		set[item] = true
	}
	return set
}

func usesWildcard(headers map[string]string, name string) bool {
	for _, item := range headerList(headers, name) {
		if item == "*" {
			return true
		}
	}
	return false
}

func methodListed(acam string, method string) bool {
	upper := strings.ToUpper(method)
	for _, item := range splitHeaderList(acam) {
		if item == strings.ToLower(upper) || item == "*" {
			return true
		}
	}
	return false
}

func isAuthHeader(lowerName string) bool {
	return lowerName == "authorization" || strings.HasPrefix(lowerName, "proxy-authorization")
}

// MaxAgeCap 返回输入配置的 Max-Age 上限。
func MaxAgeCap(in *Input) int64 {
	if in.MaxAgeCapSeconds != nil {
		return *in.MaxAgeCapSeconds
	}
	return defaultMaxAgeCap
}

// MaxRedirectHops 暴露重定向上限给引擎。
func MaxRedirectHops() int { return maxRedirectHops }
