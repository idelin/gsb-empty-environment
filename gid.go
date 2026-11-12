package netsec

import (
	"errors"
	"net/netip"
)

// GID 是 RoCE v2 的 128 位全局标识符，宽度与 IPv6 地址相同；
// IPv4 网络上的 GID 采用 IPv4-mapped IPv6 形式（::ffff:a.b.c.d）。
type GID [16]byte

var errGIDLength = errors.New("netsec: GID 必须为 16 字节")

// GIDFromBytes 由 16 字节切片构造 GID。
func GIDFromBytes(b []byte) (GID, error) {
	var g GID
	if len(b) != len(g) {
		return GID{}, errGIDLength
	}
	copy(g[:], b)
	return g, nil
}

// MustGID 由 16 字节构造 GID，长度不符时 panic。
func MustGID(b []byte) GID {
	g, err := GIDFromBytes(b)
	if err != nil {
		panic(err)
	}
	return g
}

// GIDFromAddr 把 IP 地址映射为 GID；IPv4 地址编码为 IPv4-mapped 形式。
func GIDFromAddr(addr netip.Addr) GID {
	var g GID
	addr = addr.WithZone("")
	if !addr.IsValid() {
		return g
	}
	if addr.Is4() {
		g[10] = 0xff
		g[11] = 0xff
		copy(g[12:], addr.AsSlice())
		return g
	}
	copy(g[:], addr.AsSlice())
	return g
}

// String 返回 GID 的标准文本形式（IPv4-mapped 时还原为点分十进制）。
func (g GID) String() string {
	return netip.AddrFrom16(g).Unmap().String()
}
