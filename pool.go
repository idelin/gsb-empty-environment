package netsec

import (
	"errors"
	"math/big"
	"net/netip"
)

var errPoolExhausted = errors.New("netsec: delegation pool exhausted")

// PrefixPool 是可委派前缀的地址池：
// 一个基前缀加上固定的委派前缀长度。
type PrefixPool struct {
	base    netip.Prefix
	deleg   int
	next    uint64
	maxIter uint64
}

// NewPrefixPool 构造地址池。base 会被掩码对齐，
// delegBits 是每个委派前缀的长度，必须不小于基前缀长度。
func NewPrefixPool(base netip.Prefix, delegBits int) (*PrefixPool, error) {
	if !base.Addr().Is6() || base.Addr().Is4In6() {
		return nil, errors.New("netsec: pool base must be an IPv6 prefix")
	}
	if delegBits < base.Bits() || delegBits > 128 {
		return nil, errors.New("netsec: invalid delegation prefix length")
	}
	p := &PrefixPool{base: base.Masked(), deleg: delegBits}
	p.maxIter = uint64(1) << min(uint(delegBits-base.Bits()), 62)
	return p, nil
}

// Contains 报告 p 是否是池内一个对齐的委派前缀。
func (p *PrefixPool) Contains(prefix netip.Prefix) bool {
	if !prefix.IsValid() || prefix.Bits() != p.deleg {
		return false
	}
	if prefix != prefix.Masked() {
		return false
	}
	return p.base.Contains(prefix.Addr())
}

// nth 返回池中第 n 个委派前缀。
func (p *PrefixPool) nth(n uint64) netip.Prefix {
	addr := p.base.Addr().As16()
	baseInt := new(big.Int).SetBytes(addr[:])
	offset := new(big.Int).Lsh(new(big.Int).SetUint64(n), uint(128-p.deleg))
	sum := new(big.Int).Add(baseInt, offset).Bytes()
	var out [16]byte
	copy(out[16-len(sum):], sum)
	return netip.PrefixFrom(netip.AddrFrom16(out), p.deleg)
}

// allocate 返回池中第一个未被占用谓词排除的委派前缀。
func (p *PrefixPool) allocate(occupied func(netip.Prefix) bool) (netip.Prefix, error) {
	for i := uint64(0); i < p.maxIter; i++ {
		cand := p.nth(p.next)
		p.next++
		if !occupied(cand) {
			return cand, nil
		}
	}
	return netip.Prefix{}, errPoolExhausted
}
