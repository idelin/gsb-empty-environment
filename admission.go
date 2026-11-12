package netsec

import (
	"errors"
	"fmt"
	"net/netip"
)

// 解封装路径的准入判定错误。调用方应将任何非 nil 错误视为丢弃。
var (
	ErrTruncated        = errors.New("netsec: packet truncated")
	ErrLengthMismatch   = errors.New("netsec: inner frame length inconsistent with outer UDP length")
	ErrUnauthorizedVTEP = errors.New("netsec: source VTEP not authorized for VNI")
	ErrMissingIFlag     = errors.New("netsec: VXLAN I flag not set")
	ErrReservedBits     = errors.New("netsec: reserved bits must be zero")
	ErrGeneveVersion    = errors.New("netsec: unsupported Geneve version")
	ErrGeneveProto      = errors.New("netsec: Geneve protocol type is not transparent Ethernet bridging")
	ErrGeneveOptOverrun = errors.New("netsec: Geneve options exceed declared option length")
	ErrCriticalOption   = errors.New("netsec: unknown Geneve option with critical bit set")
	ErrNestedEncap      = errors.New("netsec: nested encapsulation is not allowed")
	ErrInnerMalformed   = errors.New("netsec: inner Ethernet frame malformed")
)

// Protocol 标识外层 UDP 承载的封装协议（由目的端口决定）。
type Protocol int

const (
	ProtoVXLAN Protocol = iota
	ProtoGeneve
)

// Decision 是准入成功后的判定结果。
type Decision struct {
	VNI uint32
	// Inner 为解封装后的内层以太帧（含以太网头）。
	Inner []byte
}

type geneveOptionID struct {
	class uint16
	typ   uint8 // 不含关键位
}

// Gateway 持有 VNI→授权 VTEP 源地址的准入表与已知 Geneve 选项注册表。
// 解封装路径在把内层帧送入租户转发面之前必须调用 Admit。
type Gateway struct {
	vteps map[uint32]map[netip.Addr]struct{}
	opts  map[geneveOptionID]struct{}
}

func NewGateway() *Gateway {
	return &Gateway{
		vteps: make(map[uint32]map[netip.Addr]struct{}),
		opts:  make(map[geneveOptionID]struct{}),
	}
}

// AuthorizeVTEP 授权 src 作为 vni 的合法 VTEP 源地址。
func (g *Gateway) AuthorizeVTEP(vni uint32, src netip.Addr) {
	if vni > maxVNI {
		panic("netsec: VNI out of 24-bit range")
	}
	set, ok := g.vteps[vni]
	if !ok {
		set = make(map[netip.Addr]struct{})
		g.vteps[vni] = set
	}
	set[src] = struct{}{}
}

// RegisterGeneveOption 声明一个本端实现可识别的 Geneve 选项。
// 未注册且置了关键位的选项会导致整包丢弃。
func (g *Gateway) RegisterGeneveOption(class uint16, typ uint8) {
	g.opts[geneveOptionID{class: class, typ: typ & 0x7f}] = struct{}{}
}

func (g *Gateway) authorized(vni uint32, src netip.Addr) bool {
	set, ok := g.vteps[vni]
	if !ok {
		return false
	}
	_, ok = set[src]
	return ok
}

// Admit 对来自 UDP 底层的封装包执行准入检查。
//
// src 是外层 UDP 源地址（VTEP）；declaredLen 是外层 UDP 头长度字段的值；
// payload 是 UDP 载荷（封装头 + 内层以太帧）。
// 返回 nil Decision 与非 nil error 时必须丢包。
func (g *Gateway) Admit(proto Protocol, src netip.Addr, declaredLen int, payload []byte) (*Decision, error) {
	// 外层 UDP 声明的长度必须与实收载荷一致，防止截断/填充伪造。
	if declaredLen < 8 || declaredLen-8 != len(payload) {
		return nil, fmt.Errorf("%w: udp len %d, payload %d", ErrLengthMismatch, declaredLen, len(payload))
	}

	var (
		vni   uint32
		inner []byte
		err   error
	)
	switch proto {
	case ProtoVXLAN:
		vni, inner, err = parseVXLAN(payload)
	case ProtoGeneve:
		vni, inner, err = g.parseGeneve(payload)
	default:
		return nil, fmt.Errorf("netsec: unknown encapsulation protocol %d", proto)
	}
	if err != nil {
		return nil, err
	}

	// VNI 只允许来自授权该 VNI 的 VTEP 源地址。
	if !g.authorized(vni, src) {
		return nil, fmt.Errorf("%w: vni %d from %s", ErrUnauthorizedVTEP, vni, src)
	}

	if err := validateInnerFrame(inner); err != nil {
		return nil, err
	}
	return &Decision{VNI: vni, Inner: inner}, nil
}

const maxVNI = 1<<24 - 1
