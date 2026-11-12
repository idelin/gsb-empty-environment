package padplan

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

func objects(ids []string, lengths []int64) []ObjectLen {
	out := make([]ObjectLen, len(ids))
	for i := range ids {
		out[i] = ObjectLen{ID: ids[i], Length: lengths[i]}
	}
	return out
}

func assertPlan(t *testing.T, in Input, p *Plan, wantDistinct int, wantPadding int64) {
	t.Helper()
	if p.DistinctBuckets != wantDistinct {
		t.Fatalf("distinct buckets = %d, want %d", p.DistinctBuckets, wantDistinct)
	}
	if p.TotalPadding != wantPadding {
		t.Fatalf("total padding = %d, want %d", p.TotalPadding, wantPadding)
	}
	if len(p.Assignments) != len(in.Objects) {
		t.Fatalf("assignments = %d, want %d", len(p.Assignments), len(in.Objects))
	}

	byID := make(map[string]Assignment, len(p.Assignments))
	var sum int64
	used := make(map[int64]struct{})
	for i, a := range p.Assignments {
		if a.ObjectID != in.Objects[i].ID {
			t.Fatalf("assignment %d id = %q, want %q", i, a.ObjectID, in.Objects[i].ID)
		}
		byID[a.ObjectID] = a
		if a.Length != in.Objects[i].Length {
			t.Fatalf("assignment %d length mutated: got %d", i, a.Length)
		}
		if a.Bucket < a.Length {
			t.Fatalf("bucket %d smaller than length %d for %q", a.Bucket, a.Length, a.ObjectID)
		}
		if a.Padding != a.Bucket-a.Length {
			t.Fatalf("padding %d != bucket %d - length %d", a.Padding, a.Bucket, a.Length)
		}
		sum += a.Padding
		used[a.Bucket] = struct{}{}
	}
	if sum != p.TotalPadding {
		t.Fatalf("sum of per-object padding %d != TotalPadding %d", sum, p.TotalPadding)
	}
	if len(used) != p.DistinctBuckets {
		t.Fatalf("distinct used buckets %d != reported %d", len(used), p.DistinctBuckets)
	}
	if p.TotalPadding > in.MaxExtraBytes {
		t.Fatalf("padding %d exceeds budget %d", p.TotalPadding, in.MaxExtraBytes)
	}
	if p.Evidence.TotalPadding != p.TotalPadding ||
		p.Evidence.Budget != in.MaxExtraBytes ||
		p.Evidence.BudgetHeadroom != in.MaxExtraBytes-p.TotalPadding ||
		p.Evidence.DistinctBuckets != p.DistinctBuckets {
		t.Fatalf("evidence figures inconsistent with plan: %+v", p.Evidence)
	}
	if p.Evidence.FeasibleLeaves == 0 || p.Evidence.NodesVisited == 0 {
		t.Fatalf("enumeration counters not populated: %+v", p.Evidence)
	}
	wantSpace := new(big.Int).Exp(
		big.NewInt(int64(len(in.BucketSizes))),
		big.NewInt(int64(len(in.Objects))), nil)
	if p.Evidence.AssignmentSpace.Cmp(wantSpace) != 0 {
		t.Fatalf("assignment space = %s, want %s", p.Evidence.AssignmentSpace, wantSpace)
	}

	// 规模较小时，额外用独立暴力枚举做第三方交叉核对。
	if len(in.Objects)*len(in.BucketSizes) <= 40 {
		bfDistinct, bfPadding, feasible := bruteForceOptimum(in)
		if !feasible {
			t.Fatal("brute force found no feasible assignment although planner did")
		}
		if bfDistinct != p.DistinctBuckets || bfPadding != p.TotalPadding {
			t.Fatalf("brute force optimum (%d, %d) != plan (%d, %d)",
				bfDistinct, bfPadding, p.DistinctBuckets, p.TotalPadding)
		}
	}
}

// bruteForceOptimum 独立枚举所有 k^n 个分配，按相同字典序求最优，
// 作为规划器与内置核对器之外的第三方参照。
func bruteForceOptimum(in Input) (bestDistinct int, bestPadding int64, ok bool) {
	n := len(in.Objects)
	k := len(in.BucketSizes)
	choice := make([]int, n)
	bestDistinct = n + 1
	bestPadding = math.MaxInt64

	for {
		var padding int64
		used := make(map[int64]struct{}, k)
		feasible := true
		for i, obj := range in.Objects {
			bucket := in.BucketSizes[choice[i]]
			if bucket < obj.Length {
				feasible = false
				break
			}
			padding += bucket - obj.Length
			if padding > in.MaxExtraBytes {
				feasible = false
				break
			}
			used[bucket] = struct{}{}
		}
		if feasible {
			distinct := len(used)
			if distinct < bestDistinct || (distinct == bestDistinct && padding < bestPadding) {
				bestDistinct = distinct
				bestPadding = padding
				ok = true
			}
		}

		pos := n - 1
		for pos >= 0 {
			choice[pos]++
			if choice[pos] < k {
				break
			}
			choice[pos] = 0
			pos--
		}
		if pos < 0 {
			return bestDistinct, bestPadding, ok
		}
	}
}

