package netsec

import (
	"bytes"
	"errors"
)

// DHCPv6 消息类型，见 RFC 8415 第 7.3 节。
const (
	msgSolicit      = 1
	msgRequest      = 3
	msgConfirm      = 4
	msgRenew        = 5
	msgRebind       = 6
	msgReply        = 7
	msgRelayForward = 12
	msgRelayReply   = 13
)

// 中继相关选项号，见 RFC 8415。
const (
	optRelayMsg    = 9
	optInterfaceID = 18
	optClientID    = 1
)

// maxHops 为 RFC 8415 规定的 HOP_COUNT 上限（5 位字段）。
const maxHops = 32

const (
	clientHeaderLen = 4 // type(1) + transaction ID(3)
	relayHeaderLen  = 34
	optionHeaderLen = 4 // option code(2) + length(2)
	ipv6Len         = 16
)

var (
	// ErrMalformedPacket 表示报文长度、字段或选项布局不合法。
	ErrMalformedPacket = errors.New("netsec: malformed packet")
	// ErrClientRelayForged 表示不受信客户端来源中携带了仅可信中继可插入的字段。
	ErrClientRelayForged = errors.New("netsec: client-originated relay fields are forbidden")
	// ErrTrustedPeerRequired 表示上一跳不在本中继的可信对等体清单内。
	ErrTrustedPeerRequired = errors.New("netsec: upstream relay is not a trusted peer")
	// ErrUnknownBinding 表示回复无法匹配本跳记录的转发绑定。
	ErrUnknownBinding = errors.New("netsec: no matching relay binding")
	// ErrBindingMismatch 表示回复绑定的入端口、事务或客户身份与记录不一致。
	ErrBindingMismatch = errors.New("netsec: relay binding mismatch")
	// ErrChainCorrupt 表示嵌套中继链的跳数、接口身份或层级结构不合法。
	ErrChainCorrupt = errors.New("netsec: relay chain corrupt")
	// ErrEmptyPortIdentity 表示调用方未提供非空的入端口身份。
	ErrEmptyPortIdentity = errors.New("netsec: ingress port identity must not be empty")
)

// Option 是 DHCPv6 的 TLV 选项。
type Option struct {
	Code uint16
	Data []byte
}

// clientMessage 是终端客户端与服务器之间的普通 DHCPv6 消息。
type clientMessage struct {
	msgType       byte
	transactionID [3]byte
	options       []Option
	raw           []byte
}

// relayMessage 是 RELAY-FORW/RELAY-REPL 封装消息。
type relayMessage struct {
	msgType     byte
	hopCount    byte
	linkAddress [16]byte
	peerAddress [16]byte
	options     []Option
	raw         []byte
}

func parseOptions(b []byte) ([]Option, error) {
	var opts []Option
	for len(b) > 0 {
		if len(b) < optionHeaderLen {
			return nil, ErrMalformedPacket
		}
		code := uint16(b[0])<<8 | uint16(b[1])
		length := int(uint16(b[2])<<8 | uint16(b[3]))
		b = b[optionHeaderLen:]
		if length > len(b) {
			return nil, ErrMalformedPacket
		}
		data := make([]byte, length)
		copy(data, b[:length])
		opts = append(opts, Option{Code: code, Data: data})
		b = b[length:]
	}
	return opts, nil
}

// parseClient 解析终端侧消息，并拒绝任何只允许可信中继出现的字段，
// 也拒绝客户端把自己伪装成中继消息。
func parseClient(packet []byte) (*clientMessage, error) {
	if len(packet) < clientHeaderLen {
		return nil, ErrMalformedPacket
	}
	switch packet[0] {
	case msgRelayForward, msgRelayReply:
		return nil, ErrClientRelayForged
	case 0:
		return nil, ErrMalformedPacket
	}
	opts, err := parseOptions(packet[clientHeaderLen:])
	if err != nil {
		return nil, err
	}
	for _, opt := range opts {
		switch opt.Code {
		case optRelayMsg, optInterfaceID:
			return nil, ErrClientRelayForged
		}
	}
	msg := &clientMessage{
		msgType: packet[0],
		options: opts,
		raw:     append([]byte(nil), packet...),
	}
	copy(msg.transactionID[:], packet[1:4])
	return msg, nil
}

