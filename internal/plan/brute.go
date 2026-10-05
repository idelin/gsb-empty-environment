package plan

// Brute 是用于核验的朴素穷举器：不做任何剪枝与排序优化，
// 枚举所有满足约束的完整赋值，返回最优代价与可行解数量。
// 它与 Solve 使用相同的代价定义，但搜索逻辑独立实现，
// 用于小规模实例上交叉验证求解器的最优性。

type bruteState struct {
	in       solveInput
	assigned map[string]*PkgVersion
	required []string
	inReq    map[string]bool
	cons     map[string][]Constraint

	best    cost
	hasBest bool
	count   int
}

// BruteSolve 穷举所有可行解，返回最优代价、可行解数量与是否存在解。
func BruteSolve(reg *Registry, lock *Lock, extraRoots []Dep, pins map[string]string) (cost, int, bool, error) {
	in := solveInput{reg: reg, roots: append([]Dep(nil), lock.Roots...), pins: pins}
	if lock.Entries != nil {
		in.lock = lock.lockMap()
	} else {
		in.lock = map[string]LockEntry{}
	}
	in.roots = append(in.roots, extraRoots...)
	b := &bruteState{
		in:       in,
		assigned: make(map[string]*PkgVersion),
		inReq:    make(map[string]bool),
		cons:     make(map[string][]Constraint),
	}
	for _, d := range in.roots {
		c, err := ParseConstraint(d.Constraint)
		if err != nil {
			return cost{}, 0, false, err
		}
		b.cons[d.Name] = append(b.cons[d.Name], c)
		if !b.inReq[d.Name] {
			b.inReq[d.Name] = true
			b.required = append(b.required, d.Name)
		}
	}
	b.enum(0)
	return b.best, b.count, b.hasBest, nil
}

func (b *bruteState) enum(next int) {
	// 找到第一个未赋值的必需包。
	i := next
	for i < len(b.required) && b.assigned[b.required[i]] != nil {
		i++
	}
	if i == len(b.required) {
		// 完整赋值：统计代价。
		b.count++
		c := cost{}
		for _, name := range b.required {
			ch, by, _ := b.entryCost(name, b.assigned[name])
			c.changes += ch
			c.bytes += by
		}
		if !b.hasBest || c.less(b.best) {
			b.hasBest = true
			b.best = c
		}
		return
	}
	name := b.required[i]
	for _, pv := range b.in.reg.Versions(name) {
		if pin, ok := b.in.pins[name]; ok && pv.Version != pin {
			continue
		}
		if len(pv.availableSources()) == 0 {
			continue
		}
		okAll := true
		for _, c := range b.cons[name] {
			if !c.Match(pv.ver) {
				okAll = false
				break
			}
		}
		if !okAll {
			continue
		}
		// 赋值并传播。
		b.assigned[name] = pv
		type undo struct {
			target string
			isNew  bool
		}
		var undos []undo
		conflict := false
		for j, d := range pv.Deps {
			c := pv.depCons[j]
			if other := b.assigned[d.Name]; other != nil {
				if !c.Match(other.ver) {
					conflict = true
					break
				}
				continue
			}
			isNew := !b.inReq[d.Name]
			if isNew {
				b.inReq[d.Name] = true
				b.required = append(b.required, d.Name)
			}
			b.cons[d.Name] = append(b.cons[d.Name], c)
			undos = append(undos, undo{d.Name, isNew})
		}
		if !conflict {
			b.enum(i + 1)
		}
		for k := len(undos) - 1; k >= 0; k-- {
			u := undos[k]
			b.cons[u.target] = b.cons[u.target][:len(b.cons[u.target])-1]
			if u.isNew {
				delete(b.inReq, u.target)
				b.required = b.required[:len(b.required)-1]
			}
		}
		delete(b.assigned, name)
	}
}

// entryCost 与求解器共用一个代价定义（复制逻辑以保持实现独立可读）。
func (b *bruteState) entryCost(name string, pv *PkgVersion) (int, int64, Source) {
	le, ok := b.in.lock[name]
	sameVersion := false
	if ok {
		if lv, err := ParseVersion(le.Version); err == nil && lv == pv.ver {
			sameVersion = true
		}
	}
	changes := 0
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
