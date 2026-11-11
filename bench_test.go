package netsec

import "testing"

func BenchmarkAnalyze20(b *testing.B) {
	// 构造 20 个候选事件的对称卡死运行：双方等待、发送、接收交错，
	// 全部 2^20 个子集都要做一次具体重放。
	events := make([]Event, 0, MaxCandidates)
	events = append(events,
		Event{Kind: KindWait, Actor: "A", Resources: []string{"rB"}},
		Event{Kind: KindWait, Actor: "B", Resources: []string{"rA"}},
	)
	for len(events) < MaxCandidates {
		events = append(events,
			Event{Kind: KindSend, Actor: "A", Ref: "mA"},
			Event{Kind: KindReceive, Actor: "B", Ref: "mA", Produces: "rB"},
			Event{Kind: KindSend, Actor: "B", Ref: "mB"},
			Event{Kind: KindReceive, Actor: "A", Ref: "mB", Produces: "rA"},
		)
	}
	events = events[:MaxCandidates]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Analyze(events, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
