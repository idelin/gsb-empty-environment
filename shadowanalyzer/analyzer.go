package main

import (
	"fmt"
	"sort"
	"strings"
)

// Status 是规则的可生效性分类。
type Status string

const (
	StatusUnshadowed Status = "unshadowed"
	StatusPartial    Status = "partially_shadowed"
	StatusFull       Status = "fully_shadowed"
)

// Witness 是一条使规则成为首个匹配的具体请求。
type Witness struct {
	Principal string   `json:"principal"`
	Action    string   `json:"action"`
	Path      []string `json:"path"`
}

// RuleReport 是单条规则的分析结果。
type RuleReport struct {
	Rule      string   `json:"rule"`
	Status    Status   `json:"status"`
	Witness   *Witness `json:"witness,omitempty"`
	CoveredBy []string `json:"coveredBy,omitempty"`
}

// Report 是完整分析结果。
type Report struct {
	Rules []RuleReport `json:"rules"`
}

// analyzer 持有编译后的规则与自动机。
type analyzer struct {
	in       *Input
	compiled []compiledRule
	nfas     []*nfa
	alphabet []string
	otherSeg string // 见证中代表"任意其它片段"的显示名
}

// Analyze 对全部规则执行遮蔽分析。
func Analyze(in *Input) (*Report, error) {
	compiled, alphabet, err := validateAndCompile(in)
	if err != nil {
		return nil, err
	}
	a := &analyzer{in: in, compiled: compiled, alphabet: alphabet}
	a.otherSeg = freshName("other", map[string]bool{}, alphabet)
	for _, cr := range compiled {
		a.nfas = append(a.nfas, buildNFA(cr.segs, alphabet))
	}

	rep := &Report{}
	for i := range compiled {
		rep.Rules = append(rep.Rules, a.analyzeRule(i))
	}
	return rep, nil
}

func freshName(base string, taken map[string]bool, alphabet []string) string {
	for _, s := range alphabet {
		taken[s] = true
	}
	name := base
	for k := 1; taken[name]; k++ {
		name = fmt.Sprintf("%s_%d", base, k)
	}
	return name
}

// coverersFor 返回在 (pIdx, aIdx) 上适用的前序规则下标。
func (a *analyzer) coverersFor(ruleIdx, pIdx, aIdx int) []int {
	var out []int
	for j := 0; j < ruleIdx; j++ {
		for _, pair := range a.compiled[j].applies {
			if pair[0] == pIdx && pair[1] == aIdx {
				out = append(out, j)
				break
			}
		}
	}
	return out
}

func (a *analyzer) nfasOf(indices []int) []*nfa {
	out := make([]*nfa, 0, len(indices))
	for _, j := range indices {
		out = append(out, a.nfas[j])
	}
	return out
}

func (a *analyzer) analyzeRule(i int) RuleReport {
	cr := a.compiled[i]
	rep := RuleReport{Rule: cr.rule.Name, Status: StatusFull}

	fullyCovered := true
	overlapsAny := false
	var witness *Witness

	for _, pair := range cr.applies {
		coverIdx := a.coverersFor(i, pair[0], pair[1])
		coverNFAs := a.nfasOf(coverIdx)
		if len(coverNFAs) > 0 && intersectsAny(a.nfas[i], coverNFAs, a.alphabet) {
			overlapsAny = true
		}
		word := differenceWitness(a.nfas[i], coverNFAs, a.alphabet)
		if word != nil {
			fullyCovered = false
			if witness == nil {
				witness = &Witness{
					Principal: a.in.Principals[pair[0]],
					Action:    a.in.Actions[pair[1]],
					Path:      a.displayPath(word),
				}
			}
		}
	}

	switch {
	case fullyCovered:
		rep.Status = StatusFull
		rep.CoveredBy = a.minimalCover(i)
	case overlapsAny:
		rep.Status = StatusPartial
		rep.Witness = witness
	default:
		rep.Status = StatusUnshadowed
		rep.Witness = witness
	}
	return rep
}

