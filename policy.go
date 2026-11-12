package netsec

import (
	"errors"
	"fmt"
)

// CIP 服务码（取低 7 位，最高位为请求/响应标志）。
const (
	ServiceGetAttributeSingle = 0x0E
	ServiceSetAttributeSingle = 0x10
	ServiceUnconnectedSend    = 0x52
)

// 准入涉及的 CIP 对象类号。
const (
	ClassIdentity          = 0x01
	ClassMessageRouter     = 0x02
	ClassConnectionManager = 0x06
	ClassTCPIPInterface    = 0xF5
)

// Reason 标识拒绝/放行原因，供网关做审计与统计。
type Reason int

const (
	ReasonAllowed Reason = iota
	ReasonMalformedPath
	ReasonUnsupportedService
	ReasonClassNotWhitelisted
	ReasonReadDenied
	ReasonWriteDenied
	ReasonTCPIPWriteBlocked
	ReasonUnconnectedMalformed
	ReasonDepthExceeded
)

// String 返回原因的可读名称。
func (r Reason) String() string {
	switch r {
	case ReasonAllowed:
		return "allowed"
	case ReasonMalformedPath:
		return "malformed-path"
	case ReasonUnsupportedService:
		return "unsupported-service"
	case ReasonClassNotWhitelisted:
		return "class-not-whitelisted"
	case ReasonReadDenied:
		return "read-denied"
	case ReasonWriteDenied:
		return "write-denied"
	case ReasonTCPIPWriteBlocked:
		return "tcpip-set-attribute-blocked"
	case ReasonUnconnectedMalformed:
		return "unconnected-send-malformed"
	case ReasonDepthExceeded:
		return "unconnected-depth-exceeded"
	default:
		return "unknown"
	}
}

// Decision 是一次准入判断的结果。
type Decision struct {
	Allowed bool
	Reason  Reason
	Path    *LogicalPath
	Err     error
}

// Error 使 Decision 同时可作为 error 使用；放行时返回空串。
func (d *Decision) Error() string {
	if d.Allowed {
		return ""
	}
	if d.Err != nil {
		return fmt.Sprintf("%s: %v", d.Reason, d.Err)
	}
	return d.Reason.String()
}

// ClassRule 描述一个白名单类允许的操作。
type ClassRule struct {
	AllowRead  bool
	AllowWrite bool
}

// Policy 是按服务码 + 对象类白名单工作的准入策略。
//
// 安全基线：
//   - 默认拒绝，只有显式列入 Rules 的类才可能放行；
//   - TCP/IP 接口对象（0xF5）可读但 Set_Attribute_Single 恒拒绝，
//     无论该类的 8 位还是 16/32 位逻辑段编码，规范化后一律命中；
//   - Unconnected Send（0x52）只允许发往连接管理器（0x06），
//     内嵌消息按同一策略递归检查，嵌套深度受 MaxDepth 限制。
type Policy struct {
	Rules    map[uint32]ClassRule
	MaxDepth int
}

// DefaultMaxDepth 是 Unconnected Send 内嵌消息的默认最大递归深度。
const DefaultMaxDepth = 4

// DefaultPolicy 返回网关默认策略：身份/连接管理器/TCPIP 只读，
// 消息路由对象允许读写，TCPIP 写操作由硬规则兜底拒绝。
func DefaultPolicy() *Policy {
	return &Policy{
		Rules: map[uint32]ClassRule{
			ClassIdentity:          {AllowRead: true},
			ClassMessageRouter:     {AllowRead: true, AllowWrite: true},
			ClassConnectionManager: {AllowRead: true},
			ClassTCPIPInterface:    {AllowRead: true},
		},
		MaxDepth: DefaultMaxDepth,
	}
}

// NewPolicy 以自定义白名单构造策略，MaxDepth 非法时回退到默认值。
func NewPolicy(rules map[uint32]ClassRule, maxDepth int) *Policy {
	if maxDepth < 0 {
		maxDepth = DefaultMaxDepth
	}
	return &Policy{Rules: rules, MaxDepth: maxDepth}
}

// Request 是网关准入入口收到的一帧 CIP 消息（去掉了封装头/CPF 头之后的部分）。
type Request struct {
	// Service 是请求服务码，最高位的响应标志会被自动屏蔽。
	Service byte
	// PathSizeWords 是请求路径字长（CIP 报文里的 PATH Size，单位为 16 位字）。
	PathSizeWords uint16
	// PathData 是请求路径原始字节，允许其后携带请求数据（Set 的值等）。
	PathData []byte
}

// Admit 对一帧请求做准入判断，返回结构化 Decision（永不返回 nil）。
func (p *Policy) Admit(req *Request) *Decision {
	return p.admit(req, 0)
}

