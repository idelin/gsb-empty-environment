package netsec_test

import (
	"net/netip"
	"testing"

	netsec "example.org/netsec/q0390"
)

func TestGIDFromBytesAndString(t *testing.T) {
	b := make([]byte, 16)
	b[15] = 1
	g, err := netsec.GIDFromBytes(b)
	if err != nil {
		t.Fatalf("GIDFromBytes: %v", err)
	}
	if g != netsec.MustGID(b) {
		t.Fatal("GIDFromBytes 与 MustGID 结果不一致")
	}
	if _, err := netsec.GIDFromBytes(make([]byte, 15)); err == nil {
		t.Fatal("15 字节输入应报错")
	}
}

func TestGIDFromAddr(t *testing.T) {
	v4 := netip.MustParseAddr("192.0.2.10")
	g4 := netsec.GIDFromAddr(v4)
	if g4.String() != "192.0.2.10" {
		t.Fatalf("IPv4-mapped GID 文本应为 192.0.2.10, 得到 %s", g4)
	}
	var want netsec.GID
	want[10], want[11] = 0xff, 0xff
	want[12], want[13], want[14], want[15] = 192, 0, 2, 10
	if g4 != want {
		t.Fatalf("IPv4-mapped 编码错误: %#v", g4)
	}

	v6 := netip.MustParseAddr("2001:db8::1")
	g6 := netsec.GIDFromAddr(v6)
	if g6.String() != "2001:db8::1" {
		t.Fatalf("IPv6 GID 文本错误: %s", g6)
	}

	// 不同地址必须映射为不同 GID，保证源 GID 校验具备区分度。
	if netsec.GIDFromAddr(netip.MustParseAddr("192.0.2.10")) ==
		netsec.GIDFromAddr(netip.MustParseAddr("192.0.2.11")) {
		t.Fatal("不同 IP 的 GID 不得相同")
	}
}
