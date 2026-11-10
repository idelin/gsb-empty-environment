package cors

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const (
	StatusOK              = "ok"
	StatusBlocked         = "blocked"          // 预检失败：请求未发出
	StatusOpaque          = "opaque_response"  // 请求已发出，但脚本读不到
	StatusSimulationError = "simulation_error" // 输入建模错误（路由缺失等）

	CredentialsOmit    = "omit"
	CredentialsInclude = "include"
)

// HopDetail 记录一次请求的一跳（重定向会产生多跳）。
type HopDetail struct {
	Hop              int               `json:"hop"`
	Target           string            `json:"target"`
	Method           string            `json:"method"`
	CrossOrigin      bool              `json:"cross_origin"`
	Preflight        *PreflightVerdict `json:"preflight,omitempty"`
	Actual           *ActualVerdict    `json:"actual,omitempty"`
	HTTPStatus       int               `json:"http_status,omitempty"`
	RedirectLocation string            `json:"redirect_location,omitempty"`
}

// RequestReport 是一次发起的完整复盘结论。
type RequestReport struct {
	ID             string      `json:"id"`
	AtSeconds      int64       `json:"at_seconds"`
	Source         string      `json:"source"`
	URL            string      `json:"url"`
	Method         string      `json:"method"`
	Credentials    bool        `json:"credentials"`
	Note           string      `json:"note,omitempty"`
	RequestSent    bool        `json:"request_sent"`
	ScriptReadable bool        `json:"script_readable"`
	Status         string      `json:"status"`
	Reasons        []string    `json:"reasons,omitempty"`
	FinalURL       string      `json:"final_url,omitempty"`
	FinalStatus    int         `json:"final_http_status,omitempty"`
	Hops           []HopDetail `json:"hops"`
}

// RunResult 是整段序列的复盘结果与缓存终态。
type RunResult struct {
	Requests []RequestReport    `json:"requests"`
	Cache    []CacheSnapshotRow `json:"preflight_cache"`
}

// Run 按虚拟时间重放整个请求序列。
func Run(in *Input) (*RunResult, error) {
	enforcement := in.PreflightEnforcement
	if enforcement == "" {
		enforcement = EnforcementModern
	}
	if enforcement != EnforcementModern && enforcement != EnforcementLegacy {
		return nil, fmt.Errorf("未知 preflight_enforcement %q", enforcement)
	}

	order := make([]int, len(in.Requests))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return in.Requests[order[a]].AtSeconds < in.Requests[order[b]].AtSeconds
	})

	cache := NewPreflightCache()
	result := &RunResult{}

	for _, idx := range order {
		req := in.Requests[idx]
		cache.SetTime(req.AtSeconds)
		report, err := replayOne(in, cache, req, enforcement)
		if err != nil {
			return nil, fmt.Errorf("请求 %q: %w", req.ID, err)
		}
		result.Requests = append(result.Requests, report)
	}

	if len(in.Requests) > 0 {
		cache.SetTime(in.Requests[order[len(order)-1]].AtSeconds)
	}
	result.Cache = cache.Snapshot()
	return result, nil
}

func replayOne(in *Input, cache *PreflightCache, req Request, enforcement string) (RequestReport, error) {
	source, err := ParseOrigin(req.Source)
	if err != nil {
		return RequestReport{}, err
	}
	credentials := req.Credentials == CredentialsInclude

	report := RequestReport{
		ID:          req.ID,
		AtSeconds:   req.AtSeconds,
		Source:      source.String(),
		URL:         req.URL,
		Method:      strings.ToUpper(req.Method),
		Credentials: credentials,
		Note:        req.Note,
	}

	currentURL := req.URL
	method := strings.ToUpper(req.Method)
	headers := cloneMap(req.Headers)

	for hop := 1; hop <= MaxRedirectHops(); hop++ {
		targetOrigin, sameOrigin, err := classifyTarget(source, currentURL)
		if err != nil {
			return RequestReport{}, err
		}
		detail := HopDetail{
			Hop:         hop,
			Target:      currentURL,
			Method:      method,
			CrossOrigin: !sameOrigin,
		}

		// 第一步：跨源且非简单请求 → 预检（缓存可能直接放行）。
		if !sameOrigin && NeedsPreflight(method, headers) {
			reqHeaderList := requestHeaderList(headers)
			hit, err := cache.Lookup(source, currentURL, credentials, method, reqHeaderList)
			if err != nil {
				return RequestReport{}, err
			}
			if hit {
				detail.Preflight = &PreflightVerdict{Fetched: false, CacheHit: true, OK: true}
			} else {
				verdict, err := doPreflight(in, cache, source, targetOrigin, currentURL,
					method, reqHeaderList, credentials, enforcement, MaxAgeCap(in))
				if err != nil {
					return RequestReport{}, err
				}
				detail.Preflight = &verdict
				if !verdict.OK {
					report.Hops = append(report.Hops, detail)
					report.Status = StatusBlocked
					report.RequestSent = false
					report.ScriptReadable = false
					report.Reasons = append(report.Reasons, verdict.FailureReasons...)
					return report, nil
				}
			}
		}

		// 第二步：预检通过（或无需预检）→ 实际请求可以发出。
		route := findRoute(in.Routes, targetOrigin, currentURL)
		if route == nil || route.Reply == nil {
			report.Hops = append(report.Hops, detail)
			report.Status = StatusSimulationError
			report.Reasons = append(report.Reasons,
				fmt.Sprintf("缺少目标 %s %s 的响应建模", targetOrigin, currentURL))
			return report, nil
		}

		reply := route.Reply
		detail.HTTPStatus = reply.statusOrOK()

		location := resolveLocation(currentURL, reply.Location)
		if isRedirect(reply.statusOrOK()) && location != "" {
			detail.RedirectLocation = location
			report.Hops = append(report.Hops, detail)
			method, headers = rewriteOnRedirect(method, headers, reply.statusOrOK())
			currentURL = location
			continue
		}

		// 终态响应：同源直接可读；跨源做实际 CORS 检查。
		report.RequestSent = true
		report.FinalURL = currentURL
		report.FinalStatus = reply.statusOrOK()
		if sameOrigin {
			detail.Actual = &ActualVerdict{
				Status:         reply.statusOrOK(),
				Readable:       true,
				BodyReadable:   true,
				ExposedHeaders: allHeaderNames(reply.Headers),
			}
		} else {
			actual := ValidateActual(reply.statusOrOK(), reply.Headers, source, credentials, headers)
			detail.Actual = &actual
			if actual.Readable {
				report.Status = StatusOK
				report.ScriptReadable = true
			} else {
				report.Status = StatusOpaque
				report.ScriptReadable = false
				report.Reasons = append(report.Reasons, actual.FailureReasons...)
			}
		}
		if report.Status == "" {
			report.Status = StatusOK
			report.ScriptReadable = true
		}
		report.Hops = append(report.Hops, detail)
		return report, nil
	}

	report.Status = StatusSimulationError
	report.Reasons = append(report.Reasons, fmt.Sprintf("重定向超过 %d 跳", MaxRedirectHops()))
	return report, nil
}