func (p *Policy) admit(req *Request, depth int) *Decision {
	service := req.Service & 0x7F

	lp, err := NormalizeEPATH(req.PathSizeWords, req.PathData)
	if err != nil {
		return &Decision{Allowed: false, Reason: ReasonMalformedPath, Err: err}
	}

	// Unconnected Send 只能投递到连接管理器，内嵌消息递归检查。
	if service == ServiceUnconnectedSend {
		if !lp.HasClass || lp.Class != ClassConnectionManager {
			return &Decision{
				Allowed: false,
				Reason:  ReasonClassNotWhitelisted,
				Path:    lp,
				Err:     fmt.Errorf("Unconnected Send 只允许发往连接管理器类 0x%02X", ClassConnectionManager),
			}
		}
		return p.admitUnconnected(req, lp, depth)
	}

	return p.checkService(service, lp)
}

// checkService 对普通服务按“服务码 + 规范化类号”应用白名单。
func (p *Policy) checkService(service byte, lp *LogicalPath) *Decision {
	write := false
	switch service {
	case ServiceGetAttributeSingle:
		write = false
	case ServiceSetAttributeSingle:
		write = true
	default:
		return &Decision{
			Allowed: false,
			Reason:  ReasonUnsupportedService,
			Path:    lp,
			Err:     fmt.Errorf("服务码 0x%02X 不在支持列表内", service),
		}
	}

	rule, ok := p.Rules[lp.Class]
	if !ok {
		return &Decision{
			Allowed: false,
			Reason:  ReasonClassNotWhitelisted,
			Path:    lp,
			Err:     fmt.Errorf("类 0x%X 不在白名单内", lp.Class),
		}
	}

	// 硬规则：TCP/IP 接口对象的 Set_Attribute_Single 默认拒绝，
	// 不允许通过白名单配置放开。
	if write && lp.Class == ClassTCPIPInterface {
		return &Decision{
			Allowed: false,
			Reason:  ReasonTCPIPWriteBlocked,
			Path:    lp,
			Err:     errors.New("TCP/IP 接口对象禁止 Set_Attribute_Single"),
		}
	}

	if write && !rule.AllowWrite {
		return &Decision{
			Allowed: false,
			Reason:  ReasonWriteDenied,
			Path:    lp,
			Err:     fmt.Errorf("类 0x%X 不允许写操作", lp.Class),
		}
	}
	if !write && !rule.AllowRead {
		return &Decision{
			Allowed: false,
			Reason:  ReasonReadDenied,
			Path:    lp,
			Err:     fmt.Errorf("类 0x%X 不允许读操作", lp.Class),
		}
	}

	return &Decision{Allowed: true, Reason: ReasonAllowed, Path: lp}
}

// admitUnconnected 解析 Unconnected Send 请求体并递归检查内嵌 CIP 消息。
//
// Unconnected Send 请求体（路径已被外层解析消费）：
//
//	priority/time_ticks        1 字节
//	timeout ticks              1 字节
//	message length             2 字节（小端）
//	embedded CIP message       N 字节（service + path size + path + data）
//	... 其后的路由路径等字段与准入无关
func (p *Policy) admitUnconnected(req *Request, outer *LogicalPath, depth int) *Decision {
	maxDepth := p.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	if depth >= maxDepth {
		return &Decision{
			Allowed: false,
			Reason:  ReasonDepthExceeded,
			Path:    outer,
			Err:     fmt.Errorf("内嵌消息深度超过上限 %d", maxDepth),
		}
	}

	body := req.PathData[int(req.PathSizeWords)*2:]

	// priority/time_ticks + timeout ticks + message length(2)。
	const headerLen = 4
	if len(body) < headerLen {
		return &Decision{Allowed: false, Reason: ReasonUnconnectedMalformed, Path: outer,
			Err: errors.New("Unconnected Send 请求头不完整")}
	}

	msgLen := int(body[2]) | int(body[3])<<8
	if msgLen == 0 || headerLen+msgLen > len(body) {
		return &Decision{Allowed: false, Reason: ReasonUnconnectedMalformed, Path: outer,
			Err: fmt.Errorf("内嵌消息长度 %d 越界（请求体 %d 字节）", msgLen, len(body)-headerLen)}
	}

	embedded := body[headerLen : headerLen+msgLen]
	inner, err := parseEmbeddedRequest(embedded)
	if err != nil {
		return &Decision{Allowed: false, Reason: ReasonUnconnectedMalformed, Path: outer, Err: err}
	}

	// 同一策略递归：内嵌的 Unconnected Send 继续受深度计数约束。
	return p.admit(inner, depth+1)
}

// parseEmbeddedRequest 从内嵌 CIP 报文中切出服务码与请求路径。
func parseEmbeddedRequest(msg []byte) (*Request, error) {
	// service(1) + path size(1)，内嵌报文路径字长为单字节。
	const minLen = 2
	if len(msg) < minLen {
		return nil, errors.New("内嵌 CIP 报文缺少服务码/路径字长")
	}

	service := msg[0]
	pathWords := int(msg[1])
	pathStart := 2
	pathBytes := pathWords * 2
	if pathBytes == 0 || pathStart+pathBytes > len(msg) {
		return nil, fmt.Errorf("内嵌报文路径字长 %d 与报文长度 %d 不一致", pathWords, len(msg))
	}

	return &Request{
		Service:       service,
		PathSizeWords: uint16(pathWords),
		PathData:      msg[pathStart:],
	}, nil
}
