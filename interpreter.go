package netsec

import "fmt"

// cval 是解释器里带「来源标记」的具体值，与抽象域 Affine 对应。
//
// 地址型值记录来源区域与相对该区域基址的偏移；标量型值只携带整数。
// 这样具体寻址不必靠整数猜测某个值是否为区域基址，避免多区域基址混淆。
type cval struct {
	val      int64
	address  bool
	region   RegionID
	offset   int64
	external bool
	checked  bool
}

// LoadEvent 是具体执行中一次数据读取的观测记录。
type LoadEvent struct {
	Site     string
	Block    BlockID
	Op       int
	Region   RegionID
	Address  int64
	Size     int64
	InBounds bool
}

// RunResult 是一次具体执行的结果。
type RunResult struct {
	Loads []LoadEvent
	Steps int
	OK    bool
	Err   error
}

const defaultMaxSteps = 10_000_000

// Run 用给定的具体输入执行程序，记录每次读取及其是否落在声明区域内。
func Run(prog *Program, inputs map[InputID]int64) (*RunResult, error) {
	return RunWithLimit(prog, inputs, defaultMaxSteps)
}

// RunWithLimit 在显式步数上限下执行，防止具体执行不终止。
func RunWithLimit(prog *Program, inputs map[InputID]int64, maxSteps int) (*RunResult, error) {
	if err := ValidateProgram(prog); err != nil {
		return nil, err
	}
	m := &machine{
		prog:   prog,
		regs:   make(map[RegisterID]cval),
		inputs: inputs,
	}
	res := &RunResult{OK: true}

	for {
		if res.Steps >= maxSteps {
			res.OK = false
			res.Err = fmt.Errorf("interp: 执行超过 %d 步仍未终止", maxSteps)
			return res, res.Err
		}
		b := m.pc
		blk := &prog.Blocks[b]
		for i := range blk.Ops {
			if err := m.execOp(&blk.Ops[i], b, i, res); err != nil {
				res.OK = false
				res.Err = err
				return res, err
			}
		}
		next, ret := m.execTerm(&blk.Term)
		res.Steps++
		if ret {
			return res, nil
		}
		m.pc = next
	}
}

type machine struct {
	prog   *Program
	pc     BlockID
	regs   map[RegisterID]cval
	inputs map[InputID]int64
}

func (m *machine) execOp(op *Op, b BlockID, idx int, res *RunResult) error {
	switch op.Kind {
	case OpInput:
		v, ok := m.inputs[op.Input]
		if !ok {
			return fmt.Errorf("interp: 缺少输入 %d 的具体值", op.Input)
		}
		m.regs[op.Dst] = cval{val: v, external: true}
	case OpBase:
		region := m.prog.Regions[op.Region]
		m.regs[op.Dst] = cval{
			val: region.Base, address: true, region: op.Region, offset: 0,
		}
	case OpAddConst:
		src := m.regs[op.Src]
		out := src
		out.val = src.val + op.Const
		if src.address {
			out.offset = src.offset + op.Const
		}
		m.regs[op.Dst] = out
	case OpCopy:
		m.regs[op.Dst] = m.regs[op.Src]
	case OpLoad:
		return m.execLoad(op, b, idx, res)
	default:
		return fmt.Errorf("interp: 未知操作类型 %d", op.Kind)
	}
	return nil
}

func (m *machine) execLoad(op *Op, b BlockID, idx int, res *RunResult) error {
	base := m.regs[op.Base]
	index := m.regs[op.Src]

	type concreteSpan struct {
		region RegionID
		offset int64
	}
	var spans []concreteSpan

	if op.Region != IndirectRegion {
		// 显式区域读取：基址必须是该区域的地址。
		if !base.address || base.region != op.Region {
			return fmt.Errorf("interp: 站点 %q 基址不是声明区域 %d 的地址", op.Site, op.Region)
		}
		off := base.offset
		if index.address {
			// 零偏移地址索引：其相对来源偏移必须为 0。
			if index.offset != 0 {
				return fmt.Errorf("interp: 站点 %q 地址索引必须保持零偏移", op.Site)
			}
		} else {
			off += index.val
		}
		spans = append(spans, concreteSpan{region: op.Region, offset: off})
	} else {
		// 间接读取：基址携带合流后的单一具体来源。
		if !base.address {
			return fmt.Errorf("interp: 站点 %q 间接读取基址不携带区域来源", op.Site)
		}
		off := base.offset
		if index.address {
			if index.offset != 0 {
				return fmt.Errorf("interp: 站点 %q 地址索引必须保持零偏移", op.Site)
			}
		} else {
			off += index.val
		}
		spans = append(spans, concreteSpan{region: base.region, offset: off})
	}

	for _, sp := range spans {
		region := m.prog.Regions[sp.region]
		address, overflow := addChecked(region.Base, sp.offset)
		if overflow {
			return fmt.Errorf("interp: 站点 %q 地址溢出", op.Site)
		}
		inBounds := region.ContainsOffset(sp.offset, op.Size)
		res.Loads = append(res.Loads, LoadEvent{
			Site: op.Site, Block: b, Op: idx, Region: sp.region,
			Address: address, Size: op.Size, InBounds: inBounds,
		})
		if !inBounds {
			// 读取结果视为不可信外部值。
			m.regs[op.Dst] = cval{val: 0, external: true}
			return fmt.Errorf("interp: 站点 %q 具体读取越界（偏移 %d，大小 %d）",
				op.Site, sp.offset, op.Size)
		}
	}
	// 读取得到的数据不可信，防止把数据当指针。
	m.regs[op.Dst] = cval{val: 0, external: true}
	return nil
}

func (m *machine) execTerm(term *Terminator) (next BlockID, ret bool) {
	switch term.Kind {
	case TermReturn:
		return 0, true
	case TermJump:
		return term.Then, false
	case TermBranch:
		v := m.regs[term.Src].val
		if compare(v, term.Cmp, term.Const) {
			return term.Then, false
		}
		return term.Else, false
	default:
		return 0, true
	}
}

func compare(v int64, cmp uint8, c int64) bool {
	switch cmp {
	case CmpEQ:
		return v == c
	case CmpNE:
		return v != c
	case CmpLE:
		return v <= c
	case CmpLT:
		return v < c
	case CmpGE:
		return v >= c
	case CmpGT:
		return v > c
	default:
		return false
	}
}
