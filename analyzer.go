package leaklab

import "fmt"

// 分析结论状态。
const (
	// StatusFound 表示在预算内找到了符合条件的泄漏见证。
	StatusFound = "found"
	// StatusBoundedNotFound 表示预算足以穷尽全部候选且未发现泄漏。
	StatusBoundedNotFound = "bounded-not-found"
	// StatusUnknown 表示预算在穷尽候选前耗尽，结论不可判定。
	StatusUnknown = "unknown"
)

// Config 是一次分析请求的完整输入（程序、输入布局与执行预算）。
type Config struct {
	Program *Program
	Spec    InputSpec
	// ExecBudget 为允许调用模拟器的最大次数；每次架构运行（含其推测窗口）计一次。
	// <=0 表示不限预算。
	ExecBudget int
}

// RunView 是见证中一侧运行的可重放快照。
type RunView struct {
	Input        RunInput   `json:"input"`
	MispredictPC int        `json:"mispredict_pc"`
	Result       *RunResult `json:"result"`
}

// Witness 是可独立重放的泄漏见证：两个公开输入相同、秘密不同的运行，
// 架构 probe 序列一致而推测 probe 序列不同，且误预测发生在同一静态 jmpeq。
type Witness struct {
	ProgramText string    `json:"program_text"`
	Spec        InputSpec `json:"spec"`
	SitePC      uint8     `json:"mispredict_pc"`
	A           RunView   `json:"run_a"`
	B           RunView   `json:"run_b"`
}

// AnalysisResult 为 Analyze 的返回。
type AnalysisResult struct {
	Status     string   `json:"status"`
	Witness    *Witness `json:"witness,omitempty"`
	Executions int      `json:"executions"`
	Candidates int      `json:"candidates"`
	Exhausted  bool     `json:"exhausted"`
	Reason     string   `json:"reason,omitempty"`
}

type candidateEntry struct {
	secret uint8
	view   RunView
}

// Analyze 按预算穷举公开输入组合与秘密取值，检测被分支预测隐藏的推测泄漏。
//
// 枚举顺序固定（公开组合按小端字典序，秘密 0..255，静态 jmpeq 按下标顺序），
// 保证可重放。预算在每次模拟器调用前检查；预算耗尽且仍有候选未枚举时返回未知，
// 只有全部候选枚举完毕仍无见证才返回有界未发现。
func Analyze(cfg Config) (*AnalysisResult, error) {
	if cfg.Program == nil {
		return nil, fmt.Errorf("analyze: nil program")
	}
	if err := cfg.Program.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Spec.Validate(); err != nil {
		return nil, err
	}
	sites := jumpSites(cfg.Program)
	out := &AnalysisResult{Status: StatusUnknown}

	totalCombos := publicComboCount(len(cfg.Spec.Public))
	totalCandidates := totalCombos * 256

	// groups 以 (公开组合, 误预测 PC) 为键，按秘密值收集可配对的推测运行。
	type groupKey struct {
		combo  int
		sitePC int
	}
	groups := map[groupKey][]candidateEntry{}

	for combo := 0; combo < totalCombos; combo++ {
		pub := decodeCombo(combo, len(cfg.Spec.Public))
		for secret := 0; secret < 256; secret++ {
			in := RunInput{Public: pub, Secret: uint8(secret)}

			// 基线架构运行：确认候选可终止并取得架构 probe 序列。
			base, ok := consume(cfg, out, nil, in)
			if !ok {
				return out, nil
			}
			out.Candidates++
			if base.Status == StatusStepBudget {
				// 超限运行按题面为未知，不参与配对，但候选已枚举。
				continue
			}

			// 每个静态 jmpeq 站点各做一次“仅此站点反转”的运行。
			for _, sitePC := range sites {
				rr, ok := consume(cfg, out, &sitePC, in)
				if !ok {
					return out, nil
				}
				if !rr.Mispredicted || rr.Status == StatusStepBudget || len(rr.SpecTrace) == 0 {
					continue
				}
				key := groupKey{combo: combo, sitePC: sitePC}
				entries := groups[key]
				view := RunView{
					Input:        RunInput{Public: append([]uint8(nil), pub...), Secret: uint8(secret)},
					MispredictPC: sitePC,
					Result:       rr,
				}
				for _, prev := range entries {
					if probeEqual(prev.view.Result.ArchTrace, rr.ArchTrace) &&
						!probeEqual(prev.view.Result.SpecTrace, rr.SpecTrace) {
						out.Status = StatusFound
						out.Witness = &Witness{
							ProgramText: cfg.Program.Text(),
							Spec:        cfg.Spec,
							SitePC:      uint8(sitePC),
							A:           prev.view,
							B:           view,
						}
						out.Exhausted = false
						return out, nil
					}
				}
				groups[key] = append(entries, candidateEntry{secret: uint8(secret), view: view})
			}
		}
	}

	out.Exhausted = out.Candidates == totalCandidates
	if out.Exhausted {
		out.Status = StatusBoundedNotFound
		out.Reason = "all candidates within budget exhausted without a speculative-only leak"
	} else {
		out.Status = StatusUnknown
		out.Reason = "execution budget exhausted before all candidates were enumerated"
	}
	return out, nil
}

func jumpSites(prog *Program) []int {
	var sites []int
	for i, ins := range prog.Instr {
		if ins.Op == OpJmpEq {
			sites = append(sites, i)
		}
	}
	return sites
}

func publicComboCount(publicCount int) int {
	count := 1
	for i := 0; i < publicCount; i++ {
		count *= 256
	}
	return count
}

// decodeCombo 把 0..256^k-1 的组合号解码为按声明顺序排列的公开字节（小端）。
func decodeCombo(combo, publicCount int) []uint8 {
	pub := make([]uint8, publicCount)
	v := combo
	for i := 0; i < publicCount; i++ {
		pub[i] = uint8(v % 256)
		v /= 256
	}
	return pub
}

func probeEqual(a, b []ProbeEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// consume 在预算允许时执行一次模拟器调用；预算耗尽返回 ok=false。
func consume(cfg Config, out *AnalysisResult, sitePC *int, in RunInput) (*RunResult, bool) {
	if cfg.ExecBudget > 0 && out.Executions >= cfg.ExecBudget {
		out.Status = StatusUnknown
		out.Reason = "execution budget exhausted before all candidates were enumerated"
		return nil, false
	}
	opts := RunOptions{MispredictPC: -1}
	if sitePC != nil {
		opts.MispredictPC = *sitePC
	}
	rr, err := Run(cfg.Program, cfg.Spec, in, opts)
	if err != nil {
		// 配置类错误本应在 Analyze 入口拦截，这里防御性上抛。
		panic(fmt.Sprintf("leaklab: unexpected run error: %v", err))
	}
	out.Executions++
	return rr, true
}
