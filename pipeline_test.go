package netsec

import (
	"bytes"
	"math/rand"
	"testing"
)

// 场景 1：百分号编码把危险分隔符重新引入。
// ';' 是危险字节且不在输入字母表中，最短反例应为 "%3B"。
func TestSearchPercentWitness(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'%', '0', '1', '3', 'B', 'a', 'b'},
		Dangerous:    []byte{';'},
		MaxLen:       MaxWitnessLen,
		BackslashMap: map[byte]byte{},
	}
	res := Search(cfg)
	if res.Status != WitnessFound {
		t.Fatalf("status = %s (%s), want witness_found", res.Status, res.Reason)
	}
	if got, want := string(res.Input), "%3B"; got != want {
		t.Fatalf("witness = %q, want %q", got, want)
	}
	if len(res.Hazards) != 1 || res.Hazards[0] != ';' {
		t.Fatalf("hazards = %v, want [;]", res.Hazards)
	}
	if got := res.Stages[stagePercent]; !bytes.Equal(got, []byte{';'}) {
		t.Fatalf("percent stage = %q, want \";\"", got)
	}
	assertTraceWellFormed(t, res.Trace, res.Stages)
}

// 场景 2：危险分隔符先经百分号解码变成反斜线，再由反斜线转义解码引入。
// 反斜线本身不在字母表中，因此最短反例是链式的 "%5Cn"。
func TestSearchChainedBackslashWitness(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'%', '0', '1', '5', 'C', 'n', 'x'},
		Dangerous:    []byte{'\n'},
		BackslashMap: map[byte]byte{'n': '\n'},
		MaxLen:       MaxWitnessLen,
	}
	res := Search(cfg)
	if res.Status != WitnessFound {
		t.Fatalf("status = %s (%s), want witness_found", res.Status, res.Reason)
	}
	if got, want := string(res.Input), "%5Cn"; got != want {
		t.Fatalf("witness = %q, want %q", got, want)
	}
	if !bytes.Equal(res.Stages[stagePercent], []byte{'\\', 'n'}) {
		t.Fatalf("percent stage = %q, want \"\\n\" literal", res.Stages[stagePercent])
	}
	if !bytes.Equal(res.Stages[stageBackslash], []byte{'\n'}) {
		t.Fatalf("backslash stage = %v, want newline", res.Stages[stageBackslash])
	}
	assertTraceWellFormed(t, res.Trace, res.Stages)

	// 反例必须确实通过声明检查，且更短输入不存在反例。
	for n := 0; n < len(res.Input); n++ {
		if witnessOfLength(cfg, n) != nil {
			t.Fatalf("found shorter witness of length %d", n)
		}
	}
}

// 场景 3：解码得到的危险字节随后被删除阶段清掉，因此不存在反例。
func TestSearchDeleteRemovesHazard(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'a', 'b', '%', '3', 'B'},
		Dangerous:    []byte{';'},
		BackslashMap: map[byte]byte{},
		Delete:       []byte{';'},
		MaxLen:       6,
		Budget:       2_000_000,
	}
	res := Search(cfg)
	if res.Status != Safe {
		t.Fatalf("status = %s, want safe", res.Status)
	}

	// 删除阶段的轨迹必须同时包含 delete-keep 与 delete-drop 记录。
	analyzed, err := Analyze(cfg, []byte("a%3B"))
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	var sawKeep, sawDrop bool
	for _, st := range analyzed.Trace {
		if st.Stage != stageDelete {
			continue
		}
		switch st.Rule {
		case ruleDeleteKeep:
			sawKeep = true
		case ruleDeleteDrop:
			sawDrop = true
		}
	}
	if !sawKeep || !sawDrop {
		t.Fatalf("delete trace incomplete: keep=%v drop=%v", sawKeep, sawDrop)
	}

	// 但删除集合为空时，同一输入空间应立刻暴露出最短反例 "%3B"。
	cfg.Delete = nil
	res = Search(cfg)
	if res.Status != WitnessFound || string(res.Input) != "%3B" {
		t.Fatalf("without delete: status=%s input=%q, want witness %%3B", res.Status, res.Input)
	}
}

