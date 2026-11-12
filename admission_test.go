package netsec

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
)

// 两个出口租户：租户 A 使用 VNI 100，租户 B 使用 VNI 200，各自有授权 VTEP。
var (
	vniA      = uint32(100)
	vniB      = uint32(200)
	vtepA     = netip.MustParseAddr("10.0.0.1")
	vtepB     = netip.MustParseAddr("10.0.0.2")
	vtepRogue = netip.MustParseAddr("192.0.2.66")
)

func newTestGateway() *Gateway {
	g := NewGateway()
	g.AuthorizeVTEP(vniA, vtepA)
	g.AuthorizeVTEP(vniB, vtepB)
	// 本端实现的已知 Geneve 选项。
	g.RegisterGeneveOption(0x0108, 0x01)
	return g
}

// innerUDPv4Frame 构造内层以太帧：Ethernet/IPv4/UDP。
func innerUDPv4Frame(dstPort uint16) []byte {
	frame := make([]byte, 14)
	binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv4)
	udpLen := 8 + 4
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+udpLen))
	ip[8] = 64
	ip[9] = ipProtoUDP
	udp := make([]byte, udpLen)
	binary.BigEndian.PutUint16(udp[0:2], 12345)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))
	frame = append(frame, ip...)
	return append(frame, udp...)
}

// innerTCPv4Frame 构造内层以太帧：Ethernet/IPv4/TCP（普通租户流量）。
func innerTCPv4Frame() []byte {
	frame := make([]byte, 14)
	binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv4)
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+20))
	ip[8] = 64
	ip[9] = 6
	return append(frame, append(ip, make([]byte, 20)...)...)
}

func vxlanPacket(vni uint32, flags byte, inner []byte) []byte {
	p := []byte{flags, 0, 0, 0, byte(vni >> 16), byte(vni >> 8), byte(vni), 0}
	return append(p, inner...)
}

type geneveOpt struct {
	class   uint16
	typ     uint8
	data    []byte // 会被填充到 4 字节对齐
	dataLen int    // 覆盖 Length 字段；-1 表示按 data 计算
}

func genevePacket(vni uint32, opts []geneveOpt, optLenWords int, inner []byte) []byte {
	var optBytes []byte
	for _, o := range opts {
		dataLen := o.dataLen
		if dataLen < 0 {
			dataLen = (len(o.data) + 3) / 4
		}
		hdr := []byte{byte(o.class >> 8), byte(o.class), o.typ, byte(dataLen)}
		optBytes = append(optBytes, hdr...)
		data := make([]byte, dataLen*4)
		copy(data, o.data)
		optBytes = append(optBytes, data...)
	}
	if optLenWords < 0 {
		optLenWords = len(optBytes) / 4
	}
	p := []byte{byte(optLenWords), 0, 0x65, 0x58, byte(vni >> 16), byte(vni >> 8), byte(vni), 0}
	p = append(p, optBytes...)
	return append(p, inner...)
}

func admit(t *testing.T, g *Gateway, proto Protocol, src netip.Addr, payload []byte) (*Decision, error) {
	t.Helper()
	return g.Admit(proto, src, 8+len(payload), payload)
}

