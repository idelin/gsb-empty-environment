package biscuit

import "fmt"

// MaxFixpointRounds 是规则求值允许的最大迭代轮数。
// 第 100 轮结束后仍有新事实产生即判定为不动点超限并拒绝请求。
const MaxFixpointRounds = 100

// FixpointError 表示规则在 MaxFixpointRounds 轮内未收敛到不动点。
type FixpointError struct {
	Rounds int
	Scope  string
}

func (e *FixpointError) Error() string {
	return fmt.Sprintf("fixpoint not reached after %d rounds in %s scope", e.Rounds, e.Scope)
}

// compiledRule 把规则里的变量替换成槽位号，求值时用切片而非 map 承载绑定。
type compiledRule struct {
	head      Fact
	headSlots []int // 每个 head 参数对应的变量槽位；-1 表示常量
	body      []compiledAtom
	slotCount int
}

type compiledAtom struct {
	predicate string
	args      []compiledArg
}

type compiledArg struct {
	slot int  // >= 0 表示变量槽位；为常量参数时为 -1，取值见 term
	term Term // 常量参数
}

func compileRule(rule Rule) compiledRule {
	slots := map[string]int{}
	slot := func(name string) int {
		if index, ok := slots[name]; ok {
			return index
		}
		index := len(slots)
		slots[name] = index
		return index
	}
	compiled := compiledRule{head: rule.Head}
	compiled.headSlots = make([]int, len(rule.Head.Args))
	for i, arg := range rule.Head.Args {
		if arg.IsVariable() {
			compiled.headSlots[i] = slot(arg.Name)
		} else {
			compiled.headSlots[i] = -1
		}
	}
	compiled.body = make([]compiledAtom, len(rule.Body))
	for i, atom := range rule.Body {
		compiled.body[i] = compiledAtom{predicate: atom.Predicate}
		compiled.body[i].args = make([]compiledArg, len(atom.Args))
		for j, arg := range atom.Args {
			if arg.IsVariable() {
				compiled.body[i].args[j] = compiledArg{slot: slot(arg.Name), term: arg}
			} else {
				compiled.body[i].args[j] = compiledArg{slot: -1, term: arg}
			}
		}
	}
	compiled.slotCount = len(slots)
	return compiled
}

// factSet 保存基实例事实，并按谓词建立参数级索引以加速连接。
type factSet struct {
	facts  map[string]bool
	byPred map[string][]Fact
	index  map[predicateArgKey][]Fact
}

type predicateArgKey struct {
	predicate string
	index     int
	termKey   string
}

func newFactSet() *factSet {
	return &factSet{
		facts:  map[string]bool{},
		byPred: map[string][]Fact{},
		index:  map[predicateArgKey][]Fact{},
	}
}

func (s *factSet) add(fact Fact) bool {
	key := factKey(fact)
	if s.facts[key] {
		return false
	}
	s.facts[key] = true
	s.byPred[fact.Predicate] = append(s.byPred[fact.Predicate], fact)
	for i, arg := range fact.Args {
		indexKey := predicateArgKey{predicate: fact.Predicate, index: i, termKey: termKey(arg)}
		s.index[indexKey] = append(s.index[indexKey], fact)
	}
	return true
}

func (s *factSet) has(fact Fact) bool {
	return s.facts[factKey(fact)]
}

// snapshot 复制当前按谓词分组的事实切片，避免后续追加污染调用方持有的旧切片。
func (s *factSet) snapshot() map[string][]Fact {
	out := make(map[string][]Fact, len(s.byPred))
	for predicate, facts := range s.byPred {
		copied := make([]Fact, len(facts))
		copy(copied, facts)
		out[predicate] = copied
	}
	return out
}

// candidates 返回某个已编译规则体原子可尝试匹配的候选事实：
// 优先使用常量参数或已绑定变量的索引，否则回退到该谓词的全部事实。
func (s *factSet) candidates(atom compiledAtom, bindings []Term) []Fact {
	for i, arg := range atom.args {
		if arg.slot >= 0 {
			if arg.slot < len(bindings) && isBound(bindings[arg.slot]) {
				indexKey := predicateArgKey{predicate: atom.predicate, index: i, termKey: termKey(bindings[arg.slot])}
				if indexed, ok := s.index[indexKey]; ok {
					return indexed
				}
				return nil
			}
			continue
		}
		indexKey := predicateArgKey{predicate: atom.predicate, index: i, termKey: termKey(arg.term)}
		if indexed, ok := s.index[indexKey]; ok {
			return indexed
		}
		return nil
	}
	return s.byPred[atom.predicate]
}

