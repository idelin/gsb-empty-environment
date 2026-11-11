package netsec

// Search 在长度 0..MaxLen 的有限输入空间上，按长度递增搜索最短反例：
// 输入本身不含任何危险分隔符（通过声明检查），但管线最终输出出现危险分隔符。
//
// 每个候选都同时交给评估器 A（批量）与评估器 B（逐步状态机）独立执行，
// 并逐阶段、逐字节交叉核对；任一变换位置不一致即返回 VerificationFailed。
// 预算按评估器 B 的原子执行步骤计数，预算耗尽返回 Unknown。
func Search(c Config) *Result {
	alpha, danger, del, maxLen, budget, reason := c.validate()
	if reason != "" {
		return &Result{Status: InvalidConfig, Reason: reason}
	}

	safeAlphabet := make([]byte, 0, len(alpha))
	for _, b := range alpha.sorted() {
		if !danger.contains(b) {
			safeAlphabet = append(safeAlphabet, b)
		}
	}

	remaining := budget
	visited := make(map[string]struct{})
	queue := [][]byte{nil}
	visited[""] = struct{}{}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		res, used, mismatch, rejected := analyzeOne(c, cur, danger, del, remaining)
		remaining -= used
		if mismatch != "" {
			return &Result{Status: VerificationFailed, Reason: mismatch, Input: cloneBytes(cur)}
		}
		if rejected {
			// 候选只由非危险字节构成，正常情况下不会发生；交由两套实现复核兜底。
			return &Result{
				Status: VerificationFailed,
				Reason: "declared check rejected an alphabet-only candidate",
				Input:  cloneBytes(cur),
			}
		}
		if res == nil {
			// 评估器 B 预算耗尽。
			return &Result{Status: Unknown}
		}

		if len(res.Hazards) > 0 {
			res.Status = WitnessFound
			return res
		}

		if len(cur) >= maxLen {
			continue
		}
		for _, b := range safeAlphabet {
			next := make([]byte, 0, len(cur)+1)
			next = append(next, cur...)
			next = append(next, b)
			key := string(next)
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, next)
		}
	}

	// 队列耗尽即整个有限输入空间穷举完毕。
	return &Result{Status: Safe}
}

// Analyze 对单个输入跑完整条管线并返回逐步轨迹，便于宿主直接审计。
// 输入字节不在 Alphabet 中、或声明检查未通过时返回错误；
// 两套实现出现分歧时结果状态为 VerificationFailed。
func Analyze(c Config, in []byte) (*Result, error) {
	alpha, danger, del, _, _, reason := c.validate()
	if reason != "" {
		return &Result{Status: InvalidConfig, Reason: reason}, nil
	}
	for _, b := range in {
		if !alpha.contains(b) {
			return nil, &InputError{Byte: b, Pos: indexOf(in, b)}
		}
	}
	budget := c.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	res, _, mismatch, rejected := analyzeOne(c, in, danger, del, budget)
	if mismatch != "" {
		return &Result{Status: VerificationFailed, Reason: mismatch, Input: cloneBytes(in)}, nil
	}
	if rejected {
		return nil, &HazardousInputError{Input: cloneBytes(in)}
	}
	if res == nil {
		return &Result{Status: Unknown, Input: cloneBytes(in)}, nil
	}
	return res, nil
}

// InputError 表示输入使用了字母表之外的字节。
type InputError struct {
	Byte byte
	Pos  int
}

func (e *InputError) Error() string {
	return "netsec: byte not in alphabet at pos " + itoa(e.Pos)
}

// HazardousInputError 表示输入直接含有危险分隔符，声明检查不予通过。
type HazardousInputError struct {
	Input []byte
}

func (e *HazardousInputError) Error() string {
	return "netsec: input rejected by declared check"
}

func indexOf(in []byte, b byte) int {
	for i, x := range in {
		if x == b {
			return i
		}
	}
	return -1
}

// analyzeOne 用两套独立实现执行同一输入并交叉核对。
//
//	res      ：核对通过后的完整结果（含阶段输出、逐步轨迹、危险字节）；预算耗尽时为 nil
//	used     ：本次评估器 B 消耗的预算步骤数
//	mismatch ：非空表示两套实现在某变换位置分歧
//	rejected ：声明检查未通过
func analyzeOne(c Config, in []byte, danger, del byteSet, budget int64) (res *Result, used int64, mismatch string, rejected bool) {
	stagesA, okA := runPipelineA(in, c, danger, del)

	stagesB, trace, remaining, errB := runPipelineB(in, c, danger, del, budget)
	used = budget - remaining

	if errB == errDeclaredReject {
		if okA {
			return nil, used, "declared check disagreement: A=accept B=reject", false
		}
		return nil, used, "", true
	}
	if errB == errBudgetExhausted {
		if !okA {
			return nil, used, "declared check disagreement: A=reject B=exhausted", false
		}
		return nil, used, "", false
	}
	if errB != nil {
		return nil, used, "stepper failure: " + errB.Error(), false
	}
	if !okA {
		return nil, used, "declared check disagreement: A=reject B=accept", false
	}

	if diff := crossCheck(stagesA, stagesB); diff != "" {
		return nil, used, diff, false
	}

	out := stagesB[stageDelete]
	hazards := collectHazards(out, danger)
	return &Result{
		Input:   cloneBytes(in),
		Hazards: hazards,
		Stages:  cloneStages(stagesB),
		Trace:   cloneTrace(trace),
	}, used, "", false
}

func collectHazards(out []byte, danger byteSet) []byte {
	seen := make(byteSet)
	var hazards []byte
	for _, b := range out {
		if danger.contains(b) && !seen.contains(b) {
			seen[b] = struct{}{}
			hazards = append(hazards, b)
		}
	}
	return hazards
}

func cloneStages(in [stageCount][]byte) [stageCount][]byte {
	var out [stageCount][]byte
	for i := range in {
		out[i] = cloneBytes(in[i])
	}
	return out
}

func cloneTrace(in []Step) []Step {
	if len(in) == 0 {
		return nil
	}
	out := make([]Step, len(in))
	for i, st := range in {
		out[i] = Step{
			Stage:   st.Stage,
			Rule:    st.Rule,
			Pos:     st.Pos,
			Emitted: cloneBytes(st.Emitted),
			Origins: cloneOrigins(st.Origins),
		}
	}
	return out
}