// 场景 4：解码后出现的字节都会在删除阶段被清掉，不存在反例，应穷举为 Safe。
func TestSearchExhaustiveSafe(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'a', '%', '3', 'B'},
		Dangerous:    []byte{';'},
		BackslashMap: map[byte]byte{},
		Delete:       []byte{';'},
		MaxLen:       4,
		Budget:       10_000,
	}
	res := Search(cfg)
	if res.Status != Safe {
		t.Fatalf("status = %s (%s), want safe", res.Status, res.Reason)
	}
}

// 场景 5：预算极小，在找到反例前耗尽，必须返回 Unknown 而非误报 Safe。
func TestSearchBudgetExhausted(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'%', '0', '1', '3', 'B', 'a', 'b', 'c', 'd'},
		Dangerous:    []byte{';'},
		BackslashMap: map[byte]byte{},
		MaxLen:       MaxWitnessLen,
		Budget:       3,
	}
	res := Search(cfg)
	if res.Status != Unknown {
		t.Fatalf("status = %s, want unknown", res.Status)
	}
	if res.Reason != "" {
		t.Fatalf("Unknown must not carry a reason, got %q", res.Reason)
	}
}

// 场景 6：配置校验覆盖各类非法输入。
func TestInvalidConfigs(t *testing.T) {
	cases := map[string]Config{
		"empty alphabet":   {Dangerous: []byte{';'}},
		"empty dangerous":  {Alphabet: []byte{'a'}},
		"dup alphabet":     {Alphabet: []byte{'a', 'a'}, Dangerous: []byte{';'}},
		"dup dangerous":    {Alphabet: []byte{'a'}, Dangerous: []byte{';', ';'}},
		"dup delete":       {Alphabet: []byte{'a'}, Dangerous: []byte{';'}, Delete: []byte{'x', 'x'}},
		"maxlen too large": {Alphabet: []byte{'a'}, Dangerous: []byte{';'}, MaxLen: 13},
		"maxlen negative":  {Alphabet: []byte{'a'}, Dangerous: []byte{';'}, MaxLen: -1},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Search(cfg).Status; got != InvalidConfig {
				t.Fatalf("status = %s, want invalid_config", got)
			}
		})
	}
}

// 场景 7：Analyze 的输入校验与逐步轨迹来源链。
func TestAnalyze(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'%', '3', 'B', 'n'},
		Dangerous:    []byte{';'},
		BackslashMap: map[byte]byte{},
	}
	if _, err := Analyze(cfg, []byte{'z'}); err == nil {
		t.Fatal("expected error for byte outside alphabet")
	}

	// 危险字节若同时被字母表允许，Analyze 必须以声明检查失败拒绝它。
	hazardCfg := cfg
	hazardCfg.Alphabet = append(append([]byte{}, cfg.Alphabet...), ';')
	if _, err := Analyze(hazardCfg, []byte{';'}); err == nil {
		t.Fatal("expected declared-check rejection for hazardous input")
	} else if _, ok := err.(*HazardousInputError); !ok {
		t.Fatalf("error type = %T, want *HazardousInputError", err)
	}

	res, err := Analyze(cfg, []byte("%3Bn"))
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !bytes.Equal(res.Stages[stageDelete], []byte{';', 'n'}) {
		t.Fatalf("final = %q, want \";n\"", res.Stages[stageDelete])
	}
	assertTraceWellFormed(t, res.Trace, res.Stages)

	// 百分号逃逸一步必须消费三个来源位置，且输出落到解码字节。
	var foundEscape bool
	for _, st := range res.Trace {
		if st.Stage == stagePercent && st.Rule == rulePercentEscape {
			foundEscape = true
			if !bytes.Equal(st.Emitted, []byte{';'}) {
				t.Fatalf("escape emitted %v, want ;", st.Emitted)
			}
			if len(st.Origins) != 1 || len(st.Origins[0]) != 3 {
				t.Fatalf("escape origins = %v, want three source positions", st.Origins)
			}
		}
	}
	if !foundEscape {
		t.Fatal("percent-escape step not recorded")
	}
}

