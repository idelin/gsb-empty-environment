package netsec

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

var (
	llcpDstMAC = net.HardwareAddr{0x01, 0x80, 0xC2, 0x00, 0x00, 0x0E}
	macA       = net.HardwareAddr{0xAA, 0xBB, 0xCC, 0x00, 0x00, 0x01}
	macB       = net.HardwareAddr{0xAA, 0xBB, 0xCC, 0x00, 0x00, 0x02}
	macOther   = net.HardwareAddr{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}
)

// tlv 按 IEEE 802.1AB 的 7 位类型 + 9 位长度头部编码一个 TLV。
func tlv(tlvType uint8, value []byte) []byte {
	length := len(value)
	out := []byte{
		byte(tlvType)<<1 | byte(length>>8),
		byte(length & 0xFF),
	}
	return append(out, value...)
}

// frame 组装完整以太网帧，dst 为空时使用标准 LLDP 组播地址。
func frame(dst, src net.HardwareAddr, etherType uint16, lldp []byte) []byte {
	if dst == nil {
		dst = llcpDstMAC
	}
	head := make([]byte, 14)
	copy(head[0:6], dst)
	copy(head[6:12], src)
	head[12] = byte(etherType >> 8)
	head[13] = byte(etherType)
	return append(head, lldp...)
}

// validLLDP 构造顺序合法的 LLDP PDU：机箱 ID、端口 ID、TTL、可选 TLV、结束 TLV。
func validLLDP(chassis, port []byte, ttl uint16, extra ...[]byte) []byte {
	pdu := append([]byte(nil), tlv(tlvChassisID, chassis)...)
	pdu = append(pdu, tlv(tlvPortID, port)...)
	pdu = append(pdu, tlv(tlvTimeToLive, []byte{byte(ttl >> 8), byte(ttl)})...)
	for _, e := range extra {
		pdu = append(pdu, e...)
	}
	return append(pdu, tlv(tlvEndOfPDU, nil)...)
}

func TestAdmitAcceptsValidFrame(t *testing.T) {
	table := NewTable(4)
	chassis := []byte{4, 's', 'w', '1'}
	port := []byte{3, 'p', '1'}
	pkt := frame(nil, macA, etherTypeLLDP, validLLDP(chassis, port, 120))

	entry, err := table.Admit(1, pkt)
	if err != nil {
		t.Fatalf("Admit() unexpected error: %v", err)
	}
	if entry == nil || entry.PortID != 1 || entry.TTL != 120*time.Second {
		t.Fatalf("Admit() returned bad entry: %+v", entry)
	}
	if !bytes.Equal(entry.SrcMAC, macA) {
		t.Fatalf("entry source MAC = %v, want %v", entry.SrcMAC, macA)
	}
	if table.Lookup(1, chassis, port) == nil {
		t.Fatal("neighbor not stored")
	}
	if table.Stats().Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1", table.Stats().Accepted)
	}
}

func TestRejectWrongDestinationMAC(t *testing.T) {
	table := NewTable(4)
	wrongDst := net.HardwareAddr{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	pkt := frame(wrongDst, macA, etherTypeLLDP,
		validLLDP([]byte{4, 's'}, []byte{3, 'p'}, 10))

	if _, err := table.Admit(1, pkt); !errors.Is(err, ErrWrongDestination) {
		t.Fatalf("err = %v, want ErrWrongDestination", err)
	}
	if table.Stats().RejectedMalformed != 1 {
		t.Fatal("malformed rejection not counted")
	}
}

func TestRejectWrongEtherType(t *testing.T) {
	table := NewTable(4)
	pkt := frame(nil, macA, 0x0800,
		validLLDP([]byte{4, 's'}, []byte{3, 'p'}, 10))

	if _, err := table.Admit(1, pkt); !errors.Is(err, ErrWrongEtherType) {
		t.Fatalf("err = %v, want ErrWrongEtherType", err)
	}
}

func TestRejectOutOfOrderTLVs(t *testing.T) {
	chassis := []byte{4, 's'}
	port := []byte{3, 'p'}
	ttl := tlv(tlvTimeToLive, []byte{0, 10})

	cases := map[string][]byte{
		"TTL before Port ID": append(append(
			tlv(tlvChassisID, chassis), ttl...),
			tlv(tlvPortID, port)...),
		"Port ID first": append(append(
			tlv(tlvPortID, port),
			tlv(tlvChassisID, chassis)...), ttl...),
		"missing TTL": append(
			tlv(tlvChassisID, chassis),
			tlv(tlvPortID, port)...),
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			table := NewTable(4)
			pkt := frame(nil, macA, etherTypeLLDP,
				append(body, tlv(tlvEndOfPDU, nil)...))
			if _, err := table.Admit(1, pkt); !errors.Is(err, ErrOutOfOrderTLV) {
				t.Fatalf("err = %v, want ErrOutOfOrderTLV", err)
			}
		})
	}
}

