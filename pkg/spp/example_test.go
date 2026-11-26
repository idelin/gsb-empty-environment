package spp_test

import (
	"fmt"

	"prescreen.local/ps-f69a1f50e0/pkg/spp"
)

// ExampleAnalyze 展示最小可导入用法：构造 SPP 实例并读取结局分类。
func ExampleAnalyze() {
	cfg := &spp.Config{
		Destination: 0,
		Nodes:       []int{0, 1, 2},
		Edges: []spp.EdgeConfig{
			{From: 1, To: 0, Kind: "c2p"},
			{From: 2, To: 1, Kind: "c2p"},
		},
		Preferences: map[string][]spp.Path{
			"0": {{0}},
			"1": {{1, 0}},
			"2": {{2, 1, 0}},
		},
	}
	report, err := spp.Analyze(cfg, spp.AnalysisOptions{SkipRepair: true})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(report.Outcome)
	fmt.Println(report.GR.StrictlyValleyFree())
	// Output:
	// unique_stable
	// true
}