func TestAdmitValidVXLAN(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	d, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x08, inner))
	if err != nil {
		t.Fatalf("valid VXLAN rejected: %v", err)
	}
	if d.VNI != vniA || len(d.Inner) != len(inner) {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestAdmitValidGeneve(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	opts := []geneveOpt{{class: 0x0108, typ: 0x01, data: []byte{1, 2, 3, 4}, dataLen: -1}}
	d, err := admit(t, g, ProtoGeneve, vtepB, genevePacket(vniB, opts, -1, inner))
	if err != nil {
		t.Fatalf("valid Geneve rejected: %v", err)
	}
	if d.VNI != vniB || len(d.Inner) != len(inner) {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

// 错源 VTEP：控制租户 B 的 VTEP 或任意外部地址，向租户 A 的 VNI 注入。
func TestRejectWrongSourceVTEP(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	cases := []struct {
		name  string
		proto Protocol
		src   netip.Addr
		pkt   []byte
	}{
		{"vxlan cross-tenant", ProtoVXLAN, vtepB, vxlanPacket(vniA, 0x08, inner)},
		{"vxlan rogue", ProtoVXLAN, vtepRogue, vxlanPacket(vniA, 0x08, inner)},
		{"geneve cross-tenant", ProtoGeneve, vtepA, genevePacket(vniB, nil, -1, inner)},
		{"geneve rogue", ProtoGeneve, vtepRogue, genevePacket(vniB, nil, -1, inner)},
		{"unknown vni", ProtoVXLAN, vtepA, vxlanPacket(300, 0x08, inner)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := admit(t, g, tc.proto, tc.src, tc.pkt); !errors.Is(err, ErrUnauthorizedVTEP) {
				t.Fatalf("want ErrUnauthorizedVTEP, got %v", err)
			}
		})
	}
}

// VXLAN 保留位非零或 I 标志缺失。
func TestRejectVXLANBadFlags(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	t.Run("missing I flag", func(t *testing.T) {
		if _, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x00, inner)); !errors.Is(err, ErrMissingIFlag) {
			t.Fatalf("want ErrMissingIFlag, got %v", err)
		}
	})
	t.Run("reserved flag bit", func(t *testing.T) {
		if _, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x09, inner)); !errors.Is(err, ErrReservedBits) {
			t.Fatalf("want ErrReservedBits, got %v", err)
		}
	})
	t.Run("reserved flag high bit", func(t *testing.T) {
		if _, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x88, inner)); !errors.Is(err, ErrReservedBits) {
			t.Fatalf("want ErrReservedBits, got %v", err)
		}
	})
	t.Run("reserved byte nonzero", func(t *testing.T) {
		pkt := vxlanPacket(vniA, 0x08, inner)
		pkt[7] = 0x01
		if _, err := admit(t, g, ProtoVXLAN, vtepA, pkt); !errors.Is(err, ErrReservedBits) {
			t.Fatalf("want ErrReservedBits, got %v", err)
		}
	})
}

// Geneve 未识别且置关键位的选项。
func TestRejectGeneveCriticalOption(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	t.Run("unknown critical", func(t *testing.T) {
		opts := []geneveOpt{{class: 0xffff, typ: 0x80, data: []byte{9, 9, 9, 9}, dataLen: -1}}
		if _, err := admit(t, g, ProtoGeneve, vtepA, genevePacket(vniA, opts, -1, inner)); !errors.Is(err, ErrCriticalOption) {
			t.Fatalf("want ErrCriticalOption, got %v", err)
		}
	})
	t.Run("unknown non-critical skipped", func(t *testing.T) {
		opts := []geneveOpt{{class: 0xffff, typ: 0x00, data: []byte{9, 9, 9, 9}, dataLen: -1}}
		if _, err := admit(t, g, ProtoGeneve, vtepA, genevePacket(vniA, opts, -1, inner)); err != nil {
			t.Fatalf("non-critical unknown option should be skipped, got %v", err)
		}
	})
	t.Run("known critical accepted", func(t *testing.T) {
		opts := []geneveOpt{{class: 0x0108, typ: 0x81, data: []byte{1, 2, 3, 4}, dataLen: -1}}
		if _, err := admit(t, g, ProtoGeneve, vtepA, genevePacket(vniA, opts, -1, inner)); err != nil {
			t.Fatalf("registered critical option should be accepted, got %v", err)
		}
	})
}

