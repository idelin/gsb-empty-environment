package plan

import (
	"errors"
	"fmt"
)

// cost 是字典序比较的目标：(变更包数, 下载字节)。
// 第一优先级：尽量保留锁文件中的原版本；第二优先级：少下载。
type cost struct {
	changes int
	bytes   int64
}

func (c cost) less(o cost) bool {
	if c.changes != o.changes {
		return c.changes < o.changes
	}
	return c.bytes < o.bytes
}

// Changes 返回需要变更（含新增）的包数。
func (c cost) Changes() int { return c.changes }

// Bytes 返回需要下载的总字节数。
func (c cost) Bytes() int64 { return c.bytes }

func (c cost) String() string {
	return fmt.Sprintf("(变更 %d 包, 下载 %d 字节)", c.changes, c.bytes)
}

// Resolved 是某包在解中的解析结果。
type Resolved struct {
	Pkg    *PkgVersion
	Source Source
	Bytes  int64 // 需要下载的字节（未变化时为 0）
}

// Solution 是一次求解的结果。Entries 恰好包含从根可达的所有包，
// 锁文件中不再可达的条目已被剔除（垃圾回收）。
type Solution struct {
	Entries map[string]Resolved
	Order   []string // 包被发现（成为必需）的顺序，保证输出确定
	Roots   []Dep    // 生效的根需求
}

// ErrNoSolution 表示约束不可满足。
var ErrNoSolution = errors.New("无可行解")

// consEdge 是一条已生效的约束边：来自根需求或某个已选定版本的父包。
type consEdge struct {
	fromName string // "<root>" 或父包名
	fromVer  string // 父包版本（根时为空）
	dep      Dep
	cons     Constraint
}

const rootName = "<root>"

// solveInput 汇总求解参数。
type solveInput struct {
	reg      *Registry
	lock     map[string]LockEntry
	roots    []Dep
	rootCons []Constraint
	pins     map[string]string // 额外固定：name -> version（用于原因探测）
}

type solver struct {
	in solveInput

	assigned map[string]*PkgVersion
	cons     map[string][]consEdge
	required map[string]bool
	order    []string

	curChanges int
	curBytes   int64

	best      map[string]*PkgVersion
	bestOrder []string
	bestCost  cost
	hasBest   bool
}

// Solve 精确求解：在保留原版本数最多、其次下载字节最少的意义下找到最优解。
// extraRoots 为新增的根依赖；pins 将指定包固定到指定版本（内部分析用）。
func Solve(reg *Registry, lock *Lock, extraRoots []Dep, pins map[string]string) (*Solution, cost, error) {
	in := solveInput{reg: reg, roots: append([]Dep(nil), lock.Roots...), pins: pins}
	if lock.Entries != nil {
		in.lock = lock.lockMap()
	} else {
		in.lock = map[string]LockEntry{}
	}
	in.roots = append(in.roots, extraRoots...)
	for _, d := range in.roots {
		c, err := ParseConstraint(d.Constraint)
		if err != nil {
			return nil, cost{}, fmt.Errorf("根依赖 %s: %v", d.Name, err)
		}
		in.rootCons = append(in.rootCons, c)
	}
	s := &solver{
		in:       in,
		assigned: make(map[string]*PkgVersion),
		cons:     make(map[string][]consEdge),
		required: make(map[string]bool),
	}
	for i, d := range in.roots {
		s.addRequirement(d.Name, consEdge{fromName: rootName, dep: d, cons: in.rootCons[i]})
	}
	s.search()
	if !s.hasBest {
		return nil, cost{}, ErrNoSolution
	}
	sol := &Solution{
		Entries: make(map[string]Resolved, len(s.best)),
		Order:   s.bestOrder,
		Roots:   in.roots,
	}
	for _, name := range s.bestOrder {
		pv := s.best[name]
		_, bytes, src := s.entryCost(name, pv)
		sol.Entries[name] = Resolved{Pkg: pv, Source: src, Bytes: bytes}
	}
	return sol, s.bestCost, nil
}

func (s *solver) addRequirement(name string, e consEdge) {
	s.cons[name] = append(s.cons[name], e)
	if !s.required[name] {
		s.required[name] = true
		s.order = append(s.order, name)
	}
}

// entryCost 计算选择 (name, pv) 的代价：变更计数与下载字节、应选来源。
// 来源选择在版本确定后是独立的：版本与锁一致且锁定来源仍可用 -> 0 字节；
// 否则选可用来源中字节最小者。
func (s *solver) entryCost(name string, pv *PkgVersion) (changes int, bytes int64, src Source) {
	le, ok := s.in.lock[name]
	sameVersion := false
	if ok {
		if lv, err := ParseVersion(le.Version); err == nil && lv == pv.ver {
			sameVersion = true
		}
	}
	if !sameVersion {
		changes = 1
	}
	avail := pv.availableSources()
	if sameVersion {
		for _, a := range avail {
			if a.ID == le.Source {
				return changes, 0, a
			}
		}
	}
	best := avail[0]
	for _, a := range avail[1:] {
		if a.Size < best.Size {
			best = a
		}
	}
	return changes, best.Size, best
}

