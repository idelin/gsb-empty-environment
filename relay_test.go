package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func buildClient(txID [3]byte, duid []byte, extra ...Option) []byte {
	msg := &clientMessage{
		msgType:       msgSolicit,
		transactionID: txID,
		options:       []Option{{Code: optClientID, Data: duid}},
	}
	msg.options = append(msg.options, extra...)
	return msg.marshal()
}

func addr(last byte) [16]byte {
	var addr [16]byte
	addr[15] = last
	return addr
}

func TestClientForgedRelayFieldsRejected(t *testing.T) {
	agent := NewRelayAgent(nil)
	txID := [3]byte{0x11, 0x22, 0x33}

	forged := buildClient(txID, []byte("duid-1"), Option{Code: optInterfaceID, Data: []byte("fake-port")})
	if _, err := agent.WrapClient(forged, PortIdentity{"edge", "ge-0/0/1"}, addr(1), addr(100)); !errors.Is(err, ErrClientRelayForged) {
		t.Fatalf("forged Interface-ID: want %v, got %v", ErrClientRelayForged, err)
	}

	forgedRelay := (&relayMessage{
		msgType: msgRelayForward,
		options: []Option{{Code: optRelayMsg, Data: buildClient(txID, nil)}},
	}).marshal()
	if _, err := agent.WrapClient(forgedRelay, PortIdentity{"edge", "ge-0/0/1"}, addr(1), addr(100)); !errors.Is(err, ErrClientRelayForged) {
		t.Fatalf("client relay message: want %v, got %v", ErrClientRelayForged, err)
	}

	forgedNested := buildClient(txID, nil, Option{Code: optRelayMsg, Data: []byte("x")})
	if _, err := agent.WrapClient(forgedNested, PortIdentity{"edge", "ge-0/0/1"}, addr(1), addr(100)); !errors.Is(err, ErrClientRelayForged) {
		t.Fatalf("forged Relay-Msg: want %v, got %v", ErrClientRelayForged, err)
	}
}

func TestNestedRelayChainAndReturnPath(t *testing.T) {
	txID := [3]byte{0xab, 0xcd, 0xef}
	clientPacket := buildClient(txID, []byte("duid-A"))

	edge := NewRelayAgent(nil)
	core := NewRelayAgent([]string{linkText(addr(1))})
	server := NewServer()

	edgePort := PortIdentity{Device: "edge", Interface: "ge-0/0/1"}
	corePort := PortIdentity{Device: "core", Interface: "xe-1/0/0"}

	first, err := edge.WrapClient(clientPacket, edgePort, addr(1), addr(100))
	if err != nil {
		t.Fatalf("edge wrap: %v", err)
	}
	second, err := core.WrapRelay(first, corePort, addr(2), addr(1))
	if err != nil {
		t.Fatalf("core wrap: %v", err)
	}

	tx, err := server.Unwrap(second)
	if err != nil {
		t.Fatalf("server unwrap: %v", err)
	}
	if len(tx.Hops) != 2 || tx.Hops[0].HopCount != 1 || tx.Hops[1].HopCount != 0 {
		t.Fatalf("unexpected hops: %+v", tx.Hops)
	}
	if tx.Client.PeerAddress != addr(100) || !bytes.Equal(tx.Client.DUID, []byte("duid-A")) {
		t.Fatalf("client identity lost: %+v", tx.Client)
	}
	if !bytes.Equal(tx.Hops[1].InterfaceID, edgePort.bytes()) ||
		!bytes.Equal(tx.Hops[0].InterfaceID, corePort.bytes()) {
		t.Fatalf("per-hop port identities not bound: %+v", tx.Hops)
	}

	reply := buildClient(txID, []byte("duid-A"))
	reply[0] = msgReply
	relayReply, err := server.BuildReply(tx, reply)
	if err != nil {
		t.Fatalf("build reply: %v", err)
	}

	toEdge, err := core.UnwrapReply(relayReply)
	if err != nil {
		t.Fatalf("core unwrap reply: %v", err)
	}
	toClient, err := edge.UnwrapReply(toEdge)
	if err != nil {
		t.Fatalf("edge unwrap reply: %v", err)
	}
	if !bytes.Equal(toClient, reply) {
		t.Fatalf("reply must reach the original client unchanged")
	}
}

