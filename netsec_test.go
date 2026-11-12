package netsec

import (
	"errors"
	"math/big"
	"testing"
)

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func powInt(base, exp int) int {
	out := 1
	for i := 0; i < exp; i++ {
		out *= base
	}
	return out
}

func assertSameResult(t *testing.T, want, got *Result) {
	t.Helper()
	if want.Possible != got.Possible || want.MAP != got.MAP {
		t.Fatalf("result mismatch: want=%+v got=%+v", want, got)
	}
	if want.Likelihood.Cmp(got.Likelihood) != 0 {
		t.Fatalf("likelihood mismatch: want=%s got=%s", want.Likelihood, got.Likelihood)
	}
	if want.MAPPosterior.Cmp(got.MAPPosterior) != 0 {
		t.Fatalf("MAP posterior mismatch: want=%s got=%s", want.MAPPosterior, got.MAPPosterior)
	}
	if len(want.Posterior) != len(got.Posterior) || len(want.Compatible) != len(got.Compatible) {
		t.Fatalf("slice length mismatch: want=%+v got=%+v", want, got)
	}
	for i := range want.Posterior {
		if want.Posterior[i].Cmp(got.Posterior[i]) != 0 {
			t.Fatalf("posterior[%d] mismatch: want=%s got=%s", i, want.Posterior[i], got.Posterior[i])
		}
	}
	for i := range want.Compatible {
		if want.Compatible[i] != got.Compatible[i] {
			t.Fatalf("compatible mismatch: want=%v got=%v", want.Compatible, got.Compatible)
		}
	}
}

// baseModel 是一个 2 状态 HMM：动作 0 偏好长度 10，动作 1 偏好长度 30。
func baseModel(t *testing.T) *Compiled {
	t.Helper()
	m := &Model{
		Prior: []*big.Rat{rat(2, 5), rat(3, 5)},
		Transitions: [][]*big.Rat{
			{rat(3, 5), rat(2, 5)},
			{rat(1, 4), rat(3, 4)},
		},
		Emissions: []map[int]*big.Rat{
			{10: rat(3, 4), 30: rat(1, 4)},
			{10: rat(1, 4), 30: rat(3, 4)},
		},
	}
	c, err := NewModel(m)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	return c
}

// bruteJoint 枚举全部隐状态路径独立计算联合质量 mass[i]=P(X_T=i,obs)。
// 首时刻先验上只乘发射；之后每个时刻“当前状态发射 -> 转移到下一状态”。
// 丢失时刻发射因子为 1（对全部长度边缘化），未丢失时刻按填充块查表。
func bruteJoint(c *Compiled, seq []Observation) ([]*big.Rat, *big.Rat) {
	mass := make([]*big.Rat, c.n)
	for i := range mass {
		mass[i] = new(big.Rat)
	}
	if len(seq) == 0 {
		for i := range mass {
			mass[i].Set(c.prior[i])
		}
		total := new(big.Rat)
		for _, v := range mass {
			total.Add(total, v)
		}
		return mass, total
	}

	// 首时刻：在初始状态上发射。
	type path struct {
		state  int
		weight *big.Rat
	}
	var frontier []path
	first := seq[0]
	for i := 0; i < c.n; i++ {
		w := new(big.Rat).Set(c.prior[i])
		if !first.Lost {
			k := c.index[c.blockHead(first.Length)]
			w.Mul(w, c.emissions[i][k])
		}
		if w.Sign() > 0 {
			frontier = append(frontier, path{state: i, weight: w})
		}
	}

	// 之后每个时刻：当前状态发射，再转移。
	for t := 1; t < len(seq); t++ {
		obs := seq[t]
		var nxt []path
		for _, p := range frontier {
			emitted := p.weight
			if !obs.Lost {
				k := c.index[c.blockHead(obs.Length)]
				emitted = new(big.Rat).Mul(emitted, c.emissions[p.state][k])
				if emitted.Sign() == 0 {
					continue
				}
			}
			for j := 0; j < c.n; j++ {
				w := new(big.Rat).Mul(emitted, c.trans[p.state][j])
				if w.Sign() > 0 {
					nxt = append(nxt, path{state: j, weight: w})
				}
			}
		}
		frontier = nxt
	}
	for _, p := range frontier {
		mass[p.state].Add(mass[p.state], p.weight)
	}
	total := new(big.Rat)
	for _, v := range mass {
		total.Add(total, v)
	}
	return mass, total
}