func doPreflight(in *Input, cache *PreflightCache, source Origin, target Origin, targetURL,
	method string, reqHeaders []string, credentials bool, enforcement string,
	maxAgeCap int64) (PreflightVerdict, error) {

	route := findRoute(in.Routes, target, targetURL)
	if route == nil || route.Preflight == nil {
		return PreflightVerdict{
			Fetched:        true,
			OK:             false,
			FailureReasons: []string{"缺少预检响应建模（OPTIONS 未建模，等同预检失败）"},
		}, nil
	}

	status, headers := route.Preflight.At(cache.now)
	verdict := ValidatePreflight(status, headers, source, method, reqHeaders,
		credentials, enforcement, maxAgeCap)
	if verdict.OK {
		if err := cache.Store(source, targetURL, credentials, method, reqHeaders,
			verdict.MaxAgeSeconds); err != nil {
			return PreflightVerdict{}, err
		}
	}
	return verdict, nil
}

func classifyTarget(source Origin, rawURL string) (Origin, bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Origin{}, false, fmt.Errorf("url %q: %w", rawURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return Origin{}, false, fmt.Errorf("url %q 必须是绝对 URL", rawURL)
	}
	target := Origin{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Host)}
	return target, source.sameAs(target), nil
}

func findRoute(routes []Route, target Origin, rawURL string) *Route {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	for i := range routes {
		route := &routes[i]
		if route.Origin != target.String() {
			continue
		}
		if matchPath(route.Path, path) {
			return route
		}
	}
	return nil
}

func matchPath(pattern, path string) bool {
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "*") // 保留结尾的 /
		return strings.HasPrefix(path, prefix)
	}
	return pattern == path
}

func requestHeaderList(headers map[string]string) []string {
	seen := map[string]bool{}
	var list []string
	for name := range headers {
		lower := strings.ToLower(name)
		// 这些头由浏览器/协议控制，不会出现在 Access-Control-Request-Headers 中。
		if lower == "origin" || lower == "accept-charset" ||
			lower == "accept-encoding" || lower == "connection" ||
			lower == "content-length" || lower == "cookie" || lower == "host" {
			continue
		}
		if !seen[lower] {
			seen[lower] = true
			list = append(list, lower)
		}
	}
	sort.Strings(list)
	return list
}

func isRedirect(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}

func resolveLocation(base, location string) string {
	if location == "" {
		return ""
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return location
	}
	locURL, err := url.Parse(location)
	if err != nil {
		return location
	}
	return baseURL.ResolveReference(locURL).String()
}

// rewriteOnRedirect 模拟 Fetch 的重定向方法改写：
// 301/302/303 一律改 GET（HEAD 除外），307/308 保留方法；各状态均保留请求头。
func rewriteOnRedirect(method string, headers map[string]string, status int) (string, map[string]string) {
	newHeaders := cloneMap(headers)
	switch status {
	case 307, 308:
		return method, newHeaders
	default:
		if method == "HEAD" {
			return method, newHeaders
		}
		return "GET", newHeaders
	}
}

func allHeaderNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, strings.ToLower(name))
	}
	sort.Strings(names)
	return names
}