// world 是一次求值的事实视图。authority 对所有块只读可见；
// local 是当前块的私有空间，衰减块推出的事实只落在这里。
type world struct {
	authority *factSet
	local     *factSet
}

func (w world) candidates(atom compiledAtom, bindings []Term) []Fact {
	if w.local == nil {
		return w.authority.candidates(atom, bindings)
	}
	seen := map[string]bool{}
	var out []Fact
	for _, fact := range w.local.candidates(atom, bindings) {
		key := factKey(fact)
		if !seen[key] {
			seen[key] = true
			out = append(out, fact)
		}
	}
	for _, fact := range w.authority.candidates(atom, bindings) {
		key := factKey(fact)
		if !seen[key] {
			seen[key] = true
			out = append(out, fact)
		}
	}
	return out
}

// snapshot 返回合并视图中按谓词分组的全部事实（首轮引导用）。
func (w world) snapshot() map[string][]Fact {
	if w.local == nil {
		return w.authority.snapshot()
	}
	out := w.authority.snapshot()
	for predicate, facts := range w.local.snapshot() {
		out[predicate] = append(out[predicate], facts...)
	}
	return out
}

// solve 对给定规则做前向链求值直到不动点。
// seed 写入目标作用域（authority 块或单个衰减块的私有作用域），
// 规则在 authority + 目标作用域的合并视图上连接，新事实只写入目标作用域。
// 求值采用半朴素（semi-naive）策略：每条规则的每个 body 原子轮流作为
// “增量原子”，要求它匹配上一轮新增的事实，其余原子在完整事实视图上连接；
// 这样多原子规则跨轮产生的组合不会丢失，又避免每轮对全部事实做完整连接。
// 超过 MaxFixpointRounds 轮仍未收敛时返回 FixpointError。
func solve(seed []Fact, rules []Rule, target *factSet, view world, scope string) error {
	delta := newFactSet()
	for _, fact := range seed {
		if target.add(fact) {
			delta.add(fact)
		}
	}
	compiled := make([]compiledRule, len(rules))
	for i, rule := range rules {
		compiled[i] = compileRule(rule)
	}
	for round := 1; round <= MaxFixpointRounds; round++ {
		nextDelta := newFactSet()
		var changed map[string][]Fact
		if round == 1 {
			// 首轮用整个视图引导：衰减块规则的 body 原子可能全部位于
			// authority 作用域，本块没有同名 seed 也必须能被触发。
			changed = view.snapshot()
		} else {
			changed = delta.snapshot()
		}
		for _, rule := range compiled {
			evalRuleDelta(rule, view, changed, target, nextDelta)
		}
		if len(nextDelta.facts) == 0 {
			return nil
		}
		delta = nextDelta
	}
	return &FixpointError{Rounds: MaxFixpointRounds, Scope: scope}
}

// evalRuleDelta 只对包含某条新增事实的连接求值，产出尚未存在于 target 的 head 事实。
func evalRuleDelta(rule compiledRule, view world, changed map[string][]Fact, target, nextDelta *factSet) {
	seen := map[string]bool{}
	for atomIndex, atom := range rule.body {
		for _, changedFact := range changed[atom.predicate] {
			headFacts := joinFromSeed(rule.body, atomIndex, changedFact, make([]Term, rule.slotCount), view)
			for _, bindings := range headFacts {
				fact := instantiate(rule, bindings)
				key := factKey(fact)
				if !target.has(fact) && !seen[key] {
					seen[key] = true
					target.add(fact)
					nextDelta.add(fact)
				}
			}
		}
	}
}

// joinFromSeed 先把指定位置的规则体原子与新增事实统一，再用同一套递归
// 完成其余所有原子的连接，保证多原子规则体的绑定组合完整。
func joinFromSeed(atoms []compiledAtom, seedIndex int, seed Fact, bindings []Term, view world) [][]Term {
	seeded, ok := unify(atoms[seedIndex], seed, bindings)
	if !ok {
		return nil
	}
	results := joinForward(atoms, seedIndex+1, seeded, view)
	return joinBackward(atoms, seedIndex-1, results, view)
}