func TestFilterMatchesBruteForce(t *testing.T) {
	c := baseModel(t)
	seqs := [][]Observation{
		{},
		{{Length: 10, Lost: false}},
		{{Length: 30, Lost: false}},
		{
			{Length: 10, Lost: false},
			{Length: 10, Lost: false},
			{Length: 30, Lost: false},
		},
		{
			{Lost: true},
			{Length: 10, Lost: false},
			{Lost: true},
		},
		{{Lost: true}, {Lost: true}},
	}

	for _, seq := range seqs {
		res, err := Filter(c, seq)
		if err != nil {
			t.Fatalf("Filter(%v): %v", seq, err)
		}
		mass, total := bruteJoint(c, seq)

		if res.Likelihood.Cmp(total) != 0 {
			t.Fatalf("seq=%v likelihood=%s want %s", seq, res.Likelihood, total)
		}
		if total.Sign() == 0 {
			if res.Possible || len(res.Posterior) != 0 || len(res.Compatible) != 0 {
				t.Fatalf("seq=%v impossible sequence misreported: %+v", seq, res)
			}
			continue
		}

		var wantCompat []int
		for i := 0; i < c.n; i++ {
			want := new(big.Rat).Quo(mass[i], total)
			if res.Posterior[i].Cmp(want) != 0 {
				t.Fatalf("seq=%v posterior[%d]=%s want %s", seq, i, res.Posterior[i], want)
			}
			if mass[i].Sign() > 0 {
				wantCompat = append(wantCompat, i)
			}
		}
		if len(res.Compatible) != len(wantCompat) {
			t.Fatalf("seq=%v compatible=%v want %v", seq, res.Compatible, wantCompat)
		}
		for k, idx := range wantCompat {
			if res.Compatible[k] != idx {
				t.Fatalf("seq=%v compatible order=%v want %v", seq, res.Compatible, wantCompat)
			}
		}

		wantMAP, wantVal := 0, new(big.Rat)
		for i := 0; i < c.n; i++ {
			p := new(big.Rat).Quo(mass[i], total)
			if p.Cmp(wantVal) > 0 {
				wantMAP, wantVal = i, p
			}
		}
		if res.MAP != wantMAP || res.MAPPosterior.Cmp(wantVal) != 0 {
			t.Fatalf("seq=%v MAP=(%d,%s) want (%d,%s)", seq, res.MAP, res.MAPPosterior, wantMAP, wantVal)
		}
	}
}

