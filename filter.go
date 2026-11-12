package netsec

import "errors"

// EtherType / 长度字段相关常量。
const (
	etherTypeIPv4        = 0x0800
	etherTypeVLAN        = 0x8100
	etherTypeServiceVLAN = 0x88A8
	etherTypeOldVLAN     = 0x9100
	etherTypeIPv6        = 0x86DD
	minEtherType         = 0x0600 // RFC 5342：>=0x0600 为类型，否则为 802.3 长度字段
	ethHeaderLen         = 14
	tunPILen             = 4
)

// Action 表示过滤判定动作。
type Action uint8

const (
	// ActionDrop 丢弃该帧，不进入交换机转发面。
	ActionDrop Action = iota
	// ActionAccept 正常接收并参与转发。
	ActionAccept
	// ActionLocal 接收但仅上交本机协议栈，默认不向其他端口转发
	// （用于长度字段与载荷一致的 802.3 LLC 帧）。
	ActionLocal
)

func (a Action) String() string {
	switch a {
	case ActionAccept:
		return "accept"
	case ActionLocal:
		return "local"
	default:
		return "drop"
	}
}

// Reason 是丢弃/受限放行的具体原因，零值表示无异常。
type Reason string

const (
	ReasonOK                 Reason = ""
	ReasonTooShort           Reason = "frame shorter than Ethernet header"
	ReasonReservedMulticast  Reason = "destination in reserved bridge multicast 01:80:c2:00:00:00/45"
	ReasonNestedVLAN         Reason = "nested/stacked outer VLAN tags (QinQ)"
	ReasonLengthMismatch     Reason = "802.3 length field does not match payload size"
	ReasonMissingPacketInfo  Reason = "TUN packet info header missing or truncated"
	ReasonPacketInfoMismatch Reason = "TUN packet info proto does not match configured address family"
	ReasonAddressFamily      Reason = "IP version nibble does not match configured address family"
	ReasonTruncatedIP        Reason = "TUN payload too short to carry an IP header"
)

// Mode 选择 TAP（二层）还是 TUN（三层）解析。
type Mode uint8

const (
	ModeTAP Mode = iota
	ModeTUN
)

// AddressFamily 是 TUN 模式允许的 IP 地址族。
type AddressFamily uint8

const (
	// FamilyIPv4 仅允许 IPv4（版本半字节 4）。
	FamilyIPv4 AddressFamily = iota
	// FamilyIPv6 仅允许 IPv6（版本半字节 6）。
	FamilyIPv6
	// FamilyAny IPv4 与 IPv6 均允许。
	FamilyAny
)

// Config 描述过滤策略，由 TAP/TUN 端口初始化时给出。
type Config struct {
	// Mode 选择二层 TAP 或三层 TUN 解析，零值为 ModeTAP。
	Mode Mode
	// PacketInfo 仅 TUN 模式有效：为 true 时报文必须带 4 字节 tun_pi
	// （IFF_TUN/包信息头，常见于带 IFF_NO_PI 未置位的 Linux TUN）；
	// 为 false 时按裸 IP 报文解析。
	PacketInfo bool
	// Family 限定允许的 IP 地址族，零值为 FamilyIPv4。
	Family AddressFamily
}

// Filter 是无状态的二层/三层控制帧过滤器，可被多个读取循环并发共享。
type Filter struct {
	cfg Config
}

// New 构造一个过滤器。
func New(cfg Config) *Filter {
	return &Filter{cfg: cfg}
}

// Verdict 是单帧过滤结果。
type Verdict struct {
	Action Action
	Reason Reason
	// Offset 为触发判定的字节偏移（TUN 模式下相对报文起点），无则为 -1。
	Offset int
	// VLANTags 为剥除外层标签前统计到的 VLAN 标签层数。
	VLANTags int
	// EtherType 为外层（若有单标签则为标签内层）的类型/长度字段。
	EtherType uint16
	// Payload 指向通过校验后的载荷（共享入帧底层数组，不拷贝）。
	// TAP 下指向去掉以太网头/单层 VLAN 标签后的载荷；TUN 下指向 IP 报文。
	Payload []byte
}

// Dropped 报告该帧是否应被丢弃。
func (v Verdict) Dropped() bool { return v.Action == ActionDrop }

// Forwardable 报告该帧是否允许向其他端口转发。
func (v Verdict) Forwardable() bool { return v.Action == ActionAccept }

// Frame 供 TAP/TUN 读取循环对每个读出的报文调用。
// 入帧不会被修改；返回的 Payload 复用入帧内存。
func (f *Filter) Frame(frame []byte) Verdict {
	if f.cfg.Mode == ModeTUN {
		return f.filterTUN(frame)
	}
	return filterTAP(frame)
}