// displayPath 把内部符号序列转成可展示的路径片段。
func (a *analyzer) displayPath(word []string) []string {
	out := make([]string, len(word))
	for i, sym := range word {
		if sym == otherSymbol {
			out[i] = a.otherSeg
		} else {
			out[i] = sym
		}
	}
	return out
}

// coveredBySet 检查规则 i 是否被前序规则子集 subset（下标集合）完全覆盖。
func (a *analyzer) coveredBySet(i int, subset []int) bool {
	for _, pair := range a.compiled[i].applies {
		var covers []*nfa
		for _, j := range subset {
			for _, jp := range a.compiled[j].applies {
				if jp == pair {
					covers = append(covers, a.nfas[j])
					break
				}
			}
		}
		if differenceWitness(a.nfas[i], covers, a.alphabet) != nil {
			return false
		}
	}
	return true
}

// minimalCover 计算覆盖规则 i 的最小前序规则集合（按规则名返回）。
// 候选数较少时枚举全部子集求精确最小；否则用贪心删除法求极小集合。
func (a *analyzer) minimalCover(i int) []string {
	candidates := make([]int, 0, i)
	for j := 0; j < i; j++ {
		candidates = append(candidates, j)
	}

	var best []int
	if len(candidates) <= 14 {
		best = exactMinCover(a, i, candidates)
	} else {
		best = greedyMinCover(a, i, candidates)
	}

	names := make([]string, 0, len(best))
	for _, j := range best {
		names = append(names, a.compiled[j].rule.Name)
	}
	sort.Strings(names)
	return names
}

func exactMinCover(a *analyzer, i int, candidates []int) []int {
	n := len(candidates)
	// 按子集大小递增枚举，第一个可覆盖的就是最小集合。
	for size := 0; size <= n; size++ {
		var found []int
		var walk func(start, left int, picked []int) bool
		walk = func(start, left int, picked []int) bool {
			if left == 0 {
				if a.coveredBySet(i, picked) {
					found = append([]int{}, picked...)
					return true
				}
				return false
			}
			for s := start; s <= n-left; s++ {
				if walk(s+1, left-1, append(picked, candidates[s])) {
					return true
				}
			}
			return false
		}
		if walk(0, size, nil) {
			return found
		}
	}
	return nil
}

func greedyMinCover(a *analyzer, i int, candidates []int) []int {
	set := append([]int{}, candidates...)
	k := 0
	for k < len(set) {
		trial := make([]int, 0, len(set)-1)
		trial = append(trial, set[:k]...)
		trial = append(trial, set[k+1:]...)
		if a.coveredBySet(i, trial) {
			set = trial
		} else {
			k++
		}
	}
	return set
}

// FormatText 输出人类可读的报告。
func (r *Report) FormatText() string {
	var sb strings.Builder
	for _, rr := range r.Rules {
		switch rr.Status {
		case StatusFull:
			fmt.Fprintf(&sb, "[完全遮蔽] %s — 永远无法生效\n", rr.Rule)
			fmt.Fprintf(&sb, "  覆盖它的最小前序规则集合: %s\n", strings.Join(rr.CoveredBy, ", "))
		case StatusPartial:
			fmt.Fprintf(&sb, "[部分遮蔽] %s\n", rr.Rule)
			fmt.Fprintf(&sb, "  可生效见证: %s\n", formatWitness(rr.Witness))
		default:
			fmt.Fprintf(&sb, "[未被遮蔽] %s\n", rr.Rule)
			fmt.Fprintf(&sb, "  可生效见证: %s\n", formatWitness(rr.Witness))
		}
	}
	return sb.String()
}

func formatWitness(w *Witness) string {
	if w == nil {
		return "(无)"
	}
	return fmt.Sprintf("principal=%s action=%s path=/%s", w.Principal, w.Action, strings.Join(w.Path, "/"))
}