func TestBasicSharing(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"a", "b", "c", "d"}, []int64{10, 12, 30, 31}),
		BucketSizes:   []int64{16, 32, 64},
		MaxExtraBytes: 1000,
	}
	p, err := PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	// 1 种桶时只能选能容纳最长对象 31 的桶：32 或 64。
	// 32 对四对象的填充为 22+20+2+1=45，优于 64 的 173。
	assertPlan(t, in, p, 1, 45)
	if len(p.BucketsUsed) != 1 || p.BucketsUsed[0] != 32 {
		t.Fatalf("buckets used = %v", p.BucketsUsed)
	}

	// 禁用 64 后 32 仍可单独容纳全部对象（1 种桶、填充 45）；
	// 要观察到 2 种桶的权衡，需让预算拒绝 45：13 恰好只够 16+32 方案。
	in.BucketSizes = []int64{16, 32}
	in.MaxExtraBytes = 13
	p, err = PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	assertPlan(t, in, p, 2, 13)
}

func TestBucketCountBeforePadding(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"a", "b", "c"}, []int64{1, 2, 3}),
		BucketSizes:   []int64{4, 8, 16},
		MaxExtraBytes: 7, // 共用桶 4 的填充为 3+2+1=6，可行但需 1 种桶；
	}
	p, err := PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	// 预算允许共用桶 4 → 1 种可观察桶、6 字节填充为字典序最优。
	assertPlan(t, in, p, 1, 6)

	// 收紧预算至 5 后共用方案不可行，只能逐桶分配：4/8/16，3 种桶、填充 22。
	in.MaxExtraBytes = 5
	if _, err := PlanBuckets(in); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}

	// 预算 22 时可行的最少桶数为 3，且填充恰为 22。
	in.MaxExtraBytes = 22
	p, err = PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	// 2 种桶方案 {4,16}：a→4(+3)、b→16(+14)、c→16(+13)，填充 30 超预算；
	// {8,16} 同理 32；故 22 预算下仅 3 种桶方案可行。
	bfD, bfP, ok := bruteForceOptimum(in)
	if !ok {
		t.Fatal("brute force: no feasible assignment")
	}
	assertPlan(t, in, p, bfD, bfP)
}

func TestMinimumPaddingWithinDistinctCount(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"a", "b"}, []int64{10, 20}),
		BucketSizes:   []int64{16, 32, 64},
		MaxExtraBytes: 1000,
	}
	p, err := PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	// 1 种桶时 32 即可容纳两对象，填充 22+12=34，优于 64 的 98。
	assertPlan(t, in, p, 1, 34)
	// 禁用 64 且预算拒绝 34 后，只剩 16+32 的 2 种桶方案，填充 18。
	in.BucketSizes = []int64{16, 32}
	in.MaxExtraBytes = 18
	p, err = PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	assertPlan(t, in, p, 2, 18)
}

func TestZeroLengthObject(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"z", "a"}, []int64{0, 10}),
		BucketSizes:   []int64{16, 32},
		MaxExtraBytes: 0,
	}
	if _, err := PlanBuckets(in); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}

	in.MaxExtraBytes = 22 // 共用 16：16 + 6 = 22
	p, err := PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	assertPlan(t, in, p, 1, 22)
}

func TestBudgetExceeded(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"a", "b"}, []int64{10, 20}),
		BucketSizes:   []int64{16, 32},
		MaxExtraBytes: 5, // 最小可行填充 6+12=18。
	}
	p, err := PlanBuckets(in)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if p != nil {
		t.Fatal("plan must be nil on failure")
	}
}

func TestNoFeasibleBucket(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"a"}, []int64{100}),
		BucketSizes:   []int64{16, 32},
		MaxExtraBytes: 1000,
	}
	_, err := PlanBuckets(in)
	if !errors.Is(err, ErrNoFeasibleBucket) {
		t.Fatalf("err = %v, want ErrNoFeasibleBucket", err)
	}
}

