package netsec

import (
	"errors"
	"net/netip"
)

// MaxRelayDepth 是 relay-forw 封装链允许的最大深度。
const MaxRelayDepth = 32

var (
	ErrRelayTooDeep     = errors.New("netsec: relay-forw chain exceeds 32 hops")
	ErrRelayEmpty       = errors.New("netsec: empty relay-forw chain")
	ErrRelayLinkAddress = errors.New("netsec: invalid relay link-address")
	ErrRelayPeerAddress = errors.New("netsec: invalid relay peer-address")
)

// ValidateRelayChain 核对 relay-forw 封装链，hops 按外层到内层排列。
// 链深度不得超过 32；最内层（最靠近客户端）的 link-address
// 必须是有效的单播地址，外层允许为零值；peer-address 必须有效。
// 若配置了 linkNets，最内层 link-address 还必须落在其中之一。
func ValidateRelayChain(hops []RelayHop, linkNets []netip.Prefix) error {
	if len(hops) == 0 {
		return ErrRelayEmpty
	}
	if len(hops) > MaxRelayDepth {
		return ErrRelayTooDeep
	}
	for i, h := range hops {
		innermost := i == len(hops)-1
		if !h.LinkAddress.IsValid() {
			if innermost {
				return ErrRelayLinkAddress
			}
		} else if h.LinkAddress.IsUnspecified() || h.LinkAddress.IsMulticast() || h.LinkAddress.Is4In6() {
			return ErrRelayLinkAddress
		}
		if !h.PeerAddress.IsValid() || h.PeerAddress.IsUnspecified() || h.PeerAddress.IsMulticast() {
			return ErrRelayPeerAddress
		}
	}
	if len(linkNets) > 0 {
		inner := hops[len(hops)-1].LinkAddress
		ok := false
		for _, n := range linkNets {
			if n.Contains(inner) {
				ok = true
				break
			}
		}
		if !ok {
			return ErrRelayLinkAddress
		}
	}
	return nil
}