// filterTAP 实现二层控制帧过滤。
func filterTAP(frame []byte) Verdict {
	v := Verdict{Offset: -1}
	if len(frame) < ethHeaderLen {
		v.Action, v.Reason = ActionDrop, ReasonTooShort
		return v
	}

	// 1) 保留组播：01:80:C2:00:00:00 .. 01:80:C2:00:00:0F 一律丢弃。
	if isReservedBridgeMulticast(frame[0:6]) {
		v.Action, v.Reason = ActionDrop, ReasonReservedMulticast
		v.Offset = 0
		return v
	}

	// 2) 解析外层 VLAN 栈；多层标签（QinQ 等）丢弃，单层标签剥掉后继续。
	// typeOff 始终指向当前标签的类型字段，每识别一个标签前进 4 字节
	// （2 字节类型 + 2 字节 TCI）。
	typeOff := 12
	for isVLANTag(uint16(frame[typeOff])<<8 | uint16(frame[typeOff+1])) {
		v.VLANTags++
		if v.VLANTags > 1 {
			v.Action, v.Reason = ActionDrop, ReasonNestedVLAN
			v.Offset = 12
			return v
		}
		typeOff += 4
		if len(frame) < typeOff+2 {
			v.Action, v.Reason = ActionDrop, ReasonTooShort
			return v
		}
	}
	etherType := uint16(frame[typeOff])<<8 | uint16(frame[typeOff+1])
	payloadOffset := typeOff + 2
	v.EtherType = etherType

	// 3) 小于 0x0600：802.3 长度字段，需与实际载荷严格一致，且默认不转发。
	// TAP 读出的帧不含 FCS，故以“长度字段 == 头后剩余字节数”为一致判据。
	if etherType < minEtherType {
		payload := frame[payloadOffset:]
		if int(etherType) != len(payload) {
			v.Action, v.Reason = ActionDrop, ReasonLengthMismatch
			v.Offset = 12
			return v
		}
		v.Action = ActionLocal
		v.Payload = payload
		return v
	}

	v.Action = ActionAccept
	v.Payload = frame[payloadOffset:]
	return v
}

// filterTUN 实现三层 TUN 报文过滤：先处理可选包信息头，再核对地址族。
func (f *Filter) filterTUN(frame []byte) Verdict {
	v := Verdict{Offset: -1}
	packet := frame

	if f.cfg.PacketInfo {
		// tun_pi: flags(uint16) + proto(be16)，缺失或被截断一律拒绝。
		if len(frame) < tunPILen {
			v.Action, v.Reason = ActionDrop, ReasonMissingPacketInfo
			return v
		}
		piProto := uint16(frame[2])<<8 | uint16(frame[3])
		if !piProtoMatchesFamily(piProto, f.cfg.Family) {
			v.Action, v.Reason = ActionDrop, ReasonPacketInfoMismatch
			v.Offset = 2
			return v
		}
		packet = frame[tunPILen:]
	}

	if len(packet) < 1 {
		v.Action, v.Reason = ActionDrop, ReasonTruncatedIP
		return v
	}

	// 版本半字节必须与配置的地址族一致。
	version := packet[0] >> 4
	if !versionMatchesFamily(version, f.cfg.Family) {
		v.Action, v.Reason = ActionDrop, ReasonAddressFamily
		v.Offset = tunPILen
		return v
	}
	minLen := 20
	if version == 6 {
		minLen = 40
	}
	if len(packet) < minLen {
		v.Action, v.Reason = ActionDrop, ReasonTruncatedIP
		return v
	}

	v.Action = ActionAccept
	v.EtherType = etherTypeIPv4
	if version == 6 {
		v.EtherType = etherTypeIPv6
	}
	v.Payload = packet
	return v
}

// isReservedBridgeMulticast 判断 DA 是否落在 01:80:C2:00:00:00/45，
// 即前 5 字节固定且最后一个字节处于 0x00..0x0F。
func isReservedBridgeMulticast(da []byte) bool {
	return da[0] == 0x01 && da[1] == 0x80 && da[2] == 0xC2 &&
		da[3] == 0x00 && da[4] == 0x00 && da[5] <= 0x0F
}

func isVLANTag(etherType uint16) bool {
	switch etherType {
	case etherTypeVLAN, etherTypeServiceVLAN, etherTypeOldVLAN:
		return true
	default:
		return false
	}
}

func piProtoMatchesFamily(proto uint16, family AddressFamily) bool {
	switch family {
	case FamilyIPv4:
		return proto == etherTypeIPv4
	case FamilyIPv6:
		return proto == etherTypeIPv6
	default:
		return proto == etherTypeIPv4 || proto == etherTypeIPv6
	}
}

func versionMatchesFamily(version uint8, family AddressFamily) bool {
	switch family {
	case FamilyIPv4:
		return version == 4
	case FamilyIPv6:
		return version == 6
	default:
		return version == 4 || version == 6
	}
}

// ErrDropped 可供读取循环在需要以 error 形式记录丢帧时使用。
var ErrDropped = errors.New("l2 control frame dropped")