func TestValidationFailures(t *testing.T) {
	base := Input{
		Objects:       objects([]string{"a"}, []int64{1}),
		BucketSizes:   []int64{16},
		MaxExtraBytes: 10,
	}
	cases := []struct {
		name string
		mut  func(*Input)
		want error
	}{
		{"no objects", func(in *Input) { in.Objects = nil }, ErrInvalidInput},
		{"empty id", func(in *Input) { in.Objects[0].ID = "" }, ErrInvalidInput},
		{"negative length", func(in *Input) { in.Objects[0].Length = -1 }, ErrInvalidInput},
		{"duplicate id", func(in *Input) {
			in.Objects = append(in.Objects, ObjectLen{ID: "a", Length: 2})
		}, ErrInvalidInput},
		{"no buckets", func(in *Input) { in.BucketSizes = nil }, ErrInvalidInput},
		{"non positive bucket", func(in *Input) { in.BucketSizes = []int64{0} }, ErrInvalidInput},
		{"unsorted buckets", func(in *Input) { in.BucketSizes = []int64{32, 16} }, ErrInvalidInput},
		{"negative budget", func(in *Input) { in.MaxExtraBytes = -1 }, ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mut(&in)
			_, err := PlanBuckets(in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTooManyObjects(t *testing.T) {
	objs := make([]ObjectLen, MaxObjects+1)
	for i := range objs {
		objs[i] = ObjectLen{ID: string(rune('a' + i)), Length: 1}
	}
	in := Input{Objects: objs, BucketSizes: []int64{16}, MaxExtraBytes: math.MaxInt64}
	_, err := PlanBuckets(in)
	if !errors.Is(err, ErrTooManyObjects) {
		t.Fatalf("err = %v, want ErrTooManyObjects", err)
	}
}

func TestExactlyMaxObjects(t *testing.T) {
	objs := make([]ObjectLen, MaxObjects)
	for i := range objs {
		objs[i] = ObjectLen{ID: string(rune('a' + i)), Length: int64(1 + i)}
	}
	in := Input{
		Objects:       objs,
		BucketSizes:   []int64{8, 16, 32, 64},
		MaxExtraBytes: 1_000_000,
	}
	p, err := PlanBuckets(in)
	if err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	assertPlan(t, in, p, p.DistinctBuckets, p.TotalPadding)
	wantSpace := new(big.Int).Exp(big.NewInt(4), big.NewInt(20), nil)
	if p.Evidence.AssignmentSpace.Cmp(wantSpace) != 0 {
		t.Fatalf("space = %s, want 4^20 = %s", p.Evidence.AssignmentSpace, wantSpace)
	}
}

func TestInputNotMutated(t *testing.T) {
	in := Input{
		Objects:       objects([]string{"a", "b"}, []int64{10, 20}),
		BucketSizes:   []int64{16, 32, 64},
		MaxExtraBytes: 1000,
	}
	snapshot := in
	if _, err := PlanBuckets(in); err != nil {
		t.Fatalf("PlanBuckets: %v", err)
	}
	if len(in.Objects) != len(snapshot.Objects) ||
		len(in.BucketSizes) != len(snapshot.BucketSizes) ||
		in.MaxExtraBytes != snapshot.MaxExtraBytes {
		t.Fatal("input was mutated")
	}
	for i := range in.Objects {
		if in.Objects[i] != snapshot.Objects[i] {
			t.Fatalf("object %d mutated: %+v", i, in.Objects[i])
		}
	}
}

func TestEnumerationCap(t *testing.T) {
	objs := make([]ObjectLen, MaxObjects)
	for i := range objs {
		objs[i] = ObjectLen{ID: string(rune('a' + i)), Length: 1}
	}
	in := Input{
		Objects:       objs,
		BucketSizes:   []int64{2, 4, 8, 16, 32, 64, 128},
		MaxExtraBytes: 1_000_000,
	}
	p, err := PlanBuckets(in, WithEnumerationNodeCap(10))
	if !errors.Is(err, ErrEnumerationLimit) {
		t.Fatalf("err = %v, want ErrEnumerationLimit", err)
	}
	if p != nil {
		t.Fatal("plan must be nil when enumeration is capped")
	}

	// 不设上限时同一输入必须成功，且与独立暴力枚举一致。
	ok, err := PlanBuckets(in, WithEnumerationNodeCap(0))
	if err != nil {
		t.Fatalf("PlanBuckets uncapped: %v", err)
	}
	assertPlan(t, in, ok, ok.DistinctBuckets, ok.TotalPadding)
}