func parseRelay(packet []byte) (*relayMessage, error) {
	if len(packet) < relayHeaderLen || (packet[0] != msgRelayForward && packet[0] != msgRelayReply) {
		return nil, ErrMalformedPacket
	}
	hop := packet[1]
	if hop >= maxHops {
		return nil, ErrChainCorrupt
	}
	opts, err := parseOptions(packet[relayHeaderLen:])
	if err != nil {
		return nil, err
	}
	if countOptions(opts, optRelayMsg) != 1 || countOptions(opts, optInterfaceID) > 1 {
		return nil, ErrMalformedPacket
	}
	relay := &relayMessage{
		msgType:  packet[0],
		hopCount: hop,
		options:  opts,
		raw:      append([]byte(nil), packet...),
	}
	copy(relay.linkAddress[:], packet[2:18])
	copy(relay.peerAddress[:], packet[18:34])
	return relay, nil
}

func (m *clientMessage) marshal() []byte {
	var buf bytes.Buffer
	buf.WriteByte(m.msgType)
	buf.Write(m.transactionID[:])
	writeOptions(&buf, m.options)
	return buf.Bytes()
}

func (m *relayMessage) marshal() []byte {
	var buf bytes.Buffer
	buf.WriteByte(m.msgType)
	buf.WriteByte(m.hopCount)
	buf.Write(m.linkAddress[:])
	buf.Write(m.peerAddress[:])
	writeOptions(&buf, m.options)
	return buf.Bytes()
}

func writeOptions(buf *bytes.Buffer, opts []Option) {
	for _, opt := range opts {
		buf.WriteByte(byte(opt.Code >> 8))
		buf.WriteByte(byte(opt.Code))
		length := len(opt.Data)
		buf.WriteByte(byte(length >> 8))
		buf.WriteByte(byte(length))
		buf.Write(opt.Data)
	}
}

func findOption(opts []Option, code uint16) (Option, bool) {
	for _, opt := range opts {
		if opt.Code == code {
			return opt, true
		}
	}
	return Option{}, false
}

func countOptions(opts []Option, code uint16) int {
	count := 0
	for _, opt := range opts {
		if opt.Code == code {
			count++
		}
	}
	return count
}

func isUnspecifiedIPv6(addr [16]byte) bool {
	for _, b := range addr {
		if b != 0 {
			return false
		}
	}
	return true
}

// walkChain 逐层剥开 RELAY 消息，返回由外到内的各跳与最内层终端消息。
// 每跳必须携带唯一的 Interface-ID，HOP_COUNT 必须从外层起严格递减并归零，
// 且对端地址不得为全零。wantForward 选择期望的中继消息方向。
func walkChain(packet []byte, wantForward bool) ([]*relayMessage, *clientMessage, []byte, error) {
	wantType := byte(msgRelayForward)
	innerType := byte(msgRelayReply)
	if !wantForward {
		wantType = msgRelayReply
		innerType = msgRelayForward
	}

	var hops []*relayMessage
	current := packet
	for {
		relay, err := parseRelay(current)
		if err != nil {
			return nil, nil, nil, err
		}
		if relay.msgType != wantType {
			return nil, nil, nil, ErrChainCorrupt
		}
		if len(hops) > 0 && relay.hopCount != hops[len(hops)-1].hopCount-1 {
			return nil, nil, nil, ErrChainCorrupt
		}
		if isUnspecifiedIPv6(relay.peerAddress) {
			return nil, nil, nil, ErrChainCorrupt
		}
		iface, ok := findOption(relay.options, optInterfaceID)
		if !ok || len(iface.Data) == 0 {
			return nil, nil, nil, ErrChainCorrupt
		}

		relayMsg, _ := findOption(relay.options, optRelayMsg)
		hops = append(hops, relay)
		if len(relayMsg.Data) > 0 && (relayMsg.Data[0] == msgRelayForward || relayMsg.Data[0] == msgRelayReply) {
			if relayMsg.Data[0] == innerType {
				return nil, nil, nil, ErrChainCorrupt
			}
			current = relayMsg.Data
			continue
		}

		client, err := parseClient(relayMsg.Data)
		if err != nil {
			return nil, nil, nil, err
		}
		if relay.hopCount != 0 {
			return nil, nil, nil, ErrChainCorrupt
		}
		return hops, client, relayMsg.Data, nil
	}
}
