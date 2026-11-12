package netsec

import (
	"net/netip"
	"testing"
)

var (
	addrProvider = netip.MustParseAddr("10.0.0.10")
	addrAttacker = netip.MustParseAddr("10.0.0.66")
	addrVictim   = netip.MustParseAddr("10.0.0.77")

	keySvc = ServiceKey{ServiceID: 0x1234, InstanceID: 0x0001, MajorVersion: 0x02}
)

// ipv4Option builds an 11-byte IPv4 endpoint option (Length 0x0009 per
// AUTOSAR): Length(2) | Type | Reserved | IPv4(4) | L4Proto | Port(2).
func ipv4Option(ip netip.Addr, l4 uint8, port uint16) []byte {
	ip = ip.Unmap()
	b := make([]byte, 11)
	b[0] = 0x00
	b[1] = 0x09
	b[2] = OptionIPv4Endpoint
	as4 := ip.As4()
	copy(b[4:8], as4[:])
	b[8] = l4
	b[9] = byte(port >> 8)
	b[10] = byte(port)
	return b
}

// entry builds a 16-byte SD service entry.
func entry(typ, idx1, num1, idx2, num2 uint8, key ServiceKey, ttl uint32, minor uint32) []byte {
	b := make([]byte, 16)
	b[0] = typ
	b[1] = idx1
	b[2] = idx2
	b[3] = num1<<4 | num2
	b[4] = byte(key.ServiceID >> 8)
	b[5] = byte(key.ServiceID)
	b[6] = byte(key.InstanceID >> 8)
	b[7] = byte(key.InstanceID)
	b[8] = key.MajorVersion
	b[9] = byte(ttl >> 16)
	b[10] = byte(ttl >> 8)
	b[11] = byte(ttl)
	b[12] = byte(minor >> 24)
	b[13] = byte(minor >> 16)
	b[14] = byte(minor >> 8)
	b[15] = byte(minor)
	return b
}

// packet assembles a complete SOME/IP-SD datagram from raw entries/options.
func packet(entries, options [][]byte) []byte {
	entryBytes := make([]byte, 0, 16*len(entries))
	for _, e := range entries {
		entryBytes = append(entryBytes, e...)
	}
	optionBytes := make([]byte, 0)
	for _, o := range options {
		optionBytes = append(optionBytes, o...)
	}

	sd := make([]byte, 0, sdHeaderLen+len(entryBytes)+len(optionBytes))
	sd = append(sd,
		0x00, 0x00, 0x00, 0x00, // reserved + flags (none set)
		byte(len(entryBytes)>>24), byte(len(entryBytes)>>16),
		byte(len(entryBytes)>>8), byte(len(entryBytes)),
		byte(len(optionBytes)>>24), byte(len(optionBytes)>>16),
		byte(len(optionBytes)>>8), byte(len(optionBytes)),
	)
	sd = append(sd, entryBytes...)
	sd = append(sd, optionBytes...)

	total := 8 + len(sd)
	p := make([]byte, 0, someIPHeaderLen+len(sd))
	p = append(p,
		0xFF, 0xFF, 0x81, 0x00, // SD message ID
		byte(total>>24), byte(total>>16), byte(total>>8), byte(total),
		0x00, 0x00, 0x00, 0x01, // client ID 0, session ID 1
		sdProtocolV8, sdInterfaceV1, sdMessageType, 0x00,
	)
	return append(p, sd...)
}

func offerPacket(ep netip.Addr, ttl uint32) []byte {
	opt := ipv4Option(ep, L4UDP, 30501)
	e := entry(EntryOfferService, 0, 1, 0, 0, keySvc, ttl, 0x00000001)
	return packet([][]byte{e}, [][]byte{opt})
}

func subscribePacket(idx, num1, idx2, num2 uint8, optAddr netip.Addr) []byte {
	opt := ipv4Option(optAddr, L4UDP, 30502)
	e := entry(EntrySubscribe, idx, num1, idx2, num2, keySvc, 10, 0)
	return packet([][]byte{e}, [][]byte{opt})
}

// 抢占 Offer：只有登记过的提供者地址能提供该服务，攻击者的 Offer 必须被拒。
func TestSquattingOfferRejected(t *testing.T) {
	c := NewController()
	c.RegisterProvider(keySvc, addrProvider)

	verdicts, err := c.AdmitPacket(offerPacket(addrAttacker, 3600), addrAttacker)
	if err != nil {
		t.Fatalf("admit attacker offer: %v", err)
	}
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonProviderNotAllowed {
		t.Fatalf("attacker offer = %+v, want reject/%s", verdicts[0], ReasonProviderNotAllowed)
	}
	if _, ok := c.CurrentProvider(keySvc); ok {
		t.Fatal("attacker must not become the active provider")
	}

	verdicts, err = c.AdmitPacket(offerPacket(addrProvider, 3600), addrProvider)
	if err != nil {
		t.Fatalf("admit legit offer: %v", err)
	}
	if verdicts[0].Verdict != VerdictAccept {
		t.Fatalf("legit offer = %+v, want accept", verdicts[0])
	}
	current, ok := c.CurrentProvider(keySvc)
	if !ok || current != addrProvider {
		t.Fatalf("active provider = %v (%v), want %v", current, ok, addrProvider)
	}

	// 提供者已存在后，攻击者再次抢占仍被拒，当前提供者不变。
	verdicts, _ = c.AdmitPacket(offerPacket(addrAttacker, 3600), addrAttacker)
	if verdicts[0].Verdict != VerdictReject {
		t.Fatalf("second attacker offer = %+v, want reject", verdicts[0])
	}
	current, _ = c.CurrentProvider(keySvc)
	if current != addrProvider {
		t.Fatalf("active provider changed to %v, want %v", current, addrProvider)
	}

	// 完全未登记的服务元组没有登记记录，拒绝原因不同。
	unknown := ServiceKey{ServiceID: 0x9999, InstanceID: 0x0001, MajorVersion: 0x01}
	e := entry(EntryOfferService, 0, 0, 0, 0, unknown, 3600, 0)
	verdicts, _ = c.AdmitPacket(packet([][]byte{e}, nil), addrAttacker)
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonServiceNotRegistered {
		t.Fatalf("unregistered offer = %+v, want reject/%s", verdicts[0], ReasonServiceNotRegistered)
	}
}