// 场景 9：边界语义——空输入合法、不完整百分号按字面量、解码阶段不递归。
func TestBoundarySemantics(t *testing.T) {
	cfg := Config{
		Alphabet:     []byte{'%', '2', '5', '3', 'B', 'a', '\\'},
		Dangerous:    []byte{';'},
		BackslashMap: map[byte]byte{},
	}

	empty, err := Analyze(cfg, nil)
	if err != nil {
		t.Fatalf("empty input error: %v", err)
	}
	for st, out := range empty.Stages {
		if len(out) != 0 {
			t.Fatalf("stage %d of empty input not empty: %q", st, out)
		}
	}

	// 不完整的 "%3" 必须原样保留，触发 percent-literal 与 copy 规则。
	partial, err := Analyze(cfg, []byte("%3"))
	if err != nil {
		t.Fatalf("partial escape error: %v", err)
	}
	if got := partial.Stages[stagePercent]; !bytes.Equal(got, []byte("%3")) {
		t.Fatalf("partial escape decoded to %q, want literal %%3", got)
	}

	// "%253B" 单次解码为 "%3B" 而非 ";"：阶段内不递归。
	once, err := Analyze(cfg, []byte("%253B"))
	if err != nil {
		t.Fatalf("non-recursive error: %v", err)
	}
	if got := once.Stages[stagePercent]; !bytes.Equal(got, []byte("%3B")) {
		t.Fatalf("single-stage decode = %q, want %%3B", got)
	}
	if len(once.Hazards) != 0 {
		t.Fatalf("non-recursive pipeline must not expose ';', got %v", once.Hazards)
	}

	// 未命中映射且处于末尾的反斜线按字面量保留。
	trail, err := Analyze(cfg, []byte{'a', '\\'})
	if err != nil {
		t.Fatalf("trailing backslash error: %v", err)
	}
	if got := trail.Stages[stageBackslash]; !bytes.Equal(got, []byte("a\\")) {
		t.Fatalf("trailing backslash = %q, want literal", got)
	}

	// 默认长度上限必须等于 12。
	defaults := Search(Config{
		Alphabet:     []byte{'a'},
		Dangerous:    []byte{';'},
		BackslashMap: map[byte]byte{},
	})
	if defaults.Status != Safe {
		t.Fatalf("single-symbol space status = %s, want safe", defaults.Status)
	}
	if _, _, _, maxLen, _, _ := (Config{
		Alphabet:  []byte{'a'},
		Dangerous: []byte{';'},
	}).validate(); maxLen != MaxWitnessLen {
		t.Fatalf("default MaxLen = %d, want %d", maxLen, MaxWitnessLen)
	}
}

// 场景 8：随机配置与随机输入上，两套独立实现必须逐阶段一致且轨迹可回放。
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 300; iter++ {
		cfg, danger, del := randomConfig(rng)
		in := randomInput(rng, cfg.Alphabet, 8)

		stagesA, okA := runPipelineA(in, cfg, danger, del)
		stagesB, trace, _, errB := runPipelineB(in, cfg, danger, del, 1_000_000)

		if errB == errDeclaredReject {
			if okA {
				t.Fatalf("iter %d: declared check disagreement on %q", iter, in)
			}
			continue
		}
		if errB != nil {
			t.Fatalf("iter %d: unexpected stepper error %v", iter, errB)
		}
		if diff := crossCheck(stagesA, stagesB); diff != "" {
			t.Fatalf("iter %d input %q: %s", iter, in, diff)
		}
		assertTraceWellFormed(t, trace, stagesB)

		// 搜索结论也必须与逐输入分析相容：找到的反例确实危险且通过声明检查。
		if iter%25 == 0 {
			res := Search(cfg)
			switch res.Status {
			case WitnessFound:
				if !declaredCheckA(res.Input, danger) {
					t.Fatalf("iter %d: witness fails declared check", iter)
				}
				if len(collectHazards(res.Stages[stageDelete], danger)) == 0 {
					t.Fatalf("iter %d: witness output has no hazard", iter)
				}
			case Safe, Unknown, InvalidConfig:
			default:
				t.Fatalf("iter %d: unexpected search status %s", iter, res.Status)
			}
		}
	}
}

