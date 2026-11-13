package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// binding 是变量名到 ground 词项的一组绑定。
type binding map[string]Term

func termKey(t Term) string {
	switch t.Kind {
	case TermVariable:
		return "v:" + t.Value
	case TermSymbol:
		return "s:" + t.Value
	case TermString:
		return "q:" + t.Value
	case TermInteger:
		return "i:" + t.Value
	default:
		return "?:" + t.Value
	}
}

func predicateKey(p Predicate) string {
	out := strconv.Itoa(len(p.Args)) + ":" + p.Name
	for _, arg := range p.Args {
		out += "|" + termKey(arg)
	}
	return out
}

// factSet 是去重的 ground 事实集合，按 名称#元数 建索引便于匹配。
type factSet struct {
	byKey   map[string]Predicate
	byIndex map[string][]Predicate
}

func newFactSet() *factSet {
	return &factSet{
		byKey:   map[string]Predicate{},
		byIndex: map[string][]Predicate{},
	}
}

func factIndex(name string, arity int) string {
	return name + "#" + strconv.Itoa(arity)
}

// add 插入一条 ground 事实，返回是否为新事实。
func (f *factSet) add(p Predicate) bool {
	if _, ok := f.byKey[predicateKey(p)]; ok {
		return false
	}
	f.byKey[predicateKey(p)] = p
	idx := factIndex(p.Name, len(p.Args))
	f.byIndex[idx] = append(f.byIndex[idx], p)
	return true
}

func (f *factSet) contains(p Predicate) bool {
	_, ok := f.byKey[predicateKey(p)]
	return ok
}

// matchPred 用单个谓词模式对事实集合做匹配，扩展已有绑定集合。
// 常量参数要求相等，同名变量要求绑定一致，未绑定变量则取值并新增绑定。
func matchPred(facts *factSet, pattern Predicate, incoming []binding) []binding {
	var out []binding
	candidates := facts.byIndex[factIndex(pattern.Name, len(pattern.Args))]
	for _, base := range incoming {
		for _, fact := range candidates {
			next, ok := bindOne(base, pattern, fact)
			if ok {
				out = append(out, next)
			}
		}
	}
	return out
}

func bindOne(base binding, pattern, fact Predicate) (binding, bool) {
	result := make(binding, len(base)+len(pattern.Args))
	for k, v := range base {
		result[k] = v
	}
	for i, arg := range pattern.Args {
		actual := fact.Args[i]
		if arg.IsVariable() {
			if existing, ok := result[arg.Value]; ok {
				if termKey(existing) != termKey(actual) {
					return nil, false
				}
				continue
			}
			result[arg.Value] = actual
			continue
		}
		if termKey(arg) != termKey(actual) {
			return nil, false
		}
	}
	return result, true
}

// query 对一条规则体做连接求值，返回所有满足的绑定。
func query(facts *factSet, body []Predicate) []binding {
	bindings := []binding{{}}
	for _, pred := range body {
		bindings = matchPred(facts, pred, bindings)
		if len(bindings) == 0 {
			return nil
		}
		seen := make(map[string]bool, len(bindings))
		deduped := make([]binding, 0, len(bindings))
		for _, b := range bindings {
			key := bindingKey(b)
			if seen[key] {
				continue
			}
			seen[key] = true
			deduped = append(deduped, b)
		}
		bindings = deduped
	}
	return bindings
}

func bindingKey(b binding) string {
	keys := make([]string, 0, len(b))
	for key := range b {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, key := range keys {
		sb.WriteString(key)
		sb.WriteByte('=')
		sb.WriteString(termKey(b[key]))
		sb.WriteByte(';')
	}
	return sb.String()
}

func substitute(head Predicate, b binding) (Predicate, error) {
	out := Predicate{Name: head.Name, Args: make([]Term, 0, len(head.Args))}
	for _, arg := range head.Args {
		if !arg.IsVariable() {
			out.Args = append(out.Args, arg)
			continue
		}
		value, ok := b[arg.Value]
		if !ok || value.IsVariable() {
			return Predicate{}, fmt.Errorf("%w: unbound variable $%s while deriving %s", ErrSafety, arg.Value, head.Name)
		}
		out.Args = append(out.Args, value)
	}
	return out, nil
}

// solve 从 seed 事实出发，用半朴素（semi-naive）增量求值反复应用 rules 直到不动点。
// 每轮规则体只在一个谓词位置上匹配上一轮的增量事实，其余谓词匹配全量事实，
// 从而避免对同一组事实的重复连接。每轮没有新事实即收敛；
// 超过 MaxRounds 轮返回 ErrFixedPointLimit。
func solve(seed []Predicate, rules []Rule) (*factSet, error) {
	facts := newFactSet()
	delta := newFactSet()
	for _, fact := range seed {
		if !fact.IsGround() {
			return nil, fmt.Errorf("%w: seed fact %s must be ground", ErrSafety, fact.Name)
		}
		facts.add(fact)
		delta.add(fact)
	}
	for round := 1; round <= MaxRounds; round++ {
		nextDelta := newFactSet()
		for _, rule := range rules {
			bindings := evaluateRuleSemiNaive(facts, delta, rule.Body)
			for _, b := range bindings {
				derived, err := substitute(rule.Head, b)
				if err != nil {
					return nil, err
				}
				if !facts.contains(derived) {
					nextDelta.add(derived)
				}
			}
		}
		if len(nextDelta.byKey) == 0 {
			return facts, nil
		}
		for key, fact := range nextDelta.byKey {
			facts.byKey[key] = fact
			idx := factIndex(fact.Name, len(fact.Args))
			facts.byIndex[idx] = append(facts.byIndex[idx], fact)
		}
		delta = nextDelta
	}
	return nil, fmt.Errorf("%w after %d rounds", ErrFixedPointLimit, MaxRounds)
}

// evaluateRuleSemiNaive 对规则体做半朴素求值：依次让第 i 个谓词只匹配增量事实，
// 其余谓词匹配全量事实，并对绑定去重。谓词名/元数与增量集合不相交的位置自然跳过。
func evaluateRuleSemiNaive(all, delta *factSet, body []Predicate) []binding {
	seen := map[string]bool{}
	var out []binding
	for i := range body {
		if len(delta.byIndex[factIndex(body[i].Name, len(body[i].Args))]) == 0 {
			continue
		}
		bindings := []binding{{}}
		for j, pred := range body {
			source := all
			if i == j {
				source = delta
			}
			bindings = matchPred(source, pred, bindings)
			if len(bindings) == 0 {
				break
			}
		}
		for _, b := range bindings {
			key := bindingKey(b)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, b)
		}
	}
	return out
}

// checkPassed 报告 check 的规则体是否至少推出一组绑定。
func checkPassed(facts *factSet, check Check) bool {
	return len(query(facts, check.Body)) > 0
}
