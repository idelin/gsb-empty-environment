package memberstate

import (
	"errors"
	"net/netip"
	"sort"
)

// PrefixSet 是以 CIDR 前缀为最小操作单位的不可变 IP 源地址集合。
//
// 集合的全集由 universe 限定（IPv4 或 IPv6 前缀），所有集合运算都只在
// 二进制基数 trie 上进行，绝不把前缀展开成单地址枚举。PrefixSet 一旦
// 构造完成即不可变：Add/Union/Complement 等运算返回新集合，因此可以被
// 并发快照安全共享，是成员状态“原子发布”的基础。
type PrefixSet struct {
	universe netip.Prefix
	bits     int
	root     *node
}

// node 是二进制基数 trie 的节点。term 为 true 表示该节点自身是一个被
// 插入的前缀，其整个子空间都属于集合。节点在发布后只读，结构共享安全。
type node struct {
	term     bool
	children [2]*node
}

// fullNode 是表示“整个子空间都属于集合”的共享只读哨兵，对应补集运算
// 中取全集的一侧，避免逐节点构造完整子树。
var fullNode = &node{term: true}

// NewPrefixSet 创建以 universe 为全集的前缀集合。universe 必须是规范化
// 的 IPv4 或 IPv6 前缀（如 10.0.0.0/8 或 fd00::/16）。
func NewPrefixSet(universe netip.Prefix) (*PrefixSet, error) {
	u, err := checkPrefix(universe)
	if err != nil {
		return nil, err
	}
	return &PrefixSet{
		universe: u,
		bits:     u.Addr().BitLen(),
		root:     nil,
	}, nil
}

// Universe 返回限定全集的规范化前缀。
func (s *PrefixSet) Universe() netip.Prefix { return s.universe }

// Empty 返回同一全集下的空集合。
func (s *PrefixSet) Empty() *PrefixSet {
	return &PrefixSet{universe: s.universe, bits: s.bits, root: nil}
}

// Add 返回包含 prefix 的新集合；原集合不被修改。prefix 必须与全集同族
// 且落在全集之内。
func (s *PrefixSet) Add(prefix netip.Prefix) (*PrefixSet, error) {
	p, err := checkPrefix(prefix)
	if err != nil {
		return nil, err
	}
	if p.Addr().BitLen() != s.bits {
		return nil, errors.New("memberstate: prefix address family does not match universe")
	}
	if !s.universe.Contains(p.Addr()) {
		return nil, errors.New("memberstate: prefix falls outside the configured universe")
	}
	root := addPrefix(s.root, p.Addr().AsSlice(), s.universe.Bits(), p.Bits())
	return &PrefixSet{universe: s.universe, bits: s.bits, root: root}, nil
}

func checkPrefix(p netip.Prefix) (netip.Prefix, error) {
	if !p.IsValid() {
		return netip.Prefix{}, errors.New("memberstate: invalid prefix")
	}
	c := p.Masked()
	if c.Addr().Is4In6() {
		return netip.Prefix{}, errors.New("memberstate: 4-in-6 prefixes are not supported")
	}
	return c, nil
}

func addPrefix(root *node, addr []byte, depth, maxDepth int) *node {
	if root == fullNode {
		return fullNode
	}
	if root == nil {
		root = &node{}
	}
	if depth == maxDepth {
		return fullNode
	}
	bit := int((addr[depth/8] >> (7 - depth%8)) & 1)
	child := addPrefix(root.children[bit], addr, depth+1, maxDepth)
	clone := &node{term: root.term, children: root.children}
	clone.children[bit] = child
	return compact(clone)
}

// compact 在两个子节点都为满时把节点折叠成全空间哨兵，保证 trie 始终是
// 最小表示，输出前缀因此天然不相交且不可再合并。
func compact(n *node) *node {
	if n.term {
		return fullNode
	}
	if n.children[0] == fullNode && n.children[1] == fullNode {
		return fullNode
	}
	return n
}

// Union 返回 s 与 other 的并集。两者必须共享同一全集。
func (s *PrefixSet) Union(other *PrefixSet) *PrefixSet {
	s.mustSameUniverse(other)
	return &PrefixSet{
		universe: s.universe,
		bits:     s.bits,
		root:     unionNodes(s.root, other.root),
	}
}

