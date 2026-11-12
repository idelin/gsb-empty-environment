package netsec

import (
	"bytes"
	"sync"
)

// PortIdentity 描述中继设备上的一个入端口身份，对应 RELAY-FORW 中
// 由本跳可信中继插入的 Interface-ID 选项。
type PortIdentity struct {
	// Device 标识中继设备本身，避免不同设备的端口名冲突。
	Device string
	// Interface 为设备上的入端口/接口标识。
	Interface string
}

func (p PortIdentity) bytes() []byte {
	device := []byte(p.Device)
	iface := []byte(p.Interface)
	buf := make([]byte, 0, 4+len(device)+len(iface))
	buf = appendU16Length(buf, device)
	buf = append(buf, device...)
	buf = appendU16Length(buf, iface)
	buf = append(buf, iface...)
	return buf
}

func appendU16Length(buf, data []byte) []byte {
	return append(buf, byte(len(data)>>8), byte(len(data)))
}

// ClientIdentity 标识发起事务的终端客户：链路对端地址加可选 DUID。
type ClientIdentity struct {
	PeerAddress [16]byte
	DUID        []byte
}

// Key 返回可作为映射键使用的定长/稳定字节形态。
func (c ClientIdentity) Key() []byte {
	duid := append([]byte(nil), c.DUID...)
	buf := make([]byte, 0, ipv6Len+2+len(duid))
	buf = append(buf, c.PeerAddress[:]...)
	buf = appendU16Length(buf, duid)
	buf = append(buf, duid...)
	return buf
}

type hopBinding struct {
	transactionID [3]byte
	client        []byte
	port          []byte
	hopCount      byte
}

func (b hopBinding) key() []byte {
	key := make([]byte, 0, 3+len(b.client)+len(b.port)+1)
	key = append(key, b.transactionID[:]...)
	key = appendU16Length(key, b.client)
	key = append(key, b.client...)
	key = appendU16Length(key, b.port)
	key = append(key, b.port...)
	key = append(key, b.hopCount)
	return key
}

// RelayAgent 是接入宿主调用的可信中继实例。它维护本跳插入的端口身份
// 与入向事务之间的绑定，服务器回复只有完全匹配绑定时才会被剥离转发。
type RelayAgent struct {
	mu           sync.Mutex
	trustedPeers map[string]bool
	bindings     map[string]hopBinding
}

// NewRelayAgent 创建中继实例，trustedPeers 为允许向本跳转发封装报文的
// 上游可信中继链路地址（IPv6 文本形态）。
func NewRelayAgent(trustedPeers []string) *RelayAgent {
	agent := &RelayAgent{
		trustedPeers: make(map[string]bool, len(trustedPeers)),
		bindings:     make(map[string]hopBinding),
	}
	for _, peer := range trustedPeers {
		agent.trustedPeers[peer] = true
	}
	return agent
}

// TrustPeer 追加一个可信上游中继对等体。
func (a *RelayAgent) TrustPeer(linkAddress string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.trustedPeers[linkAddress] = true
}

// WrapClient 封装来自不受信客户链路的普通 DHCPv6 请求。客户端报文内
// 任何中继专属字段或中继消息类型都会被拒绝，绑定从本跳开始建立。
func (a *RelayAgent) WrapClient(packet []byte, ingress PortIdentity, link, peer [16]byte) ([]byte, error) {
	if ingress.Device == "" || ingress.Interface == "" {
		return nil, ErrEmptyPortIdentity
	}
	client, err := parseClient(packet)
	if err != nil {
		return nil, err
	}
	identity := ClientIdentity{PeerAddress: peer}
	if duid, ok := findOption(client.options, optClientID); ok {
		identity.DUID = append([]byte(nil), duid.Data...)
	}

	forward := &relayMessage{
		msgType:     msgRelayForward,
		hopCount:    0,
		linkAddress: link,
		peerAddress: peer,
		options: []Option{
			{Code: optRelayMsg, Data: append([]byte(nil), packet...)},
			{Code: optInterfaceID, Data: ingress.bytes()},
		},
	}

	a.mu.Lock()
	a.storeBinding(hopBinding{
		transactionID: client.transactionID,
		client:        identity.Key(),
		port:          ingress.bytes(),
		hopCount:      0,
	})
	a.mu.Unlock()

	return forward.marshal(), nil
}

