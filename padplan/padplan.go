package padplan

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
)

// MaxObjects 是单次规划允许的最大对象数量。
const MaxObjects = 20

// 默认全分配枚举访问节点上限，防止病态输入下枚举失控；可通过
// WithEnumerationNodeCap 调整，<= 0 表示不限制。
const defaultEnumerationNodeCap int64 = 2_000_000

// 规划失败或输入非法时返回的哨兵错误，调用方可用 errors.Is 判断。
var (
	// ErrInvalidInput 表示输入不满足前置条件。
	ErrInvalidInput = errors.New("padplan: invalid input")
	// ErrTooManyObjects 表示对象数量超过 MaxObjects。
	ErrTooManyObjects = errors.New("padplan: too many objects")
	// ErrNoFeasibleBucket 表示某个对象的长度超过最大允许桶边界。
	ErrNoFeasibleBucket = errors.New("padplan: no bucket large enough")
	// ErrBudgetExceeded 表示预算内不存在可行填充方案。
	ErrBudgetExceeded = errors.New("padplan: padding budget exceeded")
	// ErrEnumerationLimit 表示全分配枚举达到节点上限，无法完成核对。
	ErrEnumerationLimit = errors.New("padplan: enumeration node cap reached")
	// ErrVerificationMismatch 表示全分配枚举发现了比规划器更优的方案。
	ErrVerificationMismatch = errors.New("padplan: verification mismatch")
)

// ObjectLen 描述一个待填充对象的标识与真实长度（字节）。
type ObjectLen struct {
	ID     string
	Length int64
}

// Input 是规划输入。BucketSizes 必须为升序、去重的正整数桶边界。
type Input struct {
	Objects       []ObjectLen
	BucketSizes   []int64
	MaxExtraBytes int64
}

// Assignment 是单个对象的桶分配决策。
type Assignment struct {
	ObjectID string
	Length   int64
	Bucket   int64
	Padding  int64
}

// Evidence 是规划与核对过程的可审计证据。
type Evidence struct {
	// Budget 为调用方给出的总填充字节预算。
	Budget int64
	// TotalPadding 为方案总填充字节，满足 TotalPadding <= Budget。
	TotalPadding int64
	// BudgetHeadroom 为预算剩余字节。
	BudgetHeadroom int64
	// DistinctBuckets 为方案实际使用的不同桶大小数量。
	DistinctBuckets int
	// AssignmentSpace 为全分配空间大小，等于 len(BucketSizes)^len(Objects)，
	// 含不可行分配，用于说明枚举的理论规模。
	AssignmentSpace *big.Int
	// FeasibleLeaves 为枚举中访问到的满足桶容量与预算约束的叶子数。
	FeasibleLeaves int64
	// NodesVisited 为全分配枚举实际访问的 DFS 节点数（含根节点）。
	NodesVisited int64
	// NodeCap 为枚举节点上限，0 表示不限制。
	NodeCap int64
	// Capped 报告枚举是否因节点上限而终止；正常完成的方案恒为 false。
	Capped bool
	// CombosEvaluated 为规划器评估过的桶子集（组合）数量。
	CombosEvaluated int64
}

// Plan 是成功规划的结果。Assignments 顺序与输入对象顺序一致。
type Plan struct {
	Assignments     []Assignment
	DistinctBuckets int
	TotalPadding    int64
	BucketsUsed     []int64
	Evidence        Evidence
}

type config struct {
	enumNodeCap int64
}

// Option 调整 PlanBuckets 的行为。
type Option func(*config)

// WithEnumerationNodeCap 设置全分配枚举的 DFS 节点上限；cap <= 0 表示不限制。
// 达到上限时 PlanBuckets 返回包装 ErrEnumerationLimit 的错误且不产出方案。
func WithEnumerationNodeCap(cap int64) Option {
	return func(c *config) { c.enumNodeCap = cap }
}