func randomConfig(rng *rand.Rand) (Config, byteSet, byteSet) {
	alphaSize := 2 + rng.Intn(10)
	alpha := make([]byte, 0, alphaSize)
	used := make(map[byte]bool)
	for len(alpha) < alphaSize {
		b := byte(33 + rng.Intn(94))
		if !used[b] {
			used[b] = true
			alpha = append(alpha, b)
		}
	}
	danger := []byte{alpha[0]}
	var del []byte
	if rng.Intn(2) == 0 {
		del = []byte{alpha[1]}
	}
	m := map[byte]byte{}
	if rng.Intn(2) == 0 && len(alpha) > 2 {
		m[alpha[2]] = alpha[0]
	}
	cfg := Config{
		Alphabet:     alpha,
		Dangerous:    danger,
		Delete:       del,
		BackslashMap: m,
		MaxLen:       1 + rng.Intn(MaxWitnessLen),
		Budget:       100_000,
	}
	return cfg, makeByteSet(danger), makeByteSet(del)
}

func randomInput(rng *rand.Rand, alphabet []byte, maxLen int) []byte {
	n := rng.Intn(maxLen + 1)
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return out
}

func witnessOfLength(c Config, n int) []byte {
	alpha, danger, del, _, budget, reason := c.validate()
	if reason != "" {
		return nil
	}
	var gen func(prefix []byte) []byte
	gen = func(prefix []byte) []byte {
		if len(prefix) == n {
			res, _, mismatch, rejected := analyzeOne(c, prefix, danger, del, budget)
			if mismatch != "" || rejected || res == nil {
				return nil
			}
			if len(res.Hazards) > 0 {
				return prefix
			}
			return nil
		}
		for _, b := range alpha.sorted() {
			if danger.contains(b) {
				continue
			}
			if found := gen(append(prefix, b)); found != nil {
				return found
			}
		}
		return nil
	}
	return gen(nil)
}

// assertTraceWellFormed 回放逐步轨迹：
// 每一步的来源位置必须落在上一阶段输出范围内，按轨迹拼接可重建各阶段输出。
func assertTraceWellFormed(t *testing.T, trace []Step, stages [stageCount][]byte) {
	t.Helper()
	rebuilt := make([][]byte, stageCount)
	for _, st := range trace {
		if st.Stage < 0 || st.Stage >= stageCount {
			t.Fatalf("trace step has invalid stage %d", st.Stage)
		}
		var prev []byte
		if st.Stage == stageDeclared {
			prev = stages[stageDeclared]
		} else {
			prev = stages[st.Stage-1]
		}
		for _, chain := range st.Origins {
			for _, p := range chain {
				if p < 0 || p >= len(prev) {
					t.Fatalf("stage %d step at %d: origin %d out of range [0,%d)",
						st.Stage, st.Pos, p, len(prev))
				}
			}
		}
		rebuilt[st.Stage] = append(rebuilt[st.Stage], st.Emitted...)
	}
	for st := 1; st < stageCount; st++ {
		if !bytes.Equal(rebuilt[st], stages[st]) {
			t.Fatalf("stage %d replay = %q, want %q", st, rebuilt[st], stages[st])
		}
	}
}
