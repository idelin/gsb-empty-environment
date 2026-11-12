package netsec

import "sort"

// 域转换所需的三条权限及其所属类。
const (
	classProcess = "process"
	classFile    = "file"

	permTransition = "transition"
	permEntrypoint = "entrypoint"
	permExecute    = "execute"
)

// Step 描述一次 exec 引发的域转换：From 域执行 FileType 类型的
// 入口文件后进入 To 域。
type Step struct {
	From     string
	To       string
	FileType string
}

// Violation 表示一条被违反的 neverallow，Source/Target/Class/Perm 为
// 属性展开后命中的具体组合。Chain 是从起始域出发、依赖该违规 allow
// 的最短转换链，作为证据；若该 allow 未参与任何可达转换链则为 nil。
type Violation struct {
	Rule   NeverallowRule
	Source string
	Target string
	Class  string
	Perm   string
	Chain  []Step
}

// Report 是 Analyze 的输出。
type Report struct {
	Start string
	// Reachable 是从起始域经一次或多次 exec 可到达的域，按字典序排列。
	Reachable []string
	// Chains 记录每个可达域（含起始域自身，空链）的最短转换链。
	Chains map[string][]Step
	// Violations 是所有被违反的 neverallow 组合。
	Violations []Violation
}

type allowKey struct {
	src   string
	tgt   string
	class string
}

type edge struct {
	from   string
	to     string
	file   string
	allows [3]allowKey // transition、entrypoint、execute 三条具体 allow
}

// Analyze 从 start 域出发，基于 type_transition 与 transition/entrypoint/
// execute 三条权限构建域转换图，求可达域集合与最短链，并检查 neverallow。
// dontaudit 规则不影响放行判定，不参与任何计算。
func Analyze(p *Policy, start string) *Report {
	permTab := buildPermTable(p)
	has := func(k allowKey, perm string) bool { return permTab[k][perm] }

	// 由 type_transition 加三条权限构建转换边。
	var edges []*edge
	byAllow := map[allowKey][]*edge{}
	outEdges := map[string][]*edge{}
	for _, tt := range p.Transitions {
		for _, d := range p.expand(tt.Source) {
			for _, f := range p.expand(tt.Target) {
				t := tt.Default
				kTrans := allowKey{d, t, classProcess}
				kEntry := allowKey{t, f, classFile}
				kExec := allowKey{d, f, classFile}
				if !has(kTrans, permTransition) ||
					!has(kEntry, permEntrypoint) ||
					!has(kExec, permExecute) {
					continue
				}
				e := &edge{from: d, to: t, file: f, allows: [3]allowKey{kTrans, kEntry, kExec}}
				edges = append(edges, e)
				outEdges[d] = append(outEdges[d], e)
				for _, k := range e.allows {
					byAllow[k] = append(byAllow[k], e)
				}
			}
		}
	}

	// BFS 求每个可达域的最短转换链。
	chains := map[string][]Step{start: nil}
	queue := []string{start}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		for _, e := range outEdges[d] {
			if _, ok := chains[e.to]; ok {
				continue
			}
			chain := append(append([]Step{}, chains[d]...), Step{From: e.from, To: e.to, FileType: e.file})
			chains[e.to] = chain
			queue = append(queue, e.to)
		}
	}

	rep := &Report{Start: start, Chains: chains}
	for d := range chains {
		if d != start {
			rep.Reachable = append(rep.Reachable, d)
		}
	}
	sort.Strings(rep.Reachable)

	rep.Violations = checkNeverallows(p, permTab, byAllow, chains)
	return rep
}

// buildPermTable 将 allow 规则展开为具体类型权限表。
// dontaudit 仅抑制审计，不影响放行，故不并入。
func buildPermTable(p *Policy) map[allowKey]map[string]bool {
	tab := map[allowKey]map[string]bool{}
	for _, a := range p.Allows {
		for _, s := range p.expand(a.Source) {
			for _, t := range p.expand(a.Target) {
				k := allowKey{s, t, a.Class}
				if tab[k] == nil {
					tab[k] = map[string]bool{}
				}
				for _, perm := range a.Perms {
					tab[k][perm] = true
				}
			}
		}
	}
	return tab
}

// checkNeverallows 将 neverallow 展开后与具体 allow 逐一比对，
// 并为每个命中组合附上最短转换链作为证据。
func checkNeverallows(p *Policy, permTab map[allowKey]map[string]bool,
	byAllow map[allowKey][]*edge, chains map[string][]Step) []Violation {
	var out []Violation
	for _, nr := range p.Neverallows {
		srcSet := toSet(p.expand(nr.Source))
		tgtSet := toSet(p.expand(nr.Target))
		for k, perms := range permTab {
			if !srcSet[k.src] || !tgtSet[k.tgt] || k.class != nr.Class {
				continue
			}
			for _, perm := range nr.Perms {
				if !perms[perm] {
					continue
				}
				out = append(out, Violation{
					Rule:   nr,
					Source: k.src,
					Target: k.tgt,
					Class:  k.class,
					Perm:   perm,
					Chain:  evidenceChain(k, byAllow, chains),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Perm < out[j].Perm
	})
	return out
}

// evidenceChain 在使用了 allow k 的转换边中，选取从起始域出发
// 总长度最短的一条链作为证据；无可达边时返回 nil。
func evidenceChain(k allowKey, byAllow map[allowKey][]*edge, chains map[string][]Step) []Step {
	var best []Step
	for _, e := range byAllow[k] {
		prefix, ok := chains[e.from]
		if !ok {
			continue
		}
		chain := append(append([]Step{}, prefix...), Step{From: e.from, To: e.to, FileType: e.file})
		if best == nil || len(chain) < len(best) {
			best = chain
		}
	}
	return best
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, it := range items {
		s[it] = true
	}
	return s
}