// PlanBuckets 执行填充规划并以全分配枚举核对。
//
// 该函数是纯函数：不会修改 Input，也不触碰任何实际存储对象；任何失败路径都
// 返回 nil 方案与非空错误，调用方应保持对象原样。
func PlanBuckets(in Input, opts ...Option) (*Plan, error) {
	cfg := config{enumNodeCap: defaultEnumerationNodeCap}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := validate(in); err != nil {
		return nil, err
	}

	candidate, combos, err := optimize(in)
	if err != nil {
		return nil, err
	}

	lengths := make([]int64, len(in.Objects))
	for i, obj := range in.Objects {
		lengths[i] = obj.Length
	}
	verification, err := enumerateAll(lengths, in.BucketSizes, in.MaxExtraBytes,
		candidate.DistinctBuckets, candidate.TotalPadding, cfg.enumNodeCap)
	if err != nil {
		return nil, err
	}
	if verification.bestDistinct != candidate.DistinctBuckets ||
		verification.bestPadding != candidate.TotalPadding {
		return nil, fmt.Errorf("%w: enumerator optimum (%d buckets, %d padding) != planner optimum (%d buckets, %d padding)",
			ErrVerificationMismatch,
			verification.bestDistinct, verification.bestPadding,
			candidate.DistinctBuckets, candidate.TotalPadding)
	}

	candidate.Evidence = Evidence{
		Budget:          in.MaxExtraBytes,
		TotalPadding:    candidate.TotalPadding,
		BudgetHeadroom:  in.MaxExtraBytes - candidate.TotalPadding,
		DistinctBuckets: candidate.DistinctBuckets,
		AssignmentSpace: verification.space,
		FeasibleLeaves:  verification.leaves,
		NodesVisited:    verification.nodes,
		NodeCap:         cfg.enumNodeCap,
		Capped:          false,
		CombosEvaluated: combos,
	}
	return candidate, nil
}

func validate(in Input) error {
	n := len(in.Objects)
	switch {
	case n == 0:
		return fmt.Errorf("%w: at least one object is required", ErrInvalidInput)
	case n > MaxObjects:
		return fmt.Errorf("%w: got %d objects, limit is %d", ErrTooManyObjects, n, MaxObjects)
	}
	seenIDs := make(map[string]struct{}, n)
	for i, obj := range in.Objects {
		if obj.ID == "" {
			return fmt.Errorf("%w: object at index %d has empty ID", ErrInvalidInput, i)
		}
		if _, dup := seenIDs[obj.ID]; dup {
			return fmt.Errorf("%w: duplicate object ID %q", ErrInvalidInput, obj.ID)
		}
		seenIDs[obj.ID] = struct{}{}
		if obj.Length < 0 {
			return fmt.Errorf("%w: object %q has negative length %d", ErrInvalidInput, obj.ID, obj.Length)
		}
	}
	if len(in.BucketSizes) == 0 {
		return fmt.Errorf("%w: at least one bucket size is required", ErrInvalidInput)
	}
	prev := int64(0)
	for _, b := range in.BucketSizes {
		if b <= 0 {
			return fmt.Errorf("%w: bucket size %d is not positive", ErrInvalidInput, b)
		}
		if b <= prev {
			return fmt.Errorf("%w: bucket sizes must be strictly ascending, violation at %d", ErrInvalidInput, b)
		}
		prev = b
	}
	if in.MaxExtraBytes < 0 {
		return fmt.Errorf("%w: negative padding budget %d", ErrInvalidInput, in.MaxExtraBytes)
	}
	return nil
}