// TestPaddingSemantics 验证填充语义：块大小 16 时 0..15、16..31 分别聚合，
// 同一块内任意原始长度给出完全相同的后验，聚合概率等于手工求和。
func TestPaddingSemantics(t *testing.T) {
	m := &Model{
		BlockSize: 16,
		Prior:     []*big.Rat{rat(1, 2), rat(1, 2)},
		Transitions: [][]*big.Rat{
			{rat(3, 5), rat(2, 5)},
			{rat(1, 4), rat(3, 4)},
		},
		Emissions: []map[int]*big.Rat{
			{0: rat(1, 2), 7: rat(1, 4), 15: rat(1, 4), 16: rat(0, 1), 20: rat(0, 1)},
			{0: rat(0, 1), 10: rat(0, 1), 16: rat(1, 2), 23: rat(1, 3), 31: rat(1, 6)},
		},
	}
	c, err := NewModel(m)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	got := c.Alphabet()
	wantAlphabet := []int{0, 16}
	if len(got) != len(wantAlphabet) {
		t.Fatalf("alphabet=%v want %v", got, wantAlphabet)
	}
	for k, v := range wantAlphabet {
		if got[k] != v {
			t.Fatalf("alphabet=%v want %v", got, wantAlphabet)
		}
	}

	// 聚合后状态 0 在块 0 概率为 1，状态 1 在块 16 概率为 1。
	if c.emissions[0][0].Cmp(rat(1, 1)) != 0 || c.emissions[0][1].Sign() != 0 {
		t.Fatalf("state 0 padded emission wrong: %v", c.emissions[0])
	}
	if c.emissions[1][1].Cmp(rat(1, 1)) != 0 || c.emissions[1][0].Sign() != 0 {
		t.Fatalf("state 1 padded emission wrong: %v", c.emissions[1])
	}

	first, err := Filter(c, []Observation{{Length: 3, Lost: false}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	for _, length := range []int{0, 7, 15} {
		res, err := Filter(c, []Observation{{Length: length, Lost: false}})
		if err != nil {
			t.Fatalf("Filter length %d: %v", length, err)
		}
		assertSameResult(t, first, res)
		// 首步仅“先验*发射”：块 0 只由动作 0 发射，动作 1 质量为 0，
		// 因此动作 0 后验恰为 1，单步即确定性识别。
		if res.MAP != 0 || res.MAPPosterior.Cmp(rat(1, 1)) != 0 {
			t.Fatalf("length %d MAP posterior=%s want 1", length, res.MAPPosterior)
		}
	}
	second, err := Filter(c, []Observation{{Length: 28, Lost: false}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if second.MAP != 1 || second.MAPPosterior.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("block-16 length MAP posterior=%s want 1", second.MAPPosterior)
	}

	// 两个时刻后的后验与全路径枚举一致（第二步的转移会引入另一动作）。
	two, err := Filter(c, []Observation{{Length: 3, Lost: false}, {Length: 11, Lost: false}})
	if err != nil {
		t.Fatalf("Filter two-step: %v", err)
	}
	if two.MAP != 0 {
		t.Fatalf("two block-0 observations should keep action 0 as MAP")
	}

	// 在“先发射后转移”约定下，块0之后状态1质量为0，无法再发射块16；
	// 改用块0观察加丢失符号（丢失只推进转移），路径严格为正。
	seq := []Observation{{Length: 11, Lost: false}, {Lost: true}, {Length: 7, Lost: false}}
	res, err := Filter(c, seq)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	mass, total := bruteJoint(c, seq)
	if total.Sign() == 0 {
		t.Fatal("cross-check sequence unexpectedly impossible")
	}
	for i := 0; i < c.n; i++ {
		want := new(big.Rat).Quo(mass[i], total)
		if res.Posterior[i].Cmp(want) != 0 {
			t.Fatalf("padded seq posterior[%d]=%s want %s", i, res.Posterior[i], want)
		}
	}
}

// TestLossMarginalization 验证丢失与秘密独立：丢失时刻发射因子为 1
// （对全部长度边缘化）；首时刻丢失保持先验，之后丢失只做转移。
func TestLossMarginalization(t *testing.T) {
	c := baseModel(t)

	one, err := Filter(c, []Observation{{Lost: true}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	// 首时刻丢失：发射因子 1，且首时刻不做转移，后验即先验。
	for j := 0; j < c.n; j++ {
		want := c.prior[j]
		if one.Posterior[j].Cmp(want) != 0 {
			t.Fatalf("one-loss posterior[%d]=%s want %s", j, one.Posterior[j], want)
		}
	}
	if one.Likelihood.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("one-loss likelihood=%s want 1", one.Likelihood)
	}

	// 先观察长度 10 再丢失：第二时刻发射因子 1，仅按转移扩散。
	mid, err := Filter(c, []Observation{{Length: 10, Lost: false}, {Lost: true}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	first10, err := Filter(c, []Observation{{Length: 10, Lost: false}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	for j := 0; j < c.n; j++ {
		want := new(big.Rat)
		for i := 0; i < c.n; i++ {
			want.Add(want, new(big.Rat).Mul(first10.Posterior[i], c.trans[i][j]))
		}
		if mid.Posterior[j].Cmp(want) != 0 {
			t.Fatalf("emit-then-loss posterior[%d]=%s want %s", j, mid.Posterior[j], want)
		}
	}

	two, err := Filter(c, []Observation{{Lost: true}, {Lost: true}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if two.Likelihood.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("two-loss likelihood=%s want 1", two.Likelihood)
	}

	seq := []Observation{{Lost: true}, {Length: 10, Lost: false}, {Lost: true}}
	res, err := Filter(c, seq)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	mass, total := bruteJoint(c, seq)
	for i := 0; i < c.n; i++ {
		want := new(big.Rat).Quo(mass[i], total)
		if res.Posterior[i].Cmp(want) != 0 {
			t.Fatalf("loss-marg posterior[%d]=%s want %s", i, res.Posterior[i], want)
		}
	}
}

func TestEnumerateMatchesFilterAndOrder(t *testing.T) {
	c := baseModel(t)
	const depth = 3
	all, err := Enumerate(c, depth)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	symbols := c.symbols()
	base := len(symbols)
	wantCount := 0
	for d := 1; d <= depth; d++ {
		wantCount += powInt(base, d)
	}
	if len(all) != wantCount {
		t.Fatalf("enumerated %d sequences want %d", len(all), wantCount)
	}

	idx := 0
	for d := 1; d <= depth; d++ {
		count := powInt(base, d)
		for k := 0; k < count; k++ {
			got := all[idx]
			idx++
			if got.Depth != d || len(got.Observations) != d {
				t.Fatalf("entry depth mismatch: %+v", got)
			}
			// 期望字典序：把 k 写成 base 进制，每位映射到 symbols。
			code := k
			for pos := d - 1; pos >= 0; pos-- {
				digit := code % base
				code /= base
				if got.Observations[pos] != symbols[digit] {
					t.Fatalf("d=%d k=%d order mismatch at pos %d: %+v want %+v", d, k, pos, got.Observations, symbols[digit])
				}
			}
			direct, err := Filter(c, got.Observations)
			if err != nil {
				t.Fatalf("Filter: %v", err)
			}
			assertSameResult(t, direct, got.Result)
		}
	}
}

func TestShortest(t *testing.T) {
	c := baseModel(t)

	// 首步滤波为“先验*发射”后归一化。长度 10 的似然
	//   (2/5)(3/4)+(3/5)(1/4)=9/20，动作 0 后验=(3/10)/(9/20)=2/3。
	// 枚举顺序为 丢失、长度10、长度30，长度 10 先命中且恰好达到阈值。
	seq, err := Shortest(c, rat(2, 3))
	if err != nil {
		t.Fatalf("Shortest: %v", err)
	}
	if seq.Depth != 1 || seq.Observations[0].Lost || seq.Observations[0].Length != 10 {
		t.Fatalf("unexpected shortest sequence: %+v", seq)
	}
	if seq.Result.MAP != 0 || seq.Result.MAPPosterior.Cmp(rat(2, 3)) != 0 {
		t.Fatalf("unexpected shortest result: %+v", seq.Result)
	}

	// 阈值高于 151/220 时单步无命中；最短命中与 Enumerate 的首个命中一致。
	const higherNum, higherDen = 67, 100
	got, err := Shortest(c, big.NewRat(higherNum, higherDen))
	if err != nil {
		t.Fatalf("Shortest high threshold: %v", err)
	}
	all, enumErr := Enumerate(c, MaxDepth)
	if enumErr != nil {
		t.Fatalf("Enumerate: %v", enumErr)
	}
	var first *Sequence
	for k := range all {
		r := all[k].Result
		if r.Possible && r.MAPPosterior.Cmp(big.NewRat(higherNum, higherDen)) >= 0 {
			first = &all[k]
			break
		}
	}
	if first == nil {
		t.Fatal("enumeration found no hit for a threshold Shortest satisfied")
	}
	if got.Depth != first.Depth {
		t.Fatalf("depth mismatch: got=%d want=%d", got.Depth, first.Depth)
	}
	for k := range got.Observations {
		if got.Observations[k] != first.Observations[k] {
			t.Fatalf("sequence mismatch: got=%v want=%v", got.Observations, first.Observations)
		}
	}

	// 阈值 1 在该模型下无法达到（任何长度都有交叉发射），必须报 ErrNoSequence。
	if _, err := Shortest(c, rat(1, 1)); !errors.Is(err, ErrNoSequence) {
		t.Fatalf("want ErrNoSequence, got %v", err)
	}
}

func TestDepthGuardAndValidation(t *testing.T) {
	c := baseModel(t)

	deep := make([]Observation, MaxDepth+1)
	for i := range deep {
		deep[i] = Observation{Lost: true}
	}
	if _, err := Filter(c, deep); err == nil {
		t.Fatal("Filter must reject depth beyond MaxDepth")
	}
	if _, err := Enumerate(c, MaxDepth+1); err == nil {
		t.Fatal("Enumerate must reject depth beyond MaxDepth")
	}
	if _, err := Enumerate(c, 0); err == nil {
		t.Fatal("Enumerate must reject zero depth")
	}

	if _, err := Filter(c, []Observation{{Length: 99, Lost: false}}); err == nil {
		t.Fatal("Filter must reject alphabet-outside length")
	}

	bad := &Model{
		Prior:       []*big.Rat{rat(1, 2), rat(1, 3)},
		Transitions: [][]*big.Rat{{rat(1, 1), rat(0, 1)}, {rat(0, 1), rat(1, 1)}},
		Emissions: []map[int]*big.Rat{
			{10: rat(1, 1)},
			{10: rat(1, 1)},
		},
	}
	if _, err := NewModel(bad); err == nil {
		t.Fatal("NewModel must reject non-normalized prior")
	}

	badTrans := &Model{
		Prior:       []*big.Rat{rat(1, 2), rat(1, 2)},
		Transitions: [][]*big.Rat{{rat(1, 2), rat(1, 3)}, {rat(0, 1), rat(1, 1)}},
		Emissions: []map[int]*big.Rat{
			{10: rat(1, 1)},
			{10: rat(1, 1)},
		},
	}
	if _, err := NewModel(badTrans); err == nil {
		t.Fatal("NewModel must reject non-normalized transition row")
	}

	if _, err := Shortest(c, big.NewRat(3, 2)); err == nil {
		t.Fatal("Shortest must reject threshold > 1")
	}
}

func TestImpossibleAndEmptySequences(t *testing.T) {
	// 状态 0 只能发射长度 10，状态 1 只能发射长度 30：观察 30 后
	// 状态 0 质量为 0；再观察 10 时序列不可能发生。
	m := &Model{
		Prior: []*big.Rat{rat(1, 2), rat(1, 2)},
		Transitions: [][]*big.Rat{
			{rat(1, 1), rat(0, 1)},
			{rat(0, 1), rat(1, 1)},
		},
		Emissions: []map[int]*big.Rat{
			{10: rat(1, 1)},
			{30: rat(1, 1)},
		},
	}
	c, err := NewModel(m)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	empty, err := Filter(c, nil)
	if err != nil {
		t.Fatalf("Filter empty: %v", err)
	}
	if !empty.Possible || empty.MAPPosterior.Cmp(rat(1, 2)) != 0 {
		t.Fatalf("empty sequence should return prior: %+v", empty)
	}

	impossible := []Observation{{Length: 30, Lost: false}, {Length: 10, Lost: false}}
	res, err := Filter(c, impossible)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if res.Possible || res.Likelihood.Sign() != 0 || len(res.Posterior) != 0 || len(res.Compatible) != 0 {
		t.Fatalf("impossible sequence misreported: %+v", res)
	}

	// 枚举中的同序列也必须标记为不可能，且不被 Shortest 当作命中。
	all, err := Enumerate(c, 2)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	found := false
	for _, s := range all {
		if len(s.Observations) == 2 &&
			s.Observations[0].Length == 30 && !s.Observations[0].Lost &&
			s.Observations[1].Length == 10 && !s.Observations[1].Lost {
			found = true
			if s.Result.Possible {
				t.Fatal("enumerated impossible sequence marked possible")
			}
		}
	}
	if !found {
		t.Fatal("enumeration did not contain the target impossible sequence")
	}

	// 阈值 1 在此确定性模型下由单步发射达到（动作被唯一确定）。
	hit, err := Shortest(c, rat(1, 1))
	if err != nil {
		t.Fatalf("Shortest certain model: %v", err)
	}
	if hit.Depth != 1 || hit.Result.MAPPosterior.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("unexpected certain hit: %+v", hit)
	}
}
