package pagearchive

import "testing"

func BenchmarkPlanMaxDenseGraph(b *testing.B) {
	pages := make([][]byte, MaxPages)
	for i := range pages {
		page := make([]byte, 128)
		page[0] = byte(i + 1)
		pages[i] = page
	}

	edges := make([]CandidateEdge, 0, (MaxPages-1)*MaxBaseCandidates)
	for to := 1; to < MaxPages; to++ {
		for offset := 1; offset <= MaxBaseCandidates && to-offset >= 0; offset++ {
			edges = append(edges, CandidateEdge{From: to - offset, To: to})
		}
	}

	input := PlanInput{
		Pages:          pages,
		Snapshots:      [][]int{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}},
		CandidateEdges: edges,
		MetadataCost:   16,
		Budget:         MaxPages * (128 + 16),
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  MaxCachePages,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Plan(input); err != nil {
			b.Fatal(err)
		}
	}
}