// optimize 按桶子集做枚举：先找最小的不同桶数量 d，再在所有 d 元子集中找
// 总填充最小且不超预算的方案。每个对象在所选子集内被强制分配给能容纳它的
// 最小桶，因此给定子集的最优分配是唯一确定的。
func optimize(in Input) (*Plan, int64, error) {
	n := len(in.Objects)
	k := len(in.BucketSizes)

	// minIdx[i] 是能容纳对象 i 的最小桶在 BucketSizes 中的下标；
	// 同时累计无预算约束下的填充下界，用于预算错误诊断。
	minIdx := make([]int, n)
	var lowerBoundPadding int64
	for i, obj := range in.Objects {
		idx := sort.Search(k, func(j int) bool { return in.BucketSizes[j] >= obj.Length })
		if idx == k {
			return nil, 0, fmt.Errorf("%w: object %q length %d exceeds largest bucket %d",
				ErrNoFeasibleBucket, obj.ID, obj.Length, in.BucketSizes[k-1])
		}
		minIdx[i] = idx
		sum, ok := safeAdd(lowerBoundPadding, in.BucketSizes[idx]-obj.Length)
		if !ok {
			return nil, 0, fmt.Errorf("%w: padding computation overflow", ErrInvalidInput)
		}
		lowerBoundPadding = sum
	}

	var (
		bestCombo   []int
		bestPadding int64
		combos      int64
		combo       = make([]int, 0, k)
	)

	// evaluate 计算给定桶子集（桶下标，升序）的强制分配与总填充。
	// 返回 ok=false 表示子集无法容纳某对象、超出预算或整数溢出。
	evaluate := func() (padding int64, ok bool) {
		for i, mi := range minIdx {
			ci := sort.SearchInts(combo, mi)
			if ci == len(combo) {
				return 0, false
			}
			sum, addOK := safeAdd(padding, in.BucketSizes[combo[ci]]-in.Objects[i].Length)
			if !addOK {
				return 0, false
			}
			padding = sum
			if padding > in.MaxExtraBytes {
				return 0, false
			}
		}
		return padding, true
	}

	var enumerate func(start, depth, target int)
	enumerate = func(start, depth, target int) {
		if depth == target {
			combos++
			padding, ok := evaluate()
			if !ok {
				return
			}
			// 同一目标桶数 d 内必须评估全部组合：仅在填充严格更优时替换，
			// 首个最优组合（字典序最小的桶子集）天然具有确定性。
			if bestCombo == nil || padding < bestPadding ||
				(padding == bestPadding && lexBefore(combo, bestCombo)) {
				bestCombo = append(make([]int, 0, len(combo)), combo...)
				bestPadding = padding
			}
			return
		}
		for j := start; j <= k-(target-depth); j++ {
			combo = append(combo, j)
			enumerate(j+1, depth+1, target)
			combo = combo[:len(combo)-1]
		}
	}

	for d := 1; d <= k && bestCombo == nil; d++ {
		combo = combo[:0]
		enumerate(0, 0, d)
	}
	if bestCombo == nil {
		return nil, 0, fmt.Errorf("%w: no feasible plan within budget %d (unconstrained minimum padding is %d)",
			ErrBudgetExceeded, in.MaxExtraBytes, lowerBoundPadding)
	}

	assignments := make([]Assignment, n)
	usedBuckets := make(map[int64]struct{}, len(bestCombo))
	var totalPadding int64
	for i, obj := range in.Objects {
		ci := sort.SearchInts(bestCombo, minIdx[i])
		bucket := in.BucketSizes[bestCombo[ci]]
		padding := bucket - obj.Length
		totalPadding += padding
		usedBuckets[bucket] = struct{}{}
		assignments[i] = Assignment{
			ObjectID: obj.ID,
			Length:   obj.Length,
			Bucket:   bucket,
			Padding:  padding,
		}
	}

	bucketsUsed := make([]int64, 0, len(usedBuckets))
	for b := range usedBuckets {
		bucketsUsed = append(bucketsUsed, b)
	}
	sort.Slice(bucketsUsed, func(i, j int) bool { return bucketsUsed[i] < bucketsUsed[j] })

	return &Plan{
		Assignments:     assignments,
		DistinctBuckets: len(bucketsUsed),
		TotalPadding:    totalPadding,
		BucketsUsed:     bucketsUsed,
	}, combos, nil
}

func safeAdd(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}

// lexBefore 报告等长升序下标切片 a 是否字典序小于 b。
func lexBefore(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
