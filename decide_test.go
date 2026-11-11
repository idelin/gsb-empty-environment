package netsec

import (
	"math/rand"
	"slices"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Combiner {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) unexpected error: %v", cfg, err)
	}
	return c
}

func TestNewValidation(t *testing.T) {
	overAllow := make([]int, 15)
	for i := range overAllow {
		overAllow[i] = i
	}
	overDeny := make([]int, 6)
	for i := range overDeny {
		overDeny[i] = 15 + i
	}

	cases := []struct {
		name string
		cfg  Config
		err  error
	}{
		{"too many detectors", Config{
			AllowGroup:     overAllow,
			DenyGroup:      overDeny,
			AllowThreshold: 1,
		}, ErrTooManyDetectors},
		{"empty deny group", Config{
			AllowGroup:     []int{0, 1},
			DenyGroup:      nil,
			AllowThreshold: 1,
		}, ErrEmptyGroup},
		{"threshold zero", Config{
			AllowGroup:     []int{0, 1},
			DenyGroup:      []int{2},
			AllowThreshold: 0,
		}, ErrBadThreshold},
		{"threshold too large", Config{
			AllowGroup:     []int{0, 1},
			DenyGroup:      []int{2},
			AllowThreshold: 3,
		}, ErrBadThreshold},
		{"duplicate id", Config{
			AllowGroup:     []int{0, 1},
			DenyGroup:      []int{0},
			AllowThreshold: 1,
		}, ErrUnknownDetector},
		{"missing id", Config{
			AllowGroup:     []int{0, 2},
			DenyGroup:      []int{3},
			AllowThreshold: 1,
		}, ErrUnknownDetector},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err != tc.err {
				t.Fatalf("New() error = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestDenyHitOverridesFastAllow(t *testing.T) {
	// 允许组 0,1（阈值 2），拒绝组 2,3。允许先凑齐，但拒绝组尚未结束，
	// 此时不能放行；随后关键拒绝返回，结论锁定为拒绝。
	cfg := Config{
		AllowGroup:     []int{0, 1},
		DenyGroup:      []int{2, 3},
		AllowThreshold: 2,
	}
	c := mustNew(t, cfg)

	if err := c.Submit(0, ProvenAllow); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(1, ProvenAllow); err != nil {
		t.Fatal(err)
	}

	st := c.Evaluate()
	if st.Verdict != Pending {
		t.Fatalf("after fast allow, verdict = %s, want pending (deny branch still open)", st.Verdict)
	}
	if !slices.Equal(st.Relevant, []int{2, 3}) {
		t.Fatalf("relevant = %v, want [2 3]", st.Relevant)
	}
	if len(st.Irrelevant) != 0 {
		t.Fatalf("irrelevant = %v, want empty", st.Irrelevant)
	}

	if err := c.Submit(2, ProvenDeny); err != nil {
		t.Fatal(err)
	}
	st = c.Evaluate()
	if st.Verdict != Deny || st.Reason != DenyHit {
		t.Fatalf("verdict = %s/%s, want deny/deny-hit", st.Verdict, st.Reason)
	}
	if !slices.Equal(st.Evidence, []int{2}) {
		t.Fatalf("evidence = %v, want [2]", st.Evidence)
	}
	if !slices.Equal(st.Irrelevant, []int{3}) {
		t.Fatalf("irrelevant = %v, want remaining detector [3]", st.Irrelevant)
	}

	// 被取消的检测器再提交结果必须被拒绝，迟到的允许无法翻案。
	if err := c.Submit(3, ProvenAllow); err != ErrCancelledDetector {
		t.Fatalf("submit after cancel error = %v, want ErrCancelledDetector", err)
	}
	again := c.Evaluate()
	if again.Verdict != Deny {
		t.Fatalf("re-evaluate verdict = %s, want deny", again.Verdict)
	}

	if err := VerifyInvariant(cfg, c.Snapshot(), Deny); err != nil {
		t.Fatalf("VerifyInvariant: %v", err)
	}
}

func TestAllowAfterAllDenyBranchesClose(t *testing.T) {
	cfg := Config{
		AllowGroup:     []int{0, 1, 2},
		DenyGroup:      []int{3, 4},
		AllowThreshold: 2,
	}
	c := mustNew(t, cfg)

	for _, id := range []int{0, 1, 3} {
		if err := c.Submit(id, ProvenAllow); err != nil {
			t.Fatal(err)
		}
	}
	// 阈值已够、一个拒绝分支仍在跑：不能定案，且允许组剩余分支已无关。
	st := c.Evaluate()
	if st.Verdict != Pending {
		t.Fatalf("verdict = %s, want pending", st.Verdict)
	}
	if !slices.Equal(st.Relevant, []int{4}) {
		t.Fatalf("relevant = %v, want [4]", st.Relevant)
	}
	if !slices.Equal(st.Irrelevant, []int{2}) {
		t.Fatalf("irrelevant = %v, want [2]", st.Irrelevant)
	}

	if err := c.Submit(4, Unknown); err != nil {
		t.Fatal(err)
	}
	st = c.Evaluate()
	if st.Verdict != Allow {
		t.Fatalf("verdict = %s, want allow", st.Verdict)
	}
	// 最小证据：两张允许票 + 已结束且未拒绝的拒绝组检测器。
	if !slices.Equal(st.Evidence, []int{0, 1, 3, 4}) {
		t.Fatalf("evidence = %v, want [0 1 3 4]", st.Evidence)
	}
	if err := VerifyEvidence(cfg, c.Snapshot(), st); err != nil {
		t.Fatalf("VerifyEvidence: %v", err)
	}
}

func TestAllowUnreachableFailClosed(t *testing.T) {
	// 允许组 3 个、阈值 2；两个允许组检测器已证明不允许，第三票无论如何都凑不齐。
	cfg := Config{
		AllowGroup:     []int{0, 1, 2},
		DenyGroup:      []int{3, 4},
		AllowThreshold: 2,
	}
	c := mustNew(t, cfg)

	if err := c.Submit(0, ProvenDeny); err != nil { // 允许组检测器给出非允许证明
		t.Fatal(err)
	}
	if err := c.Submit(1, Unknown); err != nil {
		t.Fatal(err)
	}

	st := c.Evaluate()
	if st.Verdict != Deny || st.Reason != AllowUnreachable {
		t.Fatalf("verdict = %s/%s, want deny/allow-unreachable", st.Verdict, st.Reason)
	}
	if !slices.Equal(st.Evidence, []int{0, 1}) {
		t.Fatalf("evidence = %v, want two non-allow witnesses [0 1]", st.Evidence)
	}
	// 全部剩余分支（含整个拒绝组）都已无关，应被取消。
	if !slices.Equal(st.Irrelevant, []int{2, 3, 4}) {
		t.Fatalf("irrelevant = %v, want [2 3 4]", st.Irrelevant)
	}

	if err := VerifyEvidence(cfg, c.Snapshot(), st); err != nil {
		t.Fatalf("VerifyEvidence: %v", err)
	}
}

func TestSubmitErrors(t *testing.T) {
	c := mustNew(t, Config{
		AllowGroup:     []int{0},
		DenyGroup:      []int{1},
		AllowThreshold: 1,
	})
	if err := c.Submit(7, ProvenAllow); err != ErrUnknownDetector {
		t.Fatalf("unknown id error = %v", err)
	}
	if err := c.Submit(0, Outcome(42)); err != ErrBadOutcome {
		t.Fatalf("bad outcome error = %v", err)
	}
	if err := c.Submit(0, ProvenAllow); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(0, ProvenAllow); err != ErrCancelledDetector {
		t.Fatalf("double submit error = %v, want ErrCancelledDetector", err)
	}
}

// bruteForceVerdicts 独立穷举全部未结束检测器的补全，返回可能最终结论的集合。
func bruteForceVerdicts(snap Snapshot, inAllow, inDeny []bool, threshold int) map[Verdict]bool {
	n := len(snap.Outcome)
	pending := make([]int, 0)
	for id := 0; id < n; id++ {
		if !snap.Finished[id] {
			pending = append(pending, id)
		}
	}

	results := map[Verdict]bool{}
	outcomes := slices.Clone(snap.Outcome)
	for mask := 0; mask < 1<<len(pending); mask++ {
		for bit, id := range pending {
			if mask&(1<<bit) != 0 {
				if inDeny[id] {
					outcomes[id] = ProvenDeny
				} else {
					outcomes[id] = ProvenAllow
				}
			} else {
				outcomes[id] = Unknown
			}
		}
		results[finalVerdict(outcomes, inAllow, inDeny, threshold)] = true
	}
	return results
}

func TestExhaustiveRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))

	for trial := 0; trial < 400; trial++ {
		n := 2 + rng.Intn(MaxDetectors-1) // 2..20
		ids := rng.Perm(n)
		split := 1 + rng.Intn(n-1)
		cfg := Config{
			AllowGroup:     ids[:split],
			DenyGroup:      ids[split:],
			AllowThreshold: 1 + rng.Intn(split),
		}
		inAllow, inDeny, _ := membership(cfg)

		c := mustNew(t, cfg)
		order := rng.Perm(n)
		submitted := rng.Intn(n + 1) // 随机停在任意进度
		for _, id := range order[:submitted] {
			var result Outcome
			switch rng.Intn(3) {
			case 0:
				result = Unknown
			case 1:
				result = ProvenAllow
			case 2:
				result = ProvenDeny
			}
			if err := c.Submit(id, result); err != nil {
				t.Fatalf("trial %d: submit %d: %v", trial, id, err)
			}
		}

		snap := c.Snapshot()
		st, err := VerifyEarlyStop(cfg, snap)
		if err != nil {
			t.Fatalf("trial %d cfg=%+v: VerifyEarlyStop: %v", trial, cfg, err)
		}

		possible := bruteForceVerdicts(snap, inAllow, inDeny, cfg.AllowThreshold)
		switch len(possible) {
		case 1:
			var want Verdict
			for v := range possible {
				want = v
			}
			if st.Verdict != want {
				t.Fatalf("trial %d: verdict %s, brute force says locked %s", trial, st.Verdict, want)
			}
			if len(st.Relevant) != 0 {
				t.Fatalf("trial %d: locked verdict but relevant = %v", trial, st.Relevant)
			}
		default:
			if st.Verdict != Pending {
				t.Fatalf("trial %d: verdict %s but completions can yield multiple results", trial, st.Verdict)
			}
		}

		// 纯函数路径与组合器路径必须一致。
		direct := evaluate(snap, inAllow, inDeny, cfg.AllowThreshold)
		if direct.Verdict != st.Verdict || direct.Reason != st.Reason ||
			!slices.Equal(direct.Evidence, st.Evidence) {
			t.Fatalf("trial %d: VerifyEarlyStop status %+v != pure evaluate %+v", trial, st, direct)
		}
	}
}