func TestRejectDuplicateMandatoryTLV(t *testing.T) {
	table := NewTable(4)
	chassis := []byte{4, 's'}
	port := []byte{3, 'p'}
	body := validLLDP(chassis, port, 10, tlv(tlvChassisID, []byte{4, 'x'}))
	pkt := frame(nil, macA, etherTypeLLDP, body)

	if _, err := table.Admit(1, pkt); !errors.Is(err, ErrOutOfOrderTLV) {
		t.Fatalf("err = %v, want ErrOutOfOrderTLV for repeated Chassis ID", err)
	}
}

func TestRejectDataAfterEndTLV(t *testing.T) {
	table := NewTable(4)
	body := validLLDP([]byte{4, 's'}, []byte{3, 'p'}, 10)
	body = append(body, 0xAA) // 结束 TLV 之后的额外字节
	pkt := frame(nil, macA, etherTypeLLDP, body)

	if _, err := table.Admit(1, pkt); !errors.Is(err, ErrMalformedTLV) {
		t.Fatalf("err = %v, want ErrMalformedTLV for trailing data", err)
	}
}

func TestRejectEndTLVWithNonZeroLength(t *testing.T) {
	table := NewTable(4)
	body := validLLDP([]byte{4, 's'}, []byte{3, 'p'}, 10)
	body = body[:len(body)-2] // 去掉合法的零长度 End TLV
	body = append(body, tlv(tlvEndOfPDU, []byte{0x01})...)
	pkt := frame(nil, macA, etherTypeLLDP, body)

	if _, err := table.Admit(1, pkt); !errors.Is(err, ErrMalformedTLV) {
		t.Fatalf("err = %v, want ErrMalformedTLV for non-zero End TLV length", err)
	}
}

func TestFloodRejectedAtPerPortLimit(t *testing.T) {
	const max = 4
	table := NewTable(max)

	// 攻击者用不同机箱 ID 洪泛，试图塞满邻居表。
	for i := 0; i < max; i++ {
		chassis := []byte{4, 's', 'w', byte(i)}
		port := []byte{3, 'p', '1'}
		if _, err := table.Admit(1, frame(nil, macB, etherTypeLLDP,
			validLLDP(chassis, port, 60))); err != nil {
			t.Fatalf("admit %d failed: %v", i, err)
		}
	}

	// 第 max+1 个新邻居必须被容量限制拒绝。
	_, err := table.Admit(1, frame(nil, macB, etherTypeLLDP,
		validLLDP([]byte{4, 'e', 'v', 'i', 'l'}, []byte{3, 'x'}, 60)))
	if !errors.Is(err, ErrTableFull) {
		t.Fatalf("err = %v, want ErrTableFull", err)
	}
	if len(table.Entries(1)) != max {
		t.Fatalf("entries = %d, want %d", len(table.Entries(1)), max)
	}
	if table.Stats().RejectedFull != 1 {
		t.Fatalf("RejectedFull = %d, want 1", table.Stats().RejectedFull)
	}

	// 表满时已有邻居的刷新不受影响。
	refresh, err := table.Admit(1, frame(nil, macB, etherTypeLLDP,
		validLLDP([]byte{4, 's', 'w', 0}, []byte{3, 'p', '1'}, 90)))
	if err != nil {
		t.Fatalf("refresh after full: %v", err)
	}
	if refresh.TTL != 90*time.Second {
		t.Fatalf("refreshed TTL = %v, want 90s", refresh.TTL)
	}

	// 端口之间容量相互独立，其他端口仍可学习。
	if _, err := table.Admit(2, frame(nil, macB, etherTypeLLDP,
		validLLDP([]byte{4, 'o', 't', 'h', 'e', 'r'}, []byte{3, 'p'}, 60))); err != nil {
		t.Fatalf("other port admit failed: %v", err)
	}
}

func TestTTLZeroWithdrawalOnlyByOwner(t *testing.T) {
	table := NewTable(4)
	chassis := []byte{4, 'r', 'e', 'a', 'l'}
	port := []byte{3, 'p', '9'}

	// 真实邻居 macA 建立条目。
	if _, err := table.Admit(1, frame(nil, macA, etherTypeLLDP,
		validLLDP(chassis, port, 120))); err != nil {
		t.Fatal(err)
	}

	// 攻击者 macOther 伪造相同机箱 ID/端口 ID、TTL=0 的撤销帧。
	_, err := table.Admit(1, frame(nil, macOther, etherTypeLLDP,
		validLLDP(chassis, port, 0)))
	if !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("attacker withdrawal err = %v, want ErrSourceMismatch", err)
	}
	if table.Lookup(1, chassis, port) == nil {
		t.Fatal("real neighbor erased by attacker TTL=0 frame")
	}

	// 对不存在的邻居发 TTL=0 同样不能产生任何状态。
	_, err = table.Admit(1, frame(nil, macOther, etherTypeLLDP,
		validLLDP([]byte{4, 'g', 'h', 'o', 's', 't'}, port, 0)))
	if !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("ghost withdrawal err = %v, want ErrSourceMismatch", err)
	}

	// 属主自己的 TTL=0 撤销生效。
	entry, err := table.Admit(1, frame(nil, macA, etherTypeLLDP,
		validLLDP(chassis, port, 0)))
	if err != nil {
		t.Fatalf("owner withdrawal failed: %v", err)
	}
	if entry != nil {
		t.Fatalf("withdrawal returned entry %+v, want nil", entry)
	}
	if table.Lookup(1, chassis, port) != nil {
		t.Fatal("neighbor still present after owner withdrawal")
	}
	stats := table.Stats()
	if stats.Withdrawn != 1 || stats.RejectedSource != 2 {
		t.Fatalf("stats = %+v, want Withdrawn=1 RejectedSource=2", stats)
	}
}

