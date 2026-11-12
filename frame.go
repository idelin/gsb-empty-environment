package netsec

// frame 是某程序点的抽象寄存器环境。
// 缺失的寄存器视为 Bottom（未定义/不可达）。
type frame struct {
	regs map[RegisterID]Affine
}

func newFrame() *frame {
	return &frame{regs: make(map[RegisterID]Affine)}
}

func (f *frame) get(r RegisterID) Affine {
	if v, ok := f.regs[r]; ok {
		return v
	}
	return Bottom()
}

func (f *frame) set(r RegisterID, v Affine) {
	f.regs[r] = v
}

// clone 深拷贝框架。
func (f *frame) clone() *frame {
	out := newFrame()
	for r, v := range f.regs {
		out.regs[r] = v.Clone()
	}
	return out
}

// isBottom 报告框架是否为不可达。
//
// 注意：空寄存器映射代表「到达块入口但尚无寄存器被定义」的可达初始状态
// （例如程序入口），不属于不可达；只有至少含一个定义且全部为 Bottom 时才
// 判定为不可达。
func (f *frame) isBottom() bool {
	if len(f.regs) == 0 {
		return false
	}
	for _, v := range f.regs {
		if !v.IsBottom() {
			return false
		}
	}
	return true
}

// joinInto 把 other 合流入 f（原地修改），合流保留全部地址来源。
func (f *frame) joinInto(other *frame) {
	if other == nil || other.isBottom() {
		return
	}
	if f.isBottom() {
		merged := other.clone()
		f.regs = merged.regs
		return
	}
	for r, ov := range other.regs {
		if cur, ok := f.regs[r]; ok {
			f.regs[r] = cur.Join(ov)
		} else {
			f.regs[r] = ov.Clone()
		}
	}
}

// frameLeq 判断 x ⊑ y，用于不变量保持性检查。
//
// 不变量只需约束它显式声明的寄存器：x 中存在但 y 未声明的寄存器不在
// 归纳假设的责任范围内，直接忽略；已声明的寄存器必须满足逐值包含。
func frameLeq(x, y *frame) bool {
	if x == nil || x.isBottom() {
		return true
	}
	if y == nil {
		return false
	}
	for r, xv := range x.regs {
		if xv.IsBottom() {
			continue
		}
		yv, ok := y.regs[r]
		if !ok {
			continue
		}
		if !xv.Leq(yv) {
			return false
		}
	}
	return true
}

// frameFromInvariant 把宿主给定的不变量拷贝成框架。
func frameFromInvariant(inv map[RegisterID]Affine) *frame {
	f := newFrame()
	for r, v := range inv {
		f.regs[r] = v.Clone()
	}
	return f
}
