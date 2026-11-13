package netsec

import (
	"math/big"
	"math/rand"
	"testing"
)

// hasCandidate 判断候选集合是否包含给定 H。
func hasCandidate(cands []Candidate, h *big.Int) bool {
	for _, c := range cands {
		if c.H.Cmp(h) == 0 {
			return true
		}
	}
	return false
}

// 128 位主流程：本地已知 H、S 仅用于合成观测与核对，
// 求根器与伪造器只接触消息和标签。
func TestForger128UniqueRecovery(t *testing.T) {
	f := Field128()
	h, _ := new(big.Int).SetString("deadbeefcafe1234567890abcdef42", 16)
	s, _ := new(big.Int).SetString("0f1e2d3c4b5a69788796a5b4c3d2e1f0", 16)

	m1 := []*big.Int{f.FromUint(0x11)}
	m2 := []*big.Int{f.FromUint(0x22)}
	g := f.NewForger()
	if st, err := g.AddObservation(m1, f.Tag(h, s, m1)); err != nil || st != StatusInsufficient {
		t.Fatalf("首条观测后应为信息不足，got=%v err=%v", st, err)
	}
	// 重复同一观测：零多项式，信息仍然不足。
	if st, err := g.AddObservation(m1, f.Tag(h, s, m1)); err != nil || st != StatusInsufficient {
		t.Fatalf("重复观测后应仍为信息不足，got=%v err=%v", st, err)
	}
	st, err := g.AddObservation(m2, f.Tag(h, s, m2))
	if err != nil || st != StatusDetermined {
		t.Fatalf("两条不同单元素消息应唯一确定候选，got=%v err=%v", st, err)
	}
	cands := g.Candidates()
	if len(cands) != 1 || cands[0].H.Cmp(h) != 0 || cands[0].S.Cmp(s) != 0 {
		t.Fatalf("候选应为唯一的 (H,S)，got=%+v", cands)
	}
	// 伪造新消息：确定伪造且标签与真实密钥材料一致。
	fresh := []*big.Int{f.FromUint(0xaa), f.FromUint(0xbb), f.FromUint(0xcc)}
	tags, fst, err := g.Forge(fresh)
	if err != nil || fst != StatusDetermined || len(tags) != 1 {
		t.Fatalf("应为确定伪造，got=%v tags=%d err=%v", fst, len(tags), err)
	}
	if want := f.Tag(h, s, fresh); tags[0].Tag.Cmp(want) != 0 {
		t.Fatalf("伪造标签错误 got=%x want=%x", tags[0].Tag, want)
	}
}

// 多候选路径：两条观测留下 4 个根，伪造需更多观测；追加观测后过滤。
func TestForger128NeedMoreThenFilter(t *testing.T) {
	f := Field128()
	h, _ := new(big.Int).SetString("1234567890abcdef1234567890abcdef", 16)
	s, _ := new(big.Int).SetString("fedcba0987654321fedcba0987654321", 16)

	// Q([1,0,0]) = X^4+3X，Q([0,0]) = 2X，约束为 X^4+X+(H^4+H)，
	// 其根恰为 H+GF(4)，共 4 个候选。
	m1 := []*big.Int{f.One(), f.Zero(), f.Zero()}
	m2 := []*big.Int{f.Zero(), f.Zero()}
	g := f.NewForger()
	g.AddObservation(m1, f.Tag(h, s, m1))
	st, err := g.AddObservation(m2, f.Tag(h, s, m2))
	if err != nil || st != StatusNeedMore {
		t.Fatalf("应为需更多观测，got=%v err=%v", st, err)
	}
	cands := g.Candidates()
	if len(cands) != 4 || !hasCandidate(cands, h) {
		t.Fatalf("应有 4 个候选且包含真实 H，got=%d", len(cands))
	}
	fresh := []*big.Int{f.FromUint(7)}
	tags, fst, err := g.Forge(fresh)
	if err != nil || fst != StatusNeedMore || len(tags) != 4 {
		t.Fatalf("多候选且标签不一致应为需更多观测，got=%v tags=%d err=%v", fst, len(tags), err)
	}
	// 追加第三条同 nonce 观测，过滤候选根。
	m3 := []*big.Int{f.FromUint(0x99), f.FromUint(0x42)}
	st, err = g.AddObservation(m3, f.Tag(h, s, m3))
	if err != nil {
		t.Fatal(err)
	}
	cands = g.Candidates()
	if len(cands) >= 4 || !hasCandidate(cands, h) {
		t.Fatalf("追加观测后候选应收缩且仍含真实 H，got=%d st=%v", len(cands), st)
	}
	if len(cands) == 1 && st != StatusDetermined {
		t.Fatalf("候选唯一时应为确定伪造，got=%v", st)
	}
}