// feasible 判断版本是否满足当前全部约束且有可用来源。
func (s *solver) feasible(name string, pv *PkgVersion) bool {
	if len(pv.availableSources()) == 0 {
		return false
	}
	for _, e := range s.cons[name] {
		if !e.cons.Match(pv.ver) {
			return false
		}
	}
	return true
}

// candidates 返回某包当前可行的版本，锁定版本优先（偏向保留原版本）。
func (s *solver) candidates(name string) []*PkgVersion {
	var out []*PkgVersion
	if pin, ok := s.in.pins[name]; ok {
		if pv := s.in.reg.Lookup(name, pin); pv != nil && s.feasible(name, pv) {
			out = append(out, pv)
		}
		return out
	}
	var locked *PkgVersion
	if le, ok := s.in.lock[name]; ok {
		locked = s.in.reg.Lookup(name, le.Version)
	}
	if locked != nil && s.feasible(name, locked) {
		out = append(out, locked)
	}
	for _, pv := range s.in.reg.Versions(name) {
		if pv == locked {
			continue
		}
		if s.feasible(name, pv) {
			out = append(out, pv)
		}
	}
	return out
}

// lowerBound 返回当前状态下 (变更数, 字节) 的乐观下界。
func (s *solver) lowerBound() (int, int64) {
	extra := 0
	for _, name := range s.order {
		if s.assigned[name] != nil {
			continue
		}
		le, ok := s.in.lock[name]
		if !ok {
			extra++ // 新包必然计入变更
			continue
		}
		lv := s.in.reg.Lookup(name, le.Version)
		if lv == nil || !s.feasible(name, lv) {
			extra++ // 原版本已不可用或不满足现有约束，必然变更
		}
	}
	return s.curChanges + extra, s.curBytes
}

type propagation struct {
	target      string
	newlyNeeded bool
}

func (s *solver) search() {
	if s.hasBest {
		lbc, lbb := s.lowerBound()
		if lbc > s.bestCost.changes || (lbc == s.bestCost.changes && lbb >= s.bestCost.bytes) {
			return
		}
	}
	// 选择候选最少的未赋值必需包（MRV），按发现顺序保证确定性。
	pick := ""
	var pickCands []*PkgVersion
	for _, name := range s.order {
		if s.assigned[name] != nil {
			continue
		}
		cands := s.candidates(name)
		if len(cands) == 0 {
			return // 死路
		}
		if pickCands == nil || len(cands) < len(pickCands) {
			pick, pickCands = name, cands
		}
	}
	if pickCands == nil {
		// 全部必需包均已赋值：找到一个完整解。
		c := cost{s.curChanges, s.curBytes}
		if !s.hasBest || c.less(s.bestCost) {
			s.hasBest = true
			s.bestCost = c
			s.best = make(map[string]*PkgVersion, len(s.assigned))
			for k, v := range s.assigned {
				s.best[k] = v
			}
			s.bestOrder = append([]string(nil), s.order...)
		}
		return
	}
	for _, pv := range pickCands {
		props, ok := s.assign(pick, pv)
		if ok {
			s.search()
		}
		s.unassign(pick, pv, props)
	}
}

// assign 选定版本并传播其依赖约束；失败时返回的 props 供撤销。
func (s *solver) assign(name string, pv *PkgVersion) ([]propagation, bool) {
	s.assigned[name] = pv
	ch, by, _ := s.entryCost(name, pv)
	s.curChanges += ch
	s.curBytes += by
	var props []propagation
	for j, d := range pv.Deps {
		c := pv.depCons[j]
		if other := s.assigned[d.Name]; other != nil {
			if !c.Match(other.ver) {
				return props, false
			}
			continue
		}
		_, needed := s.required[d.Name]
		s.addRequirement(d.Name, consEdge{fromName: name, fromVer: pv.Version, dep: d, cons: c})
		props = append(props, propagation{target: d.Name, newlyNeeded: !needed})
	}
	return props, true
}

func (s *solver) unassign(name string, pv *PkgVersion, props []propagation) {
	for i := len(props) - 1; i >= 0; i-- {
		p := props[i]
		s.cons[p.target] = s.cons[p.target][:len(s.cons[p.target])-1]
		if p.newlyNeeded {
			delete(s.required, p.target)
			s.order = s.order[:len(s.order)-1]
		}
	}
	ch, by, _ := s.entryCost(name, pv)
	s.curChanges -= ch
	s.curBytes -= by
	delete(s.assigned, name)
}