func TestVerifyInvariantRejectsUnsoundEarlyStop(t *testing.T) {
	cfg := Config{
		AllowGroup:     []int{0, 1},
		DenyGroup:      []int{2},
		AllowThreshold: 1,
	}
	// 没有任何检测器结束：声称提前定案显然不成立，因为补全可以得到允许。
	snap := Snapshot{
		Outcome:  []Outcome{Unknown, Unknown, Unknown},
		Finished: []bool{false, false, false},
	}
	if err := VerifyInvariant(cfg, snap, Deny); err == nil {
		t.Fatal("VerifyInvariant should reject unsound early deny")
	}
	if err := VerifyInvariant(cfg, snap, Allow); err == nil {
		t.Fatal("VerifyInvariant should reject unsound early allow")
	}

	// 真实锁定的拒绝（拒绝组已命中）通过核对。
	locked := Snapshot{
		Outcome:  []Outcome{Unknown, Unknown, ProvenDeny},
		Finished: []bool{false, false, true},
	}
	if err := VerifyInvariant(cfg, locked, Deny); err != nil {
		t.Fatalf("locked deny should verify: %v", err)
	}
}

func TestMaxDetectorsInvariantCost(t *testing.T) {
	allow := make([]int, 10)
	deny := make([]int, 10)
	for i := 0; i < 10; i++ {
		allow[i] = i
		deny[i] = i + 10
	}
	cfg := Config{AllowGroup: allow, DenyGroup: deny, AllowThreshold: 10}
	c := mustNew(t, cfg)

	// 9 个允许票 + 全部拒绝组结束未命中：最后一个允许检测器是唯一相关分支。
	for i := 0; i < 9; i++ {
		if err := c.Submit(i, ProvenAllow); err != nil {
			t.Fatal(err)
		}
	}
	for i := 10; i < 20; i++ {
		if err := c.Submit(i, Unknown); err != nil {
			t.Fatal(err)
		}
	}

	st, err := VerifyEarlyStop(cfg, c.Snapshot())
	if err != nil {
		t.Fatalf("VerifyEarlyStop: %v", err)
	}
	if st.Verdict != Pending || !slices.Equal(st.Relevant, []int{9}) {
		t.Fatalf("status = %+v, want pending with only detector 9 relevant", st)
	}

	if err := c.Submit(9, ProvenAllow); err != nil {
		t.Fatal(err)
	}
	st = c.Evaluate()
	if st.Verdict != Allow {
		t.Fatalf("verdict = %s, want allow", st.Verdict)
	}
	if len(st.Evidence) != 20 {
		t.Fatalf("allow evidence = %v, want all 20 finished detectors", st.Evidence)
	}
	if _, err := VerifyEarlyStop(cfg, c.Snapshot()); err != nil {
		t.Fatalf("final VerifyEarlyStop: %v", err)
	}
}

func TestConcurrentSubmitAndEvaluate(t *testing.T) {
	cfg := Config{
		AllowGroup:     []int{0, 1, 2, 3},
		DenyGroup:      []int{4, 5, 6, 7},
		AllowThreshold: 3,
	}
	c := mustNew(t, cfg)

	var wg sync.WaitGroup
	for id := 0; id < 8; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			result := ProvenAllow
			if id == 6 {
				result = ProvenDeny
			}
			_ = c.Submit(id, result) // Evaluate 可能已取消该检测器，忽略错误
		}(id)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Evaluate()
		}()
	}
	wg.Wait()

	// 检测器 6 的拒绝命中是最终事实，无论调度顺序如何结论都必须是拒绝。
	st := c.Evaluate()
	if st.Verdict != Deny || st.Reason != DenyHit {
		t.Fatalf("final verdict = %s/%s, want deny/deny-hit", st.Verdict, st.Reason)
	}
	if err := VerifyInvariant(cfg, c.Snapshot(), Deny); err != nil {
		t.Fatalf("VerifyInvariant: %v", err)
	}
}