// 不一致观测：同一消息给出不同标签，得到常数非零约束。
func TestForger128Inconsistent(t *testing.T) {
	f := Field128()
	h := f.FromUint(0x1234)
	s := f.FromUint(0xabcd)
	m := []*big.Int{f.FromUint(5)}
	g := f.NewForger()
	g.AddObservation(m, f.Tag(h, s, m))
	bad := f.Add(f.Tag(h, s, m), f.One())
	st, err := g.AddObservation(m, bad)
	if err != nil || st != StatusInconsistent {
		t.Fatalf("同消息异标签应为观测不一致，got=%v err=%v", st, err)
	}
	if _, fst, _ := g.Forge([]*big.Int{f.One()}); fst != StatusInconsistent {
		t.Fatalf("不一致状态下不可伪造，got=%v", fst)
	}
}

// H=0 边界：候选根含 0 时恢复与伪造均正常。
func TestForger128ZeroH(t *testing.T) {
	f := Field128()
	h := f.Zero()
	s, _ := new(big.Int).SetString("0123456789abcdeffedcba9876543210", 16)
	m1 := []*big.Int{f.FromUint(3)}
	m2 := []*big.Int{f.FromUint(9)}
	g := f.NewForger()
	g.AddObservation(m1, f.Tag(h, s, m1))
	st, _ := g.AddObservation(m2, f.Tag(h, s, m2))
	if st != StatusDetermined {
		t.Fatalf("H=0 应被唯一恢复，got=%v", st)
	}
	c := g.Candidates()[0]
	if c.H.Sign() != 0 || c.S.Cmp(s) != 0 {
		t.Fatalf("H=0 恢复错误 got=(%x,%x)", c.H, c.S)
	}
}

// 8 位小域：穷举全部 H，复用同一路径验证恢复与伪造。
func TestForger8ExhaustiveH(t *testing.T) {
	f := Field8()
	rng := rand.New(rand.NewSource(4))
	for hi := 0; hi < 256; hi++ {
		h := f.FromUint(uint64(hi))
		s := f.FromUint(uint64(rng.Intn(256)))
		m1 := []*big.Int{f.FromUint(uint64(rng.Intn(256)))}
		var m2 []*big.Int
		for {
			m2 = []*big.Int{f.FromUint(uint64(rng.Intn(256)))}
			if m2[0].Cmp(m1[0]) != 0 {
				break
			}
		}
		g := f.NewForger()
		g.AddObservation(m1, f.Tag(h, s, m1))
		st, err := g.AddObservation(m2, f.Tag(h, s, m2))
		if err != nil || st != StatusDetermined {
			t.Fatalf("H=%d: 应唯一确定，got=%v err=%v", hi, st, err)
		}
		c := g.Candidates()[0]
		if c.H.Cmp(h) != 0 || c.S.Cmp(s) != 0 {
			t.Fatalf("H=%d: 恢复错误 got=(%x,%x)", hi, c.H, c.S)
		}
		fresh := []*big.Int{f.FromUint(uint64(rng.Intn(256))), f.FromUint(uint64(rng.Intn(256)))}
		tags, fst, _ := g.Forge(fresh)
		if fst != StatusDetermined || tags[0].Tag.Cmp(f.Tag(h, s, fresh)) != 0 {
			t.Fatalf("H=%d: 伪造标签错误", hi)
		}
	}
}