func unionNodes(a, b *node) *node {
	if a == fullNode || b == fullNode {
		return fullNode
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	n := &node{
		term: a.term || b.term,
	}
	for i := 0; i < 2; i++ {
		n.children[i] = unionNodes(a.children[i], b.children[i])
	}
	return compact(n)
}

// Intersect 返回 s 与 other 的交集。
func (s *PrefixSet) Intersect(other *PrefixSet) *PrefixSet {
	s.mustSameUniverse(other)
	return &PrefixSet{
		universe: s.universe,
		bits:     s.bits,
		root:     intersectNodes(s.root, other.root),
	}
}

func intersectNodes(a, b *node) *node {
	if a == nil || b == nil {
		return nil
	}
	if a == fullNode {
		return b
	}
	if b == fullNode {
		return a
	}
	n := &node{}
	for i := 0; i < 2; i++ {
		n.children[i] = intersectNodes(a.children[i], b.children[i])
	}
	return compact(n)
}

// Complement 返回全集内的补集，即“未被 s 覆盖”的前缀空间。
func (s *PrefixSet) Complement() *PrefixSet {
	return &PrefixSet{
		universe: s.universe,
		bits:     s.bits,
		root:     complementNode(s.root),
	}
}

func complementNode(n *node) *node {
	if n == fullNode {
		return nil
	}
	if n == nil {
		return fullNode
	}
	if n.term {
		return fullNode
	}
	out := &node{}
	for i := 0; i < 2; i++ {
		out.children[i] = complementNode(n.children[i])
	}
	return compact(out)
}

// Difference 返回 s 减 other 的差集。
func (s *PrefixSet) Difference(other *PrefixSet) *PrefixSet {
	s.mustSameUniverse(other)
	return &PrefixSet{
		universe: s.universe,
		bits:     s.bits,
		root:     diffNodes(s.root, other.root),
	}
}

func diffNodes(a, b *node) *node {
	if a == nil || b == fullNode {
		return nil
	}
	if b == nil {
		return a
	}
	if a == fullNode {
		return complementNode(b)
	}
	n := &node{}
	for i := 0; i < 2; i++ {
		n.children[i] = diffNodes(a.children[i], b.children[i])
	}
	return compact(n)
}

// Prefixes 以规范化、最小、互不相交的 CIDR 前缀枚举集合覆盖。枚举仅在
// 查询输出时发生，且输出的是前缀而非单地址。
func (s *PrefixSet) Prefixes() []netip.Prefix {
	var out []netip.Prefix
	walkTrie(s.root, s.universe.Addr().AsSlice(), s.universe.Bits(), &out)
	sort.Slice(out, func(i, j int) bool { return prefixLess(out[i], out[j]) })
	return out
}

func walkTrie(n *node, base []byte, depth int, out *[]netip.Prefix) {
	if n == nil {
		return
	}
	if n == fullNode || n.term {
		addr, _ := netip.AddrFromSlice(base)
		*out = append(*out, netip.PrefixFrom(addr.Unmap(), depth))
		return
	}
	if depth == len(base)*8 {
		return
	}
	zero := append([]byte(nil), base...)
	one := append([]byte(nil), base...)
	setBitAt(one, depth)
	walkTrie(n.children[0], zero, depth+1, out)
	walkTrie(n.children[1], one, depth+1, out)
}

func setBitAt(addr []byte, depth int) {
	addr[depth/8] |= 1 << (7 - depth%8)
}

// Contains 判断单个地址是否落在集合内（仅用于查询判定，不做枚举）。
func (s *PrefixSet) Contains(addr netip.Addr) bool {
	if !s.universe.Contains(addr) {
		return false
	}
	n := s.root
	bytes := addr.AsSlice()
	for depth := s.universe.Bits(); depth < s.bits; depth++ {
		if n == nil {
			return false
		}
		if n == fullNode || n.term {
			return true
		}
		bit := int((bytes[depth/8] >> (7 - depth%8)) & 1)
		n = n.children[bit]
	}
	return n == fullNode || (n != nil && n.term)
}

// Covers 判断 prefix 是否被集合完全覆盖。
func (s *PrefixSet) Covers(prefix netip.Prefix) bool {
	p, err := checkPrefix(prefix)
	if err != nil || p.Addr().BitLen() != s.bits || !s.universe.Contains(p.Addr()) {
		return false
	}
	n := s.root
	bytes := p.Addr().AsSlice()
	for depth := s.universe.Bits(); depth < p.Bits(); depth++ {
		if n == nil {
			return false
		}
		if n == fullNode || n.term {
			return true
		}
		bit := int((bytes[depth/8] >> (7 - depth%8)) & 1)
		n = n.children[bit]
	}
	return n == fullNode || (n != nil && n.term)
}

// IntersectingPrefixes 返回集合中与 target 相交的原始前缀，用于生成
// “为什么该源被放行”的授权证据。
func (s *PrefixSet) IntersectingPrefixes(target netip.Prefix) []netip.Prefix {
	t, err := checkPrefix(target)
	if err != nil || t.Addr().BitLen() != s.bits {
		return nil
	}
	var out []netip.Prefix
	collectIntersect(s.root, s.universe.Addr().AsSlice(), s.universe.Bits(), t, &out)
	sort.Slice(out, func(i, j int) bool { return prefixLess(out[i], out[j]) })
	return out
}

func collectIntersect(n *node, base []byte, depth int, target netip.Prefix, out *[]netip.Prefix) {
	if n == nil {
		return
	}
	baseAddr, _ := netip.AddrFromSlice(base)
	current := netip.PrefixFrom(baseAddr.Unmap(), depth)
	if n == fullNode || n.term {
		if prefixesOverlap(current, target) {
			*out = append(*out, current)
		}
		return
	}
	if depth == len(base)*8 {
		return
	}
	zero := append([]byte(nil), base...)
	one := append([]byte(nil), base...)
	setBitAt(one, depth)
	collectIntersect(n.children[0], zero, depth+1, target, out)
	collectIntersect(n.children[1], one, depth+1, target, out)
}

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Overlaps(b)
}

func prefixLess(a, b netip.Prefix) bool {
	ab, bb := a.Addr().AsSlice(), b.Addr().AsSlice()
	for i := 0; i < len(ab) && i < len(bb); i++ {
		if ab[i] != bb[i] {
			return ab[i] < bb[i]
		}
	}
	return a.Bits() < b.Bits()
}

func (s *PrefixSet) mustSameUniverse(other *PrefixSet) {
	if s.universe != other.universe {
		panic("memberstate: set operations require identical universe prefixes")
	}
}