// WrapRelay 封装来自可信上游中继的 RELAY-FORW。上一跳必须在可信对等体
// 清单中；本跳把入端口身份、事务和客户身份一并绑定后再向外转发。
func (a *RelayAgent) WrapRelay(packet []byte, ingress PortIdentity, link, peer [16]byte) ([]byte, error) {
	if ingress.Device == "" || ingress.Interface == "" {
		return nil, ErrEmptyPortIdentity
	}

	hops, client, _, err := walkChain(packet, true)
	if err != nil {
		return nil, err
	}
	outer := hops[0]
	a.mu.Lock()
	if !a.trustedPeers[linkText(outer.linkAddress)] {
		a.mu.Unlock()
		return nil, ErrTrustedPeerRequired
	}
	a.mu.Unlock()
	if outer.hopCount+1 >= maxHops {
		return nil, ErrChainCorrupt
	}

	identity := ClientIdentity{PeerAddress: hops[len(hops)-1].peerAddress}
	if duid, ok := findOption(client.options, optClientID); ok {
		identity.DUID = append([]byte(nil), duid.Data...)
	}

	forward := &relayMessage{
		msgType:     msgRelayForward,
		hopCount:    outer.hopCount + 1,
		linkAddress: link,
		peerAddress: peer,
		options: []Option{
			{Code: optRelayMsg, Data: append([]byte(nil), packet...)},
			{Code: optInterfaceID, Data: ingress.bytes()},
		},
	}

	a.mu.Lock()
	a.storeBinding(hopBinding{
		transactionID: client.transactionID,
		client:        identity.Key(),
		port:          ingress.bytes(),
		hopCount:      forward.hopCount,
	})
	a.mu.Unlock()

	return forward.marshal(), nil
}

// UnwrapReply 处理沿中继链返回的 RELAY-REPL。只有在绑定表中找到与
// 事务、客户身份、入端口和跳数完全一致的记录时，才确认最外层字段确为
// 本跳所添加，并剥掉该层把内层报文交还原下一跳。
func (a *RelayAgent) UnwrapReply(packet []byte) ([]byte, error) {
	hops, client, _, err := walkChain(packet, false)
	if err != nil {
		return nil, err
	}
	outer := hops[0]
	iface, ok := findOption(outer.options, optInterfaceID)
	if !ok {
		return nil, ErrChainCorrupt
	}

	identity := ClientIdentity{PeerAddress: hops[len(hops)-1].peerAddress}
	if duid, ok := findOption(client.options, optClientID); ok {
		identity.DUID = append([]byte(nil), duid.Data...)
	}

	want := hopBinding{
		transactionID: client.transactionID,
		client:        identity.Key(),
		port:          append([]byte(nil), iface.Data...),
		hopCount:      outer.hopCount,
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	recorded, ok := a.bindings[string(want.key())]
	if !ok {
		return nil, ErrUnknownBinding
	}
	if !bytes.Equal(recorded.port, want.port) ||
		!bytes.Equal(recorded.client, want.client) ||
		recorded.transactionID != want.transactionID ||
		recorded.hopCount != want.hopCount {
		return nil, ErrBindingMismatch
	}

	relayMsg, _ := findOption(outer.options, optRelayMsg)
	return append([]byte(nil), relayMsg.Data...), nil
}

func (a *RelayAgent) storeBinding(binding hopBinding) {
	key := string(binding.key())
	if existing, ok := a.bindings[key]; ok {
		if !bytes.Equal(existing.port, binding.port) ||
			!bytes.Equal(existing.client, binding.client) ||
			existing.hopCount != binding.hopCount {
			return
		}
	}
	a.bindings[key] = binding
}
