package cors

// Input 是一次离线复盘的完整输入：路由（服务端行为）+ 请求序列。
type Input struct {
	// PreflightEnforcement 取值 modern（默认，Fetch 现行规范）或 legacy
	//（旧浏览器：非凭据预检不强制校验 Access-Control-Allow-Origin）。
	PreflightEnforcement string `json:"preflight_enforcement,omitempty"`

	// MaxAgeCapSeconds 对 Access-Control-Max-Age 做上限裁剪；nil 用默认 7200，
	// 显式传 0 表示不裁剪。
	MaxAgeCapSeconds *int64 `json:"max_age_cap_seconds,omitempty"`

	Routes   []Route   `json:"routes"`
	Requests []Request `json:"requests"`
}

// Route 描述目标源上某条路径的服务端行为。
type Route struct {
	ID        string         `json:"id"`
	Origin    string         `json:"origin"`
	Path      string         `json:"path"` // 精确路径，或以 /* 结尾的前缀
	Preflight *PreflightSpec `json:"preflight,omitempty"`
	Reply     *ReplySpec     `json:"reply"`
}

// PreflightSpec 描述 OPTIONS 预检响应，可随虚拟时间变化。
type PreflightSpec struct {
	Status  int               `json:"status,omitempty"` // 默认 204
	Headers map[string]string `json:"headers,omitempty"`
	Changes []PreflightChange `json:"changes,omitempty"`
}

// PreflightChange 在 AfterSeconds 之后修改预检响应，用于构造“预检过期后变脸”。
type PreflightChange struct {
	AfterSeconds int64             `json:"after_seconds"`
	Status       int               `json:"status,omitempty"` // 0 表示不变
	Headers      map[string]string `json:"headers,omitempty"`
	Replace      bool              `json:"replace,omitempty"` // true 则替换而非合并头
}

// ReplySpec 描述实际（非预检）响应。
type ReplySpec struct {
	Status   int               `json:"status"` // 默认 200
	Headers  map[string]string `json:"headers,omitempty"`
	Location string            `json:"location,omitempty"` // 3xx 时的跳转地址
	Body     string            `json:"body,omitempty"`
}

// Request 是请求序列中的一次发起。
type Request struct {
	ID          string            `json:"id"`
	AtSeconds   int64             `json:"at_seconds"`
	Source      string            `json:"source"` // 发起源，特殊值 "null" 表示不透明源
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers,omitempty"`
	Credentials string            `json:"credentials,omitempty"` // omit（默认）或 include
	Note        string            `json:"note,omitempty"`
}

// At 返回某一虚拟时刻生效的预检响应。
func (p *PreflightSpec) At(now int64) (int, map[string]string) {
	status := p.Status
	if status == 0 {
		status = 204
	}
	headers := cloneMap(p.Headers)
	for _, change := range p.Changes {
		if change.AfterSeconds > now {
			continue
		}
		if change.Status != 0 {
			status = change.Status
		}
		if change.Replace {
			headers = map[string]string{}
		}
		for k, v := range change.Headers {
			headers[k] = v
		}
	}
	return status, headers
}

func (r *ReplySpec) statusOrOK() int {
	if r.Status == 0 {
		return 200
	}
	return r.Status
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
