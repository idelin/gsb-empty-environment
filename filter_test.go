package netsec

import (
	"bytes"
	"testing"
)

// 测试中复用的关键地址与类型。
var (
	macBPDU    = []byte{0x01, 0x80, 0xC2, 0x00, 0x00, 0x00} // 802.1D 桥接组播
	macPAUSE   = []byte{0x01, 0x80, 0xC2, 0x00, 0x00, 0x01} // 802.3 PAUSE
	macEdge0F  = []byte{0x01, 0x80, 0xC2, 0x00, 0x00, 0x0F} // 保留区间上界
	macEdge10  = []byte{0x01, 0x80, 0xC2, 0x00, 0x00, 0x10} // 保留区间之外
	macUnicast = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	macSource  = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
)

// frame 组装一个带 14 字节以太网头的帧。
func frame(dst, src []byte, ethertype uint16, payload []byte) []byte {
	b := make([]byte, 14+len(payload))
	copy(b[0:6], dst)
	copy(b[6:12], src)
	b[12], b[13] = byte(ethertype>>8), byte(ethertype)
	copy(b[14:], payload)
	return b
}

// taggedFrame 按真实线格式构造带 VLAN 标签栈的帧：12-13 为首个标签类型，
// 每个标签占 4 字节（类型 + TCI），最内层标签之后 2 字节为内层 ethertype。
func taggedFrame(dst, src []byte, tags []uint16, innerEthertype uint16, payload []byte) []byte {
	b := make([]byte, 12+4*len(tags)+2+len(payload))
	copy(b[0:6], dst)
	copy(b[6:12], src)
	off := 12
	for _, t := range tags {
		b[off], b[off+1] = byte(t>>8), byte(t)
		b[off+2], b[off+3] = 0, 0 // TCI
		off += 4
	}
	b[off], b[off+1] = byte(innerEthertype>>8), byte(innerEthertype)
	copy(b[off+2:], payload)
	return b
}

// tunPI 构造带 4 字节包信息头（flags=0, proto 大端）的 TUN 报文。
func tunPI(proto uint16, packet []byte) []byte {
	b := make([]byte, 4+len(packet))
	b[2], b[3] = byte(proto>>8), byte(proto)
	copy(b[4:], packet)
	return b
}

func ipv4Packet() []byte {
	b := make([]byte, 20)
	b[0] = 0x45 // version=4, IHL=5
	return b
}

func ipv6Packet() []byte {
	b := make([]byte, 40)
	b[0] = 0x60 // version=6
	return b
}

func TestTAPReservedMulticast(t *testing.T) {
	llc := []byte{0x42, 0x42, 0x03, 0x00, 0x00, 0x00} // STP BPDU LLC 头
	cases := []struct {
		name string
		frm  []byte
	}{
		{"STP BPDU", frame(macBPDU, macSource, uint16(len(llc)), llc)},
		{"Ethernet PAUSE", frame(macPAUSE, macSource, 0x8808, make([]byte, 46))},
		{"reserved edge 0x0F", frame(macEdge0F, macSource, 0x0800, make([]byte, 20))},
	}
	f := New(Config{Mode: ModeTAP})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := f.Frame(tc.frm)
			if !v.Dropped() || v.Reason != ReasonReservedMulticast {
				t.Fatalf("got action=%s reason=%q, want drop/%s", v.Action, v.Reason, ReasonReservedMulticast)
			}
		})
	}
}

func TestTAPReservedRangeBoundary(t *testing.T) {
	f := New(Config{Mode: ModeTAP})
	// 0x10 已在保留区间之外，正常 IPv4 帧应放行。
	v := f.Frame(frame(macEdge10, macSource, 0x0800, make([]byte, 20)))
	if v.Dropped() {
		t.Fatalf("0x10 should be outside reserved range, got drop/%s", v.Reason)
	}
	if !v.Forwardable() {
		t.Fatalf("0x10 IPv4 frame should be forwardable, got %s", v.Action)
	}
}

func TestTAPLengthField(t *testing.T) {
	f := New(Config{Mode: ModeTAP})

	// 长度字段与实际载荷不一致 -> 丢弃。
	bad := frame(macUnicast, macSource, 10, make([]byte, 12))
	v := f.Frame(bad)
	if !v.Dropped() || v.Reason != ReasonLengthMismatch {
		t.Fatalf("length mismatch: got %s/%q", v.Action, v.Reason)
	}

	// 长度字段与载荷严格一致 -> 放行但默认不转发（仅本机）。
	good := frame(macUnicast, macSource, 10, make([]byte, 10))
	v = f.Frame(good)
	if v.Action != ActionLocal {
		t.Fatalf("consistent 802.3 frame: got action=%s, want local", v.Action)
	}
	if v.Forwardable() {
		t.Fatalf("802.3 LLC frame must not be forwarded by default")
	}
	if !bytes.Equal(v.Payload, good[14:]) {
		t.Fatalf("local payload does not point at LLC payload")
	}
}

func TestTAPNestedVLAN(t *testing.T) {
	payload := make([]byte, 20)
	cases := []struct {
		name string
		frm  []byte
	}{
		{"QinQ 88A8/8100", taggedFrame(macUnicast, macSource, []uint16{0x88A8, 0x8100}, 0x0800, payload)},
		{"double 8100", taggedFrame(macUnicast, macSource, []uint16{0x8100, 0x8100}, 0x0800, payload)},
		{"triple stack", taggedFrame(macUnicast, macSource, []uint16{0x88A8, 0x9100, 0x8100}, 0x0800, payload)},
	}
	f := New(Config{Mode: ModeTAP})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := f.Frame(tc.frm)
			if !v.Dropped() || v.Reason != ReasonNestedVLAN {
				t.Fatalf("got %s/%q, want drop/%s", v.Action, v.Reason, ReasonNestedVLAN)
			}
			if v.VLANTags < 2 {
				t.Fatalf("expected >=2 VLAN tags counted, got %d", v.VLANTags)
			}
		})
	}
}

