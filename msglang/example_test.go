package msglang_test

import (
	"fmt"

	"example.org/netsec/q0470/msglang"
)

func ExampleMinimalIntersection() {
	result, err := msglang.MinimalIntersection(
		msglang.AmbiguousAcknowledge(),
		msglang.AmbiguousAuthorization(),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("empty:", result.Empty)
	fmt.Println("nodes:", result.NodeCount, "height:", result.Height)
	fmt.Println("tree:", result.Tree)
	fmt.Println("ack final:", result.LeftTrace.Final)
	fmt.Println("grant final:", result.RightTrace.Final)
	fmt.Println("ack steps:", len(result.LeftTrace.Steps))
	fmt.Println("grant steps:", len(result.RightTrace.Steps))
	// Output:
	// empty: false
	// nodes: 6 height: 2
	// tree: frame(body(subject[Principal:"anon"] nonce[Nonce:0x00] payload[ByteString:0x01]) context[ByteString:0x01])
	// ack final: ack:frame
	// grant final: grant:frame
	// ack steps: 6
	// grant steps: 6
}

func ExampleBoundedIntersect_unknownOnBudget() {
	result, err := msglang.BoundedIntersect(
		msglang.AmbiguousAcknowledge(),
		msglang.AmbiguousAuthorization(),
		msglang.Bounds{MaxNodes: 20, NodeBudget: 2},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("verdict:", result.Verdict)
	fmt.Println("exact empty:", result.ExactEmpty)
	fmt.Println("exact minimum nodes:", result.ExactMinNodes)
	// Output:
	// verdict: Unknown
	// exact empty: false
	// exact minimum nodes: 6
}

func ExampleBoundedIntersect_safeDesign() {
	result, err := msglang.BoundedIntersect(
		msglang.SafeAcknowledge(),
		msglang.SafeAuthorization(),
		msglang.Bounds{MaxNodes: 12, MaxHeight: 4, NodeBudget: 2_000_000},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("verdict:", result.Verdict)
	fmt.Println("exact empty:", result.ExactEmpty)
	// Output:
	// verdict: Empty
	// exact empty: true
}