func TestRefreshRejectedFromDifferentSourceMAC(t *testing.T) {
	table := NewTable(4)
	chassis := []byte{4, 'r'}
	port := []byte{3, 'p'}

	if _, err := table.Admit(1, frame(nil, macA, etherTypeLLDP,
		validLLDP(chassis, port, 120))); err != nil {
		t.Fatal(err)
	}

	// 攻击者伪造同样的机箱/端口 ID 但带正常 TTL，试图覆盖条目。
	if _, err := table.Admit(1, frame(nil, macOther, etherTypeLLDP,
		validLLDP(chassis, port, 5))); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("err = %v, want ErrSourceMismatch", err)
	}
	entry := table.Lookup(1, chassis, port)
	if !bytes.Equal(entry.SrcMAC, macA) || entry.TTL != 120*time.Second {
		t.Fatalf("entry tampered: %+v", entry)
	}

	// 属主刷新成功。
	if _, err := table.Admit(1, frame(nil, macA, etherTypeLLDP,
		validLLDP(chassis, port, 200))); err != nil {
		t.Fatal(err)
	}
	if table.Lookup(1, chassis, port).TTL != 200*time.Second {
		t.Fatal("owner refresh did not update TTL")
	}
}

func TestSweepExpiresEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	table := NewTable(4)
	table.now = func() time.Time { return now }

	chassis := []byte{4, 's'}
	if _, err := table.Admit(1, frame(nil, macA, etherTypeLLDP,
		validLLDP(chassis, []byte{3, 'p'}, 10))); err != nil {
		t.Fatal(err)
	}

	table.now = func() time.Time { return now.Add(10 * time.Second) }
	if aged := table.Sweep(); aged != 1 {
		t.Fatalf("Sweep() = %d, want 1", aged)
	}
	if table.Lookup(1, chassis, []byte{3, 'p'}) != nil {
		t.Fatal("expired entry still present")
	}
	if table.Stats().AgedOut != 1 {
		t.Fatalf("AgedOut = %d, want 1", table.Stats().AgedOut)
	}

	// 老化腾出的容量可以重新学习。
	if _, err := table.Admit(1, frame(nil, macA, etherTypeLLDP,
		validLLDP(chassis, []byte{3, 'p'}, 30))); err != nil {
		t.Fatalf("re-learn after aging failed: %v", err)
	}
}

func TestAdmitRejectsTruncatedFrames(t *testing.T) {
	table := NewTable(2)
	cases := map[string][]byte{
		"shorter than ethernet header": {0x01, 0x80, 0xC2},
		"no TLV at all":                frame(nil, macA, etherTypeLLDP, nil),
		"TLV header truncated": append(frame(nil, macA, etherTypeLLDP, nil),
			0x02),
		"TLV value truncated": append(frame(nil, macA, etherTypeLLDP, nil),
			0x02, 0x05, 'x'),
	}
	for name, pkt := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := table.Admit(1, pkt); err == nil {
				t.Fatal("expected rejection of malformed frame")
			}
		})
	}
}

func TestConcurrentAdmitFromReceiveThreads(t *testing.T) {
	const ports, perPort, goroutinesPerPort = 8, 16, 4
	table := NewTable(perPort)

	var wg sync.WaitGroup
	for p := 0; p < ports; p++ {
		for g := 0; g < goroutinesPerPort; g++ {
			wg.Add(1)
			go func(port, worker int) {
				defer wg.Done()
				src := net.HardwareAddr{0x02, byte(port), byte(worker), 0, 0, 1}
				for i := 0; i < perPort*2; i++ {
					chassis := []byte{4, byte(port), byte(i)}
					// 容量内的会成功，超出的返回 ErrTableFull，两者都不应 panic。
					_, _ = table.Admit(port, frame(nil, src, etherTypeLLDP,
						validLLDP(chassis, []byte{3, 1}, 60)))
				}
			}(p, g)
		}
	}
	wg.Wait()

	for p := 0; p < ports; p++ {
		if len(table.Entries(p)) > perPort {
			t.Fatalf("port %d has %d entries, limit %d",
				p, len(table.Entries(p)), perPort)
		}
	}
}
