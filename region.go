package main

import (
	"net/netip"
	"sort"
)

// portInterval 是闭区间 [lo, hi] 的目的端口范围。
type portInterval struct {
	lo, hi uint16
}

// region 表示匹配空间的一块：前缀集合 × 协议集合 × 端口区间集合 的笛卡尔积。
// prefixes 内部互不重叠，ports 内部互不重叠且升序。
type region struct {
	prefixes []netip.Prefix
	protos   Protocol
	ports    []portInterval
}

func regionOf(r Rule) region {
	return region{
		prefixes: []netip.Prefix{r.Prefix.Masked()},
		protos:   r.Protos,
		ports:    []portInterval{{r.PortMin, r.PortMax}},
	}
}

func sameFamily(a, b region) bool {
	if len(a.prefixes) == 0 || len(b.prefixes) == 0 {
		return true
	}
	return a.prefixes[0].Addr().Is4() == b.prefixes[0].Addr().Is4()
}

// intersectRegion 返回两区域的交集；不相交时 ok=false。
// IPv4 与 IPv6 区域永远不相交。
func intersectRegion(a, b region) (region, bool) {
	if !sameFamily(a, b) {
		return region{}, false
	}
	pre := intersectPrefixes(a.prefixes, b.prefixes)
	if len(pre) == 0 {
		return region{}, false
	}
	pro := a.protos & b.protos
	if pro == 0 {
		return region{}, false
	}
	po := intersectPorts(a.ports, b.ports)
	if len(po) == 0 {
		return region{}, false
	}
	return region{prefixes: pre, protos: pro, ports: po}, true
}

// subtractRegion 计算 a \ b，返回互不重叠的区域列表。
// 分解式：a\b = (preDiff × Pa × Ta) ∪ (preInter × (Pa\Pb) × Ta) ∪ (preInter × (Pa∩Pb) × portDiff)
func subtractRegion(a, b region) []region {
	inter, ok := intersectRegion(a, b)
	if !ok {
		return []region{a}
	}
	var out []region
	if preDiff := subtractPrefixes(a.prefixes, b.prefixes); len(preDiff) > 0 {
		out = append(out, region{prefixes: preDiff, protos: a.protos, ports: a.ports})
	}
	if protoDiff := a.protos &^ b.protos; protoDiff != 0 {
		out = append(out, region{prefixes: inter.prefixes, protos: protoDiff, ports: a.ports})
	}
	if portDiff := subtractPorts(a.ports, b.ports); len(portDiff) > 0 {
		out = append(out, region{prefixes: inter.prefixes, protos: inter.protos, ports: portDiff})
	}
	return out
}

// samplePacket 从区域中构造一个合成包作为反例/见证。
// 源地址使用文档保留段（192.0.2.0/24、2001:db8::/32），纯合成，不对应真实主机。
func samplePacket(r region) Packet {
	dst := r.prefixes[0].Addr()
	proto := ProtoTCP
	for _, p := range []Protocol{ProtoTCP, ProtoUDP, ProtoICMP} {
		if r.protos&p != 0 {
			proto = p
			break
		}
	}
	src := netip.MustParseAddr("192.0.2.1")
	if !dst.Is4() {
		src = netip.MustParseAddr("2001:db8::1")
	}
	return Packet{
		Src:       src,
		Dst:       dst,
		Proto:     proto,
		SrcPort:   49152,
		DstPort:   r.ports[0].lo,
		SizeBytes: 128,
	}
}

// --- 前缀集合运算 ---

func intersectPrefixes(a, b []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range a {
		for _, q := range b {
			if !p.Overlaps(q) {
				continue
			}
			if p.Bits() >= q.Bits() {
				out = append(out, p)
			} else {
				out = append(out, q)
			}
		}
	}
	return out
}

func subtractPrefixes(a, b []netip.Prefix) []netip.Prefix {
	out := a
	for _, q := range b {
		var next []netip.Prefix
		for _, p := range out {
			next = append(next, subtractPrefix(p, q)...)
		}
		out = next
	}
	return out
}

// subtractPrefix 计算 p \ q（两者同族）。
func subtractPrefix(p, q netip.Prefix) []netip.Prefix {
	p, q = p.Masked(), q.Masked()
	if !p.Overlaps(q) {
		return []netip.Prefix{p}
	}
	if q.Bits() <= p.Bits() {
		return nil // q 完全覆盖 p
	}
	// q 严格位于 p 内部：二分 p 后递归。
	lo, hi := splitPrefix(p)
	var out []netip.Prefix
	if lo.Overlaps(q) {
		out = append(out, subtractPrefix(lo, q)...)
	} else {
		out = append(out, lo)
	}
	if hi.Overlaps(q) {
		out = append(out, subtractPrefix(hi, q)...)
	} else {
		out = append(out, hi)
	}
	return out
}

// splitPrefix 把前缀分裂为两个 bits+1 的子前缀。调用方保证 p.Bits() < 地址宽度。
func splitPrefix(p netip.Prefix) (netip.Prefix, netip.Prefix) {
	bits := p.Bits()
	addr := p.Addr()
	if addr.Is4() {
		a4 := addr.As4()
		lo := netip.PrefixFrom(netip.AddrFrom4(a4), bits+1)
		a4[bits/8] |= 1 << (7 - uint(bits%8))
		hi := netip.PrefixFrom(netip.AddrFrom4(a4), bits+1)
		return lo.Masked(), hi.Masked()
	}
	a16 := addr.As16()
	lo := netip.PrefixFrom(netip.AddrFrom16(a16), bits+1)
	a16[bits/8] |= 1 << (7 - uint(bits%8))
	hi := netip.PrefixFrom(netip.AddrFrom16(a16), bits+1)
	return lo.Masked(), hi.Masked()
}

// --- 端口区间集合运算 ---

func normalizePorts(iv []portInterval) []portInterval {
	if len(iv) == 0 {
		return nil
	}
	sorted := append([]portInterval(nil), iv...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].lo < sorted[j].lo })
	out := []portInterval{sorted[0]}
	for _, x := range sorted[1:] {
		last := &out[len(out)-1]
		if last.hi == 65535 || x.lo <= last.hi+1 {
			if x.hi > last.hi {
				last.hi = x.hi
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

func intersectPorts(a, b []portInterval) []portInterval {
	var out []portInterval
	for _, x := range a {
		for _, y := range b {
			lo := max(x.lo, y.lo)
			hi := min(x.hi, y.hi)
			if lo <= hi {
				out = append(out, portInterval{lo, hi})
			}
		}
	}
	return out
}

func subtractPorts(a, b []portInterval) []portInterval {
	out := a
	for _, y := range b {
		var next []portInterval
		for _, x := range out {
			if y.hi < x.lo || y.lo > x.hi {
				next = append(next, x)
				continue
			}
			if x.lo < y.lo {
				next = append(next, portInterval{x.lo, y.lo - 1})
			}
			if x.hi > y.hi {
				next = append(next, portInterval{y.hi + 1, x.hi})
			}
		}
		out = next
	}
	return out
}