// 伪造 StopOffer：TTL 为 0 的撤回只允许当前提供者发起。
func TestForgedStopOfferRejected(t *testing.T) {
	c := NewController()
	c.RegisterProvider(keySvc, addrProvider)

	if vs, err := c.AdmitPacket(offerPacket(addrProvider, 3600), addrProvider); err != nil || vs[0].Verdict != VerdictAccept {
		t.Fatalf("setup offer failed: %+v, %v", vs, err)
	}

	// 攻击者伪造 TTL=0 的 StopOffer，必须拒绝，服务仍然在线。
	verdicts, err := c.AdmitPacket(offerPacket(addrAttacker, 0), addrAttacker)
	if err != nil {
		t.Fatalf("admit forged stop: %v", err)
	}
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonStopOfferFromNonProvider {
		t.Fatalf("forged stop = %+v, want reject/%s", verdicts[0], ReasonStopOfferFromNonProvider)
	}
	if current, ok := c.CurrentProvider(keySvc); !ok || current != addrProvider {
		t.Fatalf("provider withdrawn by forged StopOffer: %v (%v)", current, ok)
	}

	// 当前提供者自己的 StopOffer 才被接受并清除在线状态。
	verdicts, err = c.AdmitPacket(offerPacket(addrProvider, 0), addrProvider)
	if err != nil {
		t.Fatalf("admit legit stop: %v", err)
	}
	if verdicts[0].Verdict != VerdictAccept {
		t.Fatalf("legit stop = %+v, want accept", verdicts[0])
	}
	if _, ok := c.CurrentProvider(keySvc); ok {
		t.Fatal("active provider should be cleared after its own StopOffer")
	}

	// 没有活跃提供者时，任何 StopOffer 都不成立。
	verdicts, _ = c.AdmitPacket(offerPacket(addrProvider, 0), addrProvider)
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonStopOfferFromNonProvider {
		t.Fatalf("stop without active offer = %+v, want reject", verdicts[0])
	}
}

// 反射订阅：Subscribe 端点选项必须等于报文源地址；越界索引条目丢弃。
func TestReflectionSubscribeAndOutOfBounds(t *testing.T) {
	c := NewController()

	// 攻击者把订阅端点写成受害者地址，事件流量会被反射给受害者，拒绝。
	verdicts, err := c.AdmitPacket(subscribePacket(0, 1, 0, 0, addrVictim), addrAttacker)
	if err != nil {
		t.Fatalf("admit reflected subscribe: %v", err)
	}
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonEndpointSourceMismatch {
		t.Fatalf("reflected subscribe = %+v, want reject/%s", verdicts[0], ReasonEndpointSourceMismatch)
	}

	// 端点与源地址一致的正常订阅放行。
	verdicts, err = c.AdmitPacket(subscribePacket(0, 1, 0, 0, addrAttacker), addrAttacker)
	if err != nil {
		t.Fatalf("admit normal subscribe: %v", err)
	}
	if verdicts[0].Verdict != VerdictAccept {
		t.Fatalf("normal subscribe = %+v, want accept", verdicts[0])
	}

	// 选项索引指向不存在的选项：条目丢弃而不是拒绝整条报文。
	verdicts, err = c.AdmitPacket(subscribePacket(5, 1, 0, 0, addrAttacker), addrAttacker)
	if err != nil {
		t.Fatalf("admit oob subscribe: %v", err)
	}
	if verdicts[0].Verdict != VerdictDrop || verdicts[0].Reason != ReasonOptionIndexOutOfBounds {
		t.Fatalf("oob subscribe = %+v, want drop/%s", verdicts[0], ReasonOptionIndexOutOfBounds)
	}

	// 第二个选项运行越界（只有 1 个选项却声明从索引 1 开始）同样丢弃。
	verdicts, _ = c.AdmitPacket(subscribePacket(0, 1, 1, 1, addrAttacker), addrAttacker)
	if verdicts[0].Verdict != VerdictDrop || verdicts[0].Reason != ReasonOptionIndexOutOfBounds {
		t.Fatalf("oob second run = %+v, want drop", verdicts[0])
	}

	// 不带选项运行的订阅没有可反射的端点，正常放行。
	noOpt := packet(
		[][]byte{entry(EntrySubscribe, 0, 0, 0, 0, keySvc, 10, 0)},
		nil,
	)
	verdicts, _ = c.AdmitPacket(noOpt, addrAttacker)
	if verdicts[0].Verdict != VerdictAccept {
		t.Fatalf("optionless subscribe = %+v, want accept", verdicts[0])
	}
}
