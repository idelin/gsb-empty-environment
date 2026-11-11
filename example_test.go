package netsec_test

import (
	"context"
	"fmt"
	"sync"

	netsec "example.org/netsec/q0100"
)

// ExampleCombiner 演示网关如何并行调度检测器、轮询组合结论并提前取消无关工作。
func ExampleCombiner() {
	// 检测器 0..3 属于允许组（需要 3 张允许票），4..5 属于拒绝组（任一命中即拒绝）。
	combiner, err := netsec.New(netsec.Config{
		AllowGroup:     []int{0, 1, 2, 3},
		DenyGroup:      []int{4, 5},
		AllowThreshold: 3,
	})
	if err != nil {
		panic(err)
	}

	// detector 模拟一个并行安全检测器；ctx 被取消时应立即放弃工作。
	detector := func(ctx context.Context, id int, result netsec.Outcome) {
		_ = ctx
		_ = combiner.Submit(id, result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	results := []netsec.Outcome{
		netsec.ProvenAllow, // 0
		netsec.ProvenAllow, // 1
		netsec.ProvenAllow, // 2 -> 允许票凑齐
		netsec.Unknown,     // 3 -> 拒绝未排除前仍需等待，拒绝命中后变无关
		netsec.Unknown,     // 4 -> 拒绝组，相关分支
		netsec.ProvenDeny,  // 5 -> 关键拒绝
	}
	for id, result := range results {
		wg.Add(1)
		go func(id int, result netsec.Outcome) {
			defer wg.Done()
			detector(ctx, id, result)
		}(id, result)
	}

	// 网关轮询：一旦定案就取消所有无关检测器并停止等待。生产代码应在每次有
	// 检测器返回后再 Evaluate，而非忙等。
	var decision netsec.Status
	for {
		decision = combiner.Evaluate()
		if decision.Verdict != netsec.Pending {
			cancel() // Irrelevant 中的检测器已在组合器内标记取消
			break
		}
	}
	wg.Wait()

	fmt.Printf("verdict=%s reason=%s evidence=%v\n",
		decision.Verdict, decision.Reason, decision.Evidence)
	// Output: verdict=deny reason=deny-hit evidence=[5]
}
