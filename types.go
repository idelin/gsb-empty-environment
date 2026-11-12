package netsec

import (
	"net/netip"
	"time"
)

// MessageType 是报文处理层关心的 DHCPv6 消息类型子集。
type MessageType byte

const (
	MsgSolicit MessageType = 1
	MsgRequest MessageType = 3
	MsgRenew   MessageType = 5
	MsgRebind  MessageType = 6
	MsgReply   MessageType = 7
	MsgRelease MessageType = 8
)

// StatusCode 是回复中每个 IA_PD 携带的状态。
type StatusCode uint16

const (
	StatusSuccess       StatusCode = 0
	StatusNoBinding     StatusCode = 3
	StatusNoPrefixAvail StatusCode = 6
)

// IAPDRequest 描述报文中一个 IA_PD 选项。
// HasPrefix 为真时 Prefix 在 Solicit/Request 中是前缀提示，
// 在 Renew/Rebind/Release 中是客户端声称已持有的前缀。
type IAPDRequest struct {
	IAID      uint32
	Prefix    netip.Prefix
	HasPrefix bool
}

// RelayHop 是一层 relay-forw 封装，链路自外层向内层排列。
type RelayHop struct {
	LinkAddress netip.Addr
	PeerAddress netip.Addr
}

// Message 是报文处理层解包后交给委派绑定包的内容。
// Relay 为 nil 表示客户端直连；非空时按外层到内层排列。
type Message struct {
	Type       MessageType
	ClientDUID []byte
	IAPDs      []IAPDRequest
	SourceAddr netip.Addr
	Relay      []RelayHop
}

// BindingKey 唯一标识一条委派绑定：客户端 DUID 加 IAID。
type BindingKey struct {
	DUID string
	IAID uint32
}

// Binding 是一条 IA_PD 委派绑定。
type Binding struct {
	Key       BindingKey
	Prefix    netip.Prefix
	NextHop   netip.Addr
	ExpiresAt time.Time
}

// IAResult 是回复中单个 IA_PD 的处理结果。
type IAResult struct {
	IAID       uint32
	Status     StatusCode
	Prefix     netip.Prefix
	ValidUntil time.Time
}