// Geneve 选项长度不自洽：声明越界或选项越过选项区。
func TestRejectGeneveOptionOverrun(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	t.Run("opt len beyond packet", func(t *testing.T) {
		pkt := genevePacket(vniA, nil, 63, inner) // 声明 252 字节选项，远超包长
		if _, err := admit(t, g, ProtoGeneve, vtepA, pkt); !errors.Is(err, ErrGeneveOptOverrun) {
			t.Fatalf("want ErrGeneveOptOverrun, got %v", err)
		}
	})
	t.Run("option data overruns area", func(t *testing.T) {
		// 选项声明 8 字节数据，但选项区只有 8 字节（头 4 + 数据仅 4）。
		opts := []geneveOpt{{class: 0x0108, typ: 0x01, data: []byte{1, 2, 3, 4}, dataLen: 2}}
		pkt := genevePacket(vniA, opts, 2, inner)
		if _, err := admit(t, g, ProtoGeneve, vtepA, pkt); !errors.Is(err, ErrGeneveOptOverrun) {
			t.Fatalf("want ErrGeneveOptOverrun, got %v", err)
		}
	})
	t.Run("trailing partial option", func(t *testing.T) {
		pkt := genevePacket(vniA, nil, 1, inner)
		// 选项区 4 字节但塞入 8 字节长的选项声明。
		pkt = append(pkt[:8], []byte{0x01, 0x08, 0x01, 1}...)
		pkt = append(pkt, inner...)
		if _, err := admit(t, g, ProtoGeneve, vtepA, pkt); !errors.Is(err, ErrGeneveOptOverrun) {
			t.Fatalf("want ErrGeneveOptOverrun, got %v", err)
		}
	})
}

// 嵌套封装：内层帧再指向隧道端口。
func TestRejectNestedEncapsulation(t *testing.T) {
	g := newTestGateway()
	for _, port := range []uint16{4789, 4790, 6081, 8472} {
		inner := innerUDPv4Frame(port)
		if _, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x08, inner)); !errors.Is(err, ErrNestedEncap) {
			t.Fatalf("port %d: want ErrNestedEncap, got %v", port, err)
		}
		if _, err := admit(t, g, ProtoGeneve, vtepA, genevePacket(vniA, nil, -1, inner)); !errors.Is(err, ErrNestedEncap) {
			t.Fatalf("port %d: want ErrNestedEncap, got %v", port, err)
		}
	}
	// 普通 UDP 流量（如 DNS）必须放行。
	if _, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x08, innerUDPv4Frame(53))); err != nil {
		t.Fatalf("ordinary UDP traffic rejected: %v", err)
	}
}

// 外层 UDP 声明长度与实收不一致；内层 IP 声明长度越界。
func TestRejectLengthMismatch(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	pkt := vxlanPacket(vniA, 0x08, inner)
	t.Run("udp length mismatch", func(t *testing.T) {
		if _, err := g.Admit(ProtoVXLAN, vtepA, 8+len(pkt)+4, pkt); !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("want ErrLengthMismatch, got %v", err)
		}
	})
	t.Run("inner ip total length overrun", func(t *testing.T) {
		bad := append([]byte(nil), inner...)
		binary.BigEndian.PutUint16(bad[14+2:14+4], uint16(len(inner))) // 超过实收
		if _, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x08, bad)); !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("want ErrLengthMismatch, got %v", err)
		}
	})
}

// 出口租户隔离：合法租户流量互不串扰，判定结果归属正确 VNI。
func TestTenantIsolation(t *testing.T) {
	g := newTestGateway()
	inner := innerTCPv4Frame()
	dA, err := admit(t, g, ProtoVXLAN, vtepA, vxlanPacket(vniA, 0x08, inner))
	if err != nil || dA.VNI != vniA {
		t.Fatalf("tenant A: %v %+v", err, dA)
	}
	dB, err := admit(t, g, ProtoGeneve, vtepB, genevePacket(vniB, nil, -1, inner))
	if err != nil || dB.VNI != vniB {
		t.Fatalf("tenant B: %v %+v", err, dB)
	}
}