func TestSameTransactionDifferentClientsDoNotMix(t *testing.T) {
	txID := [3]byte{0x01, 0x02, 0x03}

	clientA := buildClient(txID, []byte("duid-A"))
	clientB := buildClient(txID, []byte("duid-B"))

	edge := NewRelayAgent(nil)
	core := NewRelayAgent([]string{linkText(addr(1))})
	server := NewServer()

	portA := PortIdentity{Device: "edge", Interface: "port-A"}
	portB := PortIdentity{Device: "edge", Interface: "port-B"}

	firstA, err := edge.WrapClient(clientA, portA, addr(1), addr(100))
	if err != nil {
		t.Fatal(err)
	}
	firstB, err := edge.WrapClient(clientB, portB, addr(1), addr(200))
	if err != nil {
		t.Fatal(err)
	}
	chainA, err := core.WrapRelay(firstA, PortIdentity{"core", "up-A"}, addr(2), addr(1))
	if err != nil {
		t.Fatal(err)
	}
	chainB, err := core.WrapRelay(firstB, PortIdentity{"core", "up-B"}, addr(2), addr(1))
	if err != nil {
		t.Fatal(err)
	}

	txA, err := server.Unwrap(chainA)
	if err != nil {
		t.Fatal(err)
	}
	txB, err := server.Unwrap(chainB)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(txA.Client.Key(), txB.Client.Key()) || bytes.Equal(txA.key(), txB.key()) {
		t.Fatal("clients sharing a transaction ID must remain distinct")
	}

	replyA := buildClient(txID, []byte("duid-A"))
	replyA[0] = msgReply
	replyForA, err := server.BuildReply(txA, replyA)
	if err != nil {
		t.Fatal(err)
	}

	toEdge, err := core.UnwrapReply(replyForA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edge.UnwrapReply(toEdge); err != nil {
		t.Fatalf("legitimate return path failed: %v", err)
	}

	replyB := buildClient(txID, []byte("duid-B"))
	replyB[0] = msgReply
	replyForB, err := server.BuildReply(txB, replyB)
	if err != nil {
		t.Fatal(err)
	}

	// 把 B 的回复嵌入 A 的中继链：端口身份属于 A，客户身份属于 B，
	// 逐跳绑定（事务号 + 客户身份 + 入端口 + 跳数）必须识别出不匹配。
	crossed, err := server.BuildReply(txA, replyB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.UnwrapReply(crossed); !errors.Is(err, ErrUnknownBinding) {
		t.Fatalf("B client identity must not reuse A's core port binding, got %v", err)
	}

	// B 自己的合法链仍然正常返回。
	legitToEdge, err := core.UnwrapReply(replyForB)
	if err != nil {
		t.Fatal(err)
	}
	// 把 B 的外层剥掉后伪造 A 的入端口身份，边缘跳同样不得放行。
	edgeLayer, err := parseRelay(legitToEdge)
	if err != nil {
		t.Fatal(err)
	}
	for i := range edgeLayer.options {
		if edgeLayer.options[i].Code == optInterfaceID {
			edgeLayer.options[i].Data = portA.bytes()
		}
	}
	if _, err := edge.UnwrapReply(edgeLayer.marshal()); !errors.Is(err, ErrUnknownBinding) {
		t.Fatalf("B client identity must not reuse A's edge port binding, got %v", err)
	}
	if _, err := edge.UnwrapReply(legitToEdge); err != nil {
		t.Fatalf("B's own return path must succeed: %v", err)
	}
}

func TestReplyRejectsForeignOrTamperedHopFields(t *testing.T) {
	txID := [3]byte{0x22, 0x44, 0x66}
	edge := NewRelayAgent(nil)
	core := NewRelayAgent([]string{linkText(addr(1))})
	server := NewServer()

	first, err := edge.WrapClient(buildClient(txID, []byte("duid-A")), PortIdentity{"edge", "p1"}, addr(1), addr(100))
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.WrapRelay(first, PortIdentity{"core", "p2"}, addr(2), addr(1))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := server.Unwrap(second)
	if err != nil {
		t.Fatal(err)
	}

	reply := buildClient(txID, []byte("duid-A"))
	reply[0] = msgReply
	good, err := server.BuildReply(tx, reply)
	if err != nil {
		t.Fatal(err)
	}

	tampered, err := parseRelay(good)
	if err != nil {
		t.Fatal(err)
	}
	for i := range tampered.options {
		if tampered.options[i].Code == optInterfaceID {
			tampered.options[i].Data = PortIdentity{"core", "attacker-port"}.bytes()
		}
	}
	if _, err := core.UnwrapReply(tampered.marshal()); !errors.Is(err, ErrUnknownBinding) {
		t.Fatalf("foreign Interface-ID must be rejected, got %v", err)
	}

	replayedAtEdge, err := core.UnwrapReply(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edge.UnwrapReply(good); !errors.Is(err, ErrUnknownBinding) {
		t.Fatalf("core's outermost layer must not be accepted by edge, got %v", err)
	}
	if _, err := core.UnwrapReply(replayedAtEdge); !errors.Is(err, ErrUnknownBinding) {
		t.Fatalf("edge's outermost layer must not be accepted by core, got %v", err)
	}
}

func TestUntrustedPeerAndHopCountEnforced(t *testing.T) {
	txID := [3]byte{0x09, 0x08, 0x07}
	edge := NewRelayAgent(nil)
	core := NewRelayAgent(nil)

	first, err := edge.WrapClient(buildClient(txID, []byte("duid-A")), PortIdentity{"edge", "p1"}, addr(1), addr(100))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.WrapRelay(first, PortIdentity{"core", "p2"}, addr(2), addr(1)); !errors.Is(err, ErrTrustedPeerRequired) {
		t.Fatalf("untrusted peer: want %v, got %v", ErrTrustedPeerRequired, err)
	}

	core.TrustPeer(linkText(addr(1)))
	second, err := core.WrapRelay(first, PortIdentity{"core", "p2"}, addr(2), addr(1))
	if err != nil {
		t.Fatal(err)
	}

	badHop, err := parseRelay(second)
	if err != nil {
		t.Fatal(err)
	}
	badHop.hopCount = 7
	next := NewRelayAgent([]string{linkText(addr(2))})
	if _, err := next.WrapRelay(badHop.marshal(), PortIdentity{"next", "p3"}, addr(3), addr(2)); !errors.Is(err, ErrChainCorrupt) {
		t.Fatalf("non-contiguous hop count must be rejected, got %v", err)
	}
}

func TestServerRejectsConflictingChainForSameIdentity(t *testing.T) {
	txID := [3]byte{0x55, 0x66, 0x77}
	edge := NewRelayAgent(nil)
	core := NewRelayAgent([]string{linkText(addr(1))})
	server := NewServer()

	first, err := edge.WrapClient(buildClient(txID, []byte("duid-A")), PortIdentity{"edge", "p1"}, addr(1), addr(100))
	if err != nil {
		t.Fatal(err)
	}
	chain, err := core.WrapRelay(first, PortIdentity{"core", "p2"}, addr(2), addr(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Unwrap(chain); err != nil {
		t.Fatal(err)
	}

	otherFirst, err := edge.WrapClient(buildClient(txID, []byte("duid-A")), PortIdentity{"edge", "p1-other"}, addr(1), addr(100))
	if err != nil {
		t.Fatal(err)
	}
	otherChain, err := core.WrapRelay(otherFirst, PortIdentity{"core", "p2"}, addr(2), addr(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Unwrap(otherChain); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("conflicting chain for same identity: want %v, got %v", ErrBindingMismatch, err)
	}
}
