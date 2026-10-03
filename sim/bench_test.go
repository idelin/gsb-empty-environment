package sim

import "testing"

func BenchmarkSim(b *testing.B) {
	cfg := WorkloadConfig{
		NumTxns:         20000,
		NumPages:        4096,
		MaxOpsPerTxn:    10,
		CommitPct:       60,
		AbortPct:        25,
		CrashAt:         120000,
		CheckpointEvery: 50000,
		FlushEvery:      33333,
		PostCrashTxns:   100,
	}
	for i := 0; i < b.N; i++ {
		s := New(1)
		s.LoadWorkload(cfg)
		s.Run(1 << 62)
	}
}
