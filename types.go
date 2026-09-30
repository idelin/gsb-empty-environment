package main

import (
	"fmt"
	"net/netip"
	"strings"
)

// Protocol 是协议位掩码，可组合表示协议集合。
type Protocol uint8

const (
	ProtoTCP Protocol = 1 << iota
	ProtoUDP
	ProtoICMP
	ProtoAny = ProtoTCP | ProtoUDP | ProtoICMP
)

func (p Protocol) String() string {
	if p == ProtoAny {
		return "any"
	}
	var parts []string
	if p&ProtoTCP != 0 {
		parts = append(parts, "tcp")
	}
	if p&ProtoUDP != 0 {
		parts = append(parts, "udp")
	}
	if p&ProtoICMP != 0 {
		parts = append(parts, "icmp")
	}
	return strings.Join(parts, ",")
}

func parseProtocolName(s string) (Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tcp":
		return ProtoTCP, nil
	case "udp":
		return ProtoUDP, nil
	case "icmp":
		return ProtoICMP, nil
	case "any":
		return ProtoAny, nil
	}
	return 0, fmt.Errorf("未知协议 %q（可选 tcp/udp/icmp/any）", s)
}

// Action 是规则动作。
type Action int

const (
	ActionDeny Action = iota
	ActionAllow
)

func (a Action) String() string {
	if a == ActionAllow {
		return "allow"
	}
	return "deny"
}

// Rule 是一条防火墙规则：目的前缀 × 协议集合 × 目的端口区间。
// 规则按切片顺序即优先级从高到低排列，先匹配先生效。
type Rule struct {
	Name    string
	Prefix  netip.Prefix
	Protos  Protocol
	PortMin uint16
	PortMax uint16
	Action  Action
}

// Family 返回 "ipv4" 或 "ipv6"。
func (r Rule) Family() string {
	if r.Prefix.Addr().Is4() {
		return "ipv4"
	}
	return "ipv6"
}

func (r Rule) String() string {
	ports := "any"
	if r.PortMin != 0 || r.PortMax != 65535 {
		ports = fmt.Sprintf("%d-%d", r.PortMin, r.PortMax)
	}
	return fmt.Sprintf("%s %s %s ports=%s", r.Action, r.Protos, r.Prefix, ports)
}

// Matches 判断包是否命中规则。IPv4 与 IPv6 域严格隔离，绝不跨族匹配。
func (r Rule) Matches(pkt Packet) bool {
	if r.Prefix.Addr().Is4() != pkt.Dst.Is4() {
		return false
	}
	if r.Protos&pkt.Proto == 0 {
		return false
	}
	if pkt.DstPort < r.PortMin || pkt.DstPort > r.PortMax {
		return false
	}
	return r.Prefix.Contains(pkt.Dst)
}

// Packet 是合成数据包，仅存在于离散事件模型内，从不发往真实网络。
type Packet struct {
	ID        int
	Src       netip.Addr
	Dst       netip.Addr
	Proto     Protocol // 单比特
	SrcPort   uint16
	DstPort   uint16
	SizeBytes int
	Arrival   int // 到达 tick
}

func (p Packet) String() string {
	return fmt.Sprintf("pkt#%d %s:%d -> %s:%d %s", p.ID, p.Src, p.SrcPort, p.Dst, p.DstPort, p.Proto)
}

// Match 返回首个命中规则的下标，未命中返回 -1（默认拒绝）。
func Match(rules []Rule, pkt Packet) int {
	for i := range rules {
		if rules[i].Matches(pkt) {
			return i
		}
	}
	return -1
}

// ValidateRules 校验规则集：前缀合法、端口区间合法、协议非空。
func ValidateRules(rules []Rule) error {
	for i, r := range rules {
		if !r.Prefix.IsValid() || r.Prefix.Bits() < 0 {
			return fmt.Errorf("规则 %d(%s)：前缀非法", i, r.Name)
		}
		if r.PortMin > r.PortMax {
			return fmt.Errorf("规则 %d(%s)：端口区间 %d-%d 非法", i, r.Name, r.PortMin, r.PortMax)
		}
		if r.Protos == 0 {
			return fmt.Errorf("规则 %d(%s)：协议集合为空", i, r.Name)
		}
	}
	return nil
}
