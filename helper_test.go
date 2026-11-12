package netsec

// 测试用寄存器编号。
const (
	t0 RegisterID = iota
	t1
	t2
	t3
	t4
	t5
)

func retTerm() Terminator { return Terminator{Kind: TermReturn} }

func jumpTo(to BlockID) Terminator { return Terminator{Kind: TermJump, Then: to} }

func ld(dst, base, idx RegisterID, region RegionID, size int64, site string) Op {
	return Op{Kind: OpLoad, Dst: dst, Base: base, Src: idx, Region: region, Size: size, Site: site}
}

func branch(src RegisterID, cmp uint8, c int64, then, els BlockID) Terminator {
	return Terminator{Kind: TermBranch, Src: src, Cmp: cmp, Const: c, Then: then, Else: els}
}