func TestTAPSingleVLANAndNormal(t *testing.T) {
	f := New(Config{Mode: ModeTAP})

	// 单层 802.1Q 标签：剥标签后正常转发，内层类型 0x0800。
	single := taggedFrame(macUnicast, macSource, []uint16{0x8100}, 0x0800, make([]byte, 20))
	v := f.Frame(single)
	if v.Dropped() {
		t.Fatalf("single-tag frame dropped: %s", v.Reason)
	}
	if v.VLANTags != 1 || v.EtherType != 0x0800 {
		t.Fatalf("tags=%d ethertype=0x%04x", v.VLANTags, v.EtherType)
	}

	// 普通 ARP（0x0806）放行。
	arp := frame(macUnicast, macSource, 0x0806, make([]byte, 28))
	if v = f.Frame(arp); !v.Forwardable() {
		t.Fatalf("ARP should forward, got %s/%q", v.Action, v.Reason)
	}

	// 过短帧丢弃。
	if v = f.Frame(make([]byte, 13)); !v.Dropped() || v.Reason != ReasonTooShort {
		t.Fatalf("short frame: got %s/%q", v.Action, v.Reason)
	}
}

func TestTUNMissingPacketInfo(t *testing.T) {
	f := New(Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyIPv4})

	// 裸 IPv4 报文被当成 tun_pi 解析：不足 4 字节 -> MissingPacketInfo。
	if v := f.Frame(make([]byte, 3)); !v.Dropped() || v.Reason != ReasonMissingPacketInfo {
		t.Fatalf("truncated PI: got %s/%q", v.Action, v.Reason)
	}

	// 完整裸 IPv4（无 PI）：前 4 字节全 0，PI proto=0x0000 与 v4 不符，
	// 同样在包信息头环节被拒绝。
	v := f.Frame(ipv4Packet())
	if !v.Dropped() {
		t.Fatalf("frame without PI header accepted, reason=%q", v.Reason)
	}
	if v.Reason != ReasonPacketInfoMismatch && v.Reason != ReasonMissingPacketInfo {
		t.Fatalf("missing PI: got reason=%q", v.Reason)
	}
}

func TestTUNAddressFamily(t *testing.T) {
	v4 := ipv4Packet()
	v6 := ipv6Packet()

	t.Run("version nibble mismatch", func(t *testing.T) {
		// PI 声称 IPv4，载荷版本半字节却是 6 -> 地址族拒绝。
		f := New(Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyIPv4})
		v := f.Frame(tunPI(0x0800, v6))
		if !v.Dropped() || v.Reason != ReasonAddressFamily {
			t.Fatalf("got %s/%q, want drop/%s", v.Action, v.Reason, ReasonAddressFamily)
		}
	})

	t.Run("PI proto mismatch", func(t *testing.T) {
		// PI 声称 IPv6，端口仅允许 IPv4 -> 包信息头协议不符直接拒绝。
		f := New(Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyIPv4})
		v := f.Frame(tunPI(0x86DD, v6))
		if !v.Dropped() || v.Reason != ReasonPacketInfoMismatch {
			t.Fatalf("got %s/%q, want drop/%s", v.Action, v.Reason, ReasonPacketInfoMismatch)
		}
	})

	t.Run("valid IPv4 with PI", func(t *testing.T) {
		f := New(Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyIPv4})
		frm := tunPI(0x0800, v4)
		v := f.Frame(frm)
		if !v.Forwardable() {
			t.Fatalf("valid v4: got %s/%q", v.Action, v.Reason)
		}
		if !bytes.Equal(v.Payload, frm[4:]) {
			t.Fatalf("payload should start after tun_pi")
		}
	})

	t.Run("valid IPv6 with PI on v6 port", func(t *testing.T) {
		f := New(Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyIPv6})
		if v := f.Frame(tunPI(0x86DD, v6)); !v.Forwardable() {
			t.Fatalf("valid v6: got %s/%q", v.Action, v.Reason)
		}
	})

	t.Run("raw IP without PI", func(t *testing.T) {
		f := New(Config{Mode: ModeTUN, PacketInfo: false, Family: FamilyIPv4})
		if v := f.Frame(v4); !v.Forwardable() {
			t.Fatalf("raw v4: got %s/%q", v.Action, v.Reason)
		}
		// 裸 IPv6 在 v4-only 端口按版本半字节拒绝。
		if v := f.Frame(v6); !v.Dropped() || v.Reason != ReasonAddressFamily {
			t.Fatalf("raw v6 on v4 port: got %s/%q", v.Action, v.Reason)
		}
	})

	t.Run("dual stack", func(t *testing.T) {
		f := New(Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyAny})
		if v := f.Frame(tunPI(0x0800, v4)); !v.Forwardable() {
			t.Fatalf("any/v4: got %s/%q", v.Action, v.Reason)
		}
		if v := f.Frame(tunPI(0x86DD, v6)); !v.Forwardable() {
			t.Fatalf("any/v6: got %s/%q", v.Action, v.Reason)
		}
	})
}
