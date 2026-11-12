package netsec

import (
	"net/url"
	"strings"
)

// OriginKind 描述一个有效来源(effective origin)的种类。
type OriginKind int

const (
	// OriginOpaque 表示不透明来源:data: URL、无法解析或没有继承发起者的
	// about:blank/srcdoc/blob 文档都归入此类。不透明来源之间互不相同,
	// 能力表对其一律默认拒绝。
	OriginOpaque OriginKind = iota
	// OriginTuple 表示由 (scheme, host, port) 三元组标识的常规来源。
	OriginTuple
)

// Origin 是经过解析后的有效来源。
//
// 不透明来源的 Tuple 字段为零值且 Kind == OriginOpaque;两个不透明来源
// 永远不 Equal,即使它们的零值字段看起来相同。
type Origin struct {
	Kind   OriginKind
	Scheme string
	Host   string
	Port   string
}

// OpaqueOrigin 返回一个新的不透明来源。不透明来源之间彼此不相等,
// 调用方应持有返回值并通过 Equal 比较。
func OpaqueOrigin() Origin {
	return Origin{Kind: OriginOpaque}
}

// NewTupleOrigin 用三元组构造常规来源。scheme 与 host 会被规范化为小写,
// 端口保留字符串形式(空串表示该 scheme 的默认端口)。
func NewTupleOrigin(scheme, host, port string) Origin {
	return Origin{
		Kind:   OriginTuple,
		Scheme: strings.ToLower(scheme),
		Host:   strings.ToLower(host),
		Port:   port,
	}
}

// IsOpaque 报告该来源是否为不透明来源。
func (o Origin) IsOpaque() bool {
	return o.Kind == OriginOpaque
}

// Equal 报告两个有效来源是否相同。不透明来源永远不等于任何来源
// (包括另一个不透明来源),这与浏览器的不透明来源语义一致。
func (o Origin) Equal(other Origin) bool {
	if o.Kind != OriginTuple || other.Kind != OriginTuple {
		return false
	}
	return o.Scheme == other.Scheme && o.Host == other.Host && o.Port == other.Port
}

// String 返回来源的可调试表示,不参与安全判定。
func (o Origin) String() string {
	if o.Kind == OriginOpaque {
		return "opaque"
	}
	if o.Port != "" {
		return o.Scheme + "://" + o.Host + ":" + o.Port
	}
	return o.Scheme + "://" + o.Host
}

// NavigationRequest 描述一次帧导航,用于计算新文档的有效来源。
//
// URL 是即将提交的文档地址;Initiator 是发起该导航的文档来源。
// 主帧导航没有脚本发起者时可传入零值 Origin(此时 about:blank/srcdoc/blob
// 退化为不透明来源)。
type NavigationRequest struct {
	URL       string
	Initiator Origin
}

// ResolveEffectiveOrigin 按浏览器嵌入规则计算一次导航产生文档的有效来源。
//
// 继承规则:
//   - about:blank 与 about:srcdoc 继承发起者来源,发起者不透明或缺失则不透明;
//   - blob: 继承其 blob URL 标识的创建者来源(此处以导航发起者表示),
//     发起者不透明或缺失则不透明;
//   - data: 以及其它无法形成三元组来源的文档,一律为不透明来源;
//   - 常规层级 URL(http/https 等)形成 (scheme, host, port) 三元组。
func ResolveEffectiveOrigin(req NavigationRequest) Origin {
	u, err := url.Parse(req.URL)
	if err != nil {
		return OpaqueOrigin()
	}

	switch {
	case u.Scheme == "about":
		// about:blank、about:srcdoc 以及其它 about 文档都继承发起者来源。
		return inheritInitiator(req.Initiator)
	case u.Scheme == "blob":
		return resolveBlobOrigin(u, req.Initiator)
	case u.Scheme == "data":
		// data: 文档始终拥有全新的不透明来源。
		return OpaqueOrigin()
	case u.Scheme == "" && u.Host == "":
		// 相对/无 scheme 的裸字符串无法形成安全来源。
		return OpaqueOrigin()
	default:
		return tupleOrigin(u)
	}
}

// inheritInitiator 复制发起者来源:不透明发起者产生新的不透明来源。
func inheritInitiator(initiator Origin) Origin {
	if initiator.Kind != OriginTuple {
		return OpaqueOrigin()
	}
	return NewTupleOrigin(initiator.Scheme, initiator.Host, initiator.Port)
}

// resolveBlobOrigin 处理 blob:<creator-origin>/<uuid> 形式的 URL。
// 优先采用 blob URL 内嵌的创建者来源;内嵌来源缺失或无法解析时,
// 回退到导航发起者来源。两者都没有可信三元组时返回不透明来源。
func resolveBlobOrigin(u *url.URL, initiator Origin) Origin {
	inner := u.Opaque
	if inner == "" {
		inner = strings.TrimPrefix(u.String(), "blob:")
	}
	if parsed, perr := url.Parse(inner); perr == nil && parsed.Scheme != "" && parsed.Host != "" {
		if origin := tupleOrigin(parsed); origin.Kind == OriginTuple {
			return origin
		}
	}
	return inheritInitiator(initiator)
}

// tupleOrigin 从层级 URL 提取三元组来源;无法形成主机来源时返回不透明来源。
func tupleOrigin(u *url.URL) Origin {
	if u.Scheme == "" || u.Host == "" {
		return OpaqueOrigin()
	}
	host := u.Hostname()
	if host == "" {
		return OpaqueOrigin()
	}
	return NewTupleOrigin(u.Scheme, host, u.Port())
}
