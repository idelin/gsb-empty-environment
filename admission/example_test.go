package admission

import "fmt"

func ExampleAdmit() {
	req := PublishRequest{
		Batches: []Batch{
			{ID: "L1", Side: "L", Category: "x"},
			{ID: "L2", Side: "L", Category: "x"},
			{ID: "R1", Side: "R", Category: "y"},
			{ID: "R2", Side: "R", Category: "z"},
		},
		Edges: []Edge{
			{L: "L1", R: "R1", JointCategory: "J"},
			{L: "L1", R: "R2", JointCategory: "J"},
			{L: "L2", R: "R1", JointCategory: "J"},
			{L: "L2", R: "R2", JointCategory: "J"},
		},
		ProtectedBatches: []string{"L1"},
		PrivacyThreshold: 2,
	}
	report, err := Admit(req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("decision:", report.Decision)
	fmt.Println("possible joint categories:", report.Batches[0].PossibleJointCategories)
	// Output:
	// decision: below_threshold
	// possible joint categories: 1
}

func ExampleGateway() {
	gw := NewGateway()
	first := PublishRequest{
		Batches: []Batch{
			{ID: "L1", Side: "L", Category: "x"},
			{ID: "R1", Side: "R", Category: "y"},
		},
		Edges:            []Edge{{L: "L1", R: "R1", JointCategory: "J"}},
		ProtectedBatches: []string{"L1"},
		PrivacyThreshold: 1,
	}
	report, err := gw.Publish(first)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("first:", report.Decision, "committed:", gw.Committed())
	// Output:
	// first: publishable committed: true
}