// joinForward 从种子右侧开始，按原子顺序完成连接。
func joinForward(atoms []compiledAtom, index int, bindings []Term, view world) [][]Term {
	if index == len(atoms) {
		return [][]Term{bindings}
	}
	var out [][]Term
	for _, candidate := range view.candidates(atoms[index], bindings) {
		next, ok := unify(atoms[index], candidate, bindings)
		if !ok {
			continue
		}
		out = append(out, joinForward(atoms, index+1, next, view)...)
	}
	return out
}

// joinBackward 对已经完成右侧连接的绑定集合，再从种子向左逐个连接。
func joinBackward(atoms []compiledAtom, index int, bindingsList [][]Term, view world) [][]Term {
	if index < 0 {
		return bindingsList
	}
	var out [][]Term
	for _, bindings := range bindingsList {
		for _, candidate := range view.candidates(atoms[index], bindings) {
			next, ok := unify(atoms[index], candidate, bindings)
			if !ok {
				continue
			}
			out = append(out, next)
		}
	}
	return joinBackward(atoms, index-1, out, view)
}

func walk(atoms []compiledAtom, index int, bindings []Term, view world) [][]Term {
	if index == len(atoms) {
		completed := make([]Term, len(bindings))
		copy(completed, bindings)
		return [][]Term{completed}
	}
	atom := atoms[index]
	var out [][]Term
	for _, candidate := range view.candidates(atom, bindings) {
		next, ok := unify(atom, candidate, bindings)
		if !ok {
			continue
		}
		out = append(out, walk(atoms, index+1, next, view)...)
	}
	return out
}

// unify 尝试让规则体原子与一条基实例事实在已有绑定下一致。
// 常量参数必须逐项相等；同一变量槽位必须绑定到同一个常量。
func unify(atom compiledAtom, fact Fact, bindings []Term) ([]Term, bool) {
	if atom.predicate != fact.Predicate || len(atom.args) != len(fact.Args) {
		return nil, false
	}
	// 每次成功统一都返回独立切片，避免不同递归分支共享底层数组、
	// 后续变量绑定覆盖前序分支已经算出的结果。
	next := make([]Term, len(bindings))
	copy(next, bindings)
	for i, pattern := range atom.args {
		value := fact.Args[i]
		if pattern.slot >= 0 {
			if isBound(bindings[pattern.slot]) {
				if !termsEqual(bindings[pattern.slot], value) {
					return nil, false
				}
				continue
			}
			next[pattern.slot] = value
			continue
		}
		if !termsEqual(pattern.term, value) {
			return nil, false
		}
	}
	return next, true
}

// isBound 判断变量槽位是否已绑定；未绑定槽位保持 Term 零值（KindVariable + 空名）。
func isBound(term Term) bool {
	return term.Kind != KindVariable || term.Name != ""
}

func instantiate(rule compiledRule, bindings []Term) Fact {
	args := make([]Term, len(rule.head.Args))
	for i, arg := range rule.head.Args {
		if rule.headSlots[i] >= 0 {
			args[i] = bindings[rule.headSlots[i]]
		} else {
			args[i] = arg
		}
	}
	return Fact{Predicate: rule.head.Predicate, Args: args}
}

func termsEqual(a, b Term) bool {
	return termKey(a) == termKey(b)
}

// queryHits 报告规则体查询在给定视图上是否至少存在一组绑定。
// 空规则体（allow if true）恒真。
func queryHits(body []Fact, view world) bool {
	if len(body) == 0 {
		return true
	}
	rule := compileRule(Rule{Body: body})
	return len(walk(rule.body, 0, make([]Term, rule.slotCount), view)) > 0
}

func factKey(fact Fact) string {
	out := fact.Predicate + "("
	for i, arg := range fact.Args {
		if i > 0 {
			out += ","
		}
		out += termKey(arg)
	}
	return out + ")"
}

func termKey(term Term) string {
	switch term.Kind {
	case KindVariable:
		return "v:" + term.Name
	case KindSymbol:
		return "s:" + term.Name
	case KindString:
		return "q:" + term.Text
	case KindInteger:
		return fmt.Sprintf("i:%d", term.Int)
	case KindBool:
		if term.Bool {
			return "b:1"
		}
		return "b:0"
	default:
		return "?"
	}
}
