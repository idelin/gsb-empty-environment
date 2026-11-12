package taskpkg

// Verdict 是操作相对撤权的因果判定。
type Verdict int

const (
	// Incomplete 表示引用闭包缺失，无法判定。
	Incomplete Verdict = iota
	// Before 表示操作必然在撤权之前（操作 ∈ 撤权的引用闭包）。
	Before
	// After 表示操作必然在撤权之后（撤权 ∈ 操作的引用闭包）。
	After
	// Concurrent 表示两者无序：闭包完备且互不可达。
	Concurrent
)

func (v Verdict) String() string {
	switch v {
	case Before:
		return "before"
	case After:
		return "after"
	case Concurrent:
		return "concurrent"
	default:
		return "incomplete"
	}
}

type closure struct {
	set     map[Hash]bool
	missing []Hash
}

// closureOf 计算 h 的完整引用闭包（含 h 自身），沿 Prev 与 Refs 遍历。
// 缺失的依赖记入 missing；发现环返回 ErrCycle。
func (s *Store) closureOf(h Hash) (closure, error) {
	c := closure{set: make(map[Hash]bool)}
	onPath := make(map[Hash]bool)
	var visit func(Hash) error
	visit = func(cur Hash) error {
		if c.set[cur] {
			return nil
		}
		if onPath[cur] {
			return ErrCycle
		}
		e, ok := s.events[cur]
		if !ok {
			c.missing = append(c.missing, cur)
			return nil
		}
		onPath[cur] = true
		for _, d := range e.deps() {
			if err := visit(d); err != nil {
				return err
			}
		}
		delete(onPath, cur)
		c.set[cur] = true
		return nil
	}
	if err := visit(h); err != nil {
		return closure{}, err
	}
	return c, nil
}

// Compare 判定 op 相对 revoke 的因果先后。仅依据可验证的
// 引用闭包，墙钟不参与。已证明的必然先后不受无关缺失引用影响；
// 无法证明且闭包存在缺失时返回 Incomplete。
func (s *Store) Compare(op, revoke Hash) (Verdict, error) {
	if _, ok := s.events[op]; !ok {
		return Incomplete, nil
	}
	if _, ok := s.events[revoke]; !ok {
		return Incomplete, nil
	}
	cRev, err := s.closureOf(revoke)
	if err != nil {
		return Incomplete, err
	}
	if cRev.set[op] {
		return Before, nil
	}
	cOp, err := s.closureOf(op)
	if err != nil {
		return Incomplete, err
	}
	if cOp.set[revoke] {
		return After, nil
	}
	if len(cRev.missing) > 0 || len(cOp.missing) > 0 {
		return Incomplete, nil
	}
	return Concurrent, nil
}
