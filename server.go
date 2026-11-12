package netsec

import (
	"bytes"
	"sync"
)

// ChainHop 是可信中继链上的一跳记录，按客户端到服务器的方向排列。
type ChainHop struct {
	HopCount    byte
	LinkAddress [16]byte
	PeerAddress [16]byte
	InterfaceID []byte
}

// Transaction 是服务器从完整 RELAY-FORW 链中恢复出的事务上下文。
type Transaction struct {
	ID     [3]byte
	Client ClientIdentity
	Hops   []ChainHop
}

func (t *Transaction) key() []byte {
	key := make([]byte, 0, 3+len(t.Client.Key()))
	key = append(key, t.ID[:]...)
	key = appendU16Length(key, t.Client.Key())
	key = append(key, t.Client.Key()...)
	return key
}

// Server 是可信 DHCPv6 服务器侧的封装处理者。
type Server struct {
	mu       sync.Mutex
	bindings map[string]*Transaction
}

// NewServer 创建服务器实例。
func NewServer() *Server {
	return &Server{bindings: make(map[string]*Transaction)}
}

// Unwrap 校验并剥开整条可信中继链，返回事务上下文。同一事务号下不同
// 客户身份各自独立；若键相同但链路内容不一致则拒绝，防止身份串用。
func (s *Server) Unwrap(packet []byte) (*Transaction, error) {
	hops, client, _, err := walkChain(packet, true)
	if err != nil {
		return nil, err
	}

	tx := &Transaction{
		ID: client.transactionID,
		Client: ClientIdentity{
			PeerAddress: hops[len(hops)-1].peerAddress,
		},
		Hops: make([]ChainHop, 0, len(hops)),
	}
	if duid, ok := findOption(client.options, optClientID); ok {
		tx.Client.DUID = append([]byte(nil), duid.Data...)
	}
	for _, hop := range hops {
		iface, _ := findOption(hop.options, optInterfaceID)
		tx.Hops = append(tx.Hops, ChainHop{
			HopCount:    hop.hopCount,
			LinkAddress: hop.linkAddress,
			PeerAddress: hop.peerAddress,
			InterfaceID: append([]byte(nil), iface.Data...),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(tx.key())
	if existing, ok := s.bindings[key]; ok {
		if !sameChain(existing.Hops, tx.Hops) {
			return nil, ErrBindingMismatch
		}
		return existing, nil
	}
	s.bindings[key] = tx
	return tx, nil
}

// BuildReply 严格按入向链路的逆序逐层构造 RELAY-REPL，保证服务器回复
// 沿原中继链、原跳数、原入端口身份返回。
func (s *Server) BuildReply(tx *Transaction, reply []byte) ([]byte, error) {
	if tx == nil || len(tx.Hops) == 0 {
		return nil, ErrUnknownBinding
	}
	inner, err := parseClient(reply)
	if err != nil {
		return nil, err
	}
	if inner.transactionID != tx.ID {
		return nil, ErrBindingMismatch
	}

	current := append([]byte(nil), reply...)
	for i := len(tx.Hops) - 1; i >= 0; i-- {
		hop := tx.Hops[i]
		layer := &relayMessage{
			msgType:     msgRelayReply,
			hopCount:    hop.HopCount,
			linkAddress: hop.LinkAddress,
			peerAddress: hop.PeerAddress,
			options: []Option{
				{Code: optRelayMsg, Data: current},
				{Code: optInterfaceID, Data: append([]byte(nil), hop.InterfaceID...)},
			},
		}
		current = layer.marshal()
	}
	return current, nil
}

func sameChain(a, b []ChainHop) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].HopCount != b[i].HopCount ||
			a[i].LinkAddress != b[i].LinkAddress ||
			a[i].PeerAddress != b[i].PeerAddress ||
			!bytes.Equal(a[i].InterfaceID, b[i].InterfaceID) {
			return false
		}
	}
	return true
}
