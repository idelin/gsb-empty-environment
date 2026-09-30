package redsim

import (
	"reflect"
	"testing"
)

func buildSim(seed uint64, end int64) *Sim {
	queues := []*Queue{
		NewQueue(0, REDConfig{MinTh: 3, MaxTh: 12, Wq: 0.002, MaxP: 0.1, MTU: 1500}),
		NewQueue(1, REDConfig{MinTh: 3, MaxTh: 12, Wq: 0.002, MaxP: 0.1, MTU: 1500}),
	}
	link := NewLink(100, 1500, len(queues))
	var arrivals []Arrival
	var id uint64
	for q := 0; q < 2; q++ {
		size := 1500 - 700*q
		for i := 0; i < 300; i++ {
			arrivals = append(arrivals, Arrival{
				Tick: int64(i)*5 + int64(q),
				Pkt:  Packet{ID: id, Queue: q, Len: size},
			})
			id++
		}
	}
	return NewSim(seed, queues, link, arrivals)
}

// 相同 seed 轨迹完全相同。
func TestDeterministicReplay(t *testing.T) {
	s1 := buildSim(42, 10000)
	s1.Run(10000)
	s2 := buildSim(42, 10000)
	s2.Run(10000)
	if !reflect.DeepEqual(s1.Drops, s2.Drops) {
		t.Fatal("same seed must reproduce identical drop records")
	}
	if !reflect.DeepEqual(s1.DepartLog, s2.DepartLog) {
		t.Fatal("same seed must reproduce identical departure log")
	}
	if len(s1.Drops) == 0 {
		t.Fatal("scenario should produce some drops")
	}
}

// 分块运行与一次跑完结果一致（抽样按事件身份，不受分块影响）。
func TestChunkedRunMatchesSingleRun(t *testing.T) {
	whole := buildSim(7, 10000)
	whole.Run(10000)
	chunked := buildSim(7, 10000)
	for _, boundary := range []int64{1000, 3333, 7777, 10000} {
		chunked.Run(boundary)
	}
	if !reflect.DeepEqual(whole.Drops, chunked.Drops) {
		t.Fatal("chunked run must match single run (drops)")
	}
	if !reflect.DeepEqual(whole.DepartLog, chunked.DepartLog) {
		t.Fatal("chunked run must match single run (departures)")
	}
	for i := range whole.Queues {
		if whole.Queues[i].Stats != chunked.Queues[i].Stats {
			t.Fatalf("queue %d stats differ between whole and chunked run", i)
		}
	}
}

// 全链路字节守恒：到达 == 丢弃 + 离链 + 仍在队列。
func TestByteConservationSim(t *testing.T) {
	s := buildSim(99, 10000)
	s.Run(10000)
	if !s.CheckByteConservation() {
		t.Fatal("byte conservation violated")
	}
	for _, q := range s.Queues {
		st := q.Stats
		if st.BytesArrived != st.BytesDropped+st.BytesOut+q.QueuedBytes() {
			t.Fatalf("queue %d: in=%d dropped=%d out=%d queued=%d",
				q.ID, st.BytesArrived, st.BytesDropped, st.BytesOut, q.QueuedBytes())
		}
		if st.Arrived != st.Dropped+st.Departed+q.Len() {
			t.Fatalf("queue %d: packet conservation violated", q.ID)
		}
	}
}

// DRR 公平性：两个持续积压的队列长期字节份额均衡，
// 且每个窗口都有吞吐（无长期饥饿），不以平均延迟掩盖。
func TestFairSchedulingNoStarvation(t *testing.T) {
	queues := []*Queue{
		NewQueue(0, REDConfig{MinTh: 1000, MaxTh: 2000, Wq: 0.001, MaxP: 0.1}),
		NewQueue(1, REDConfig{MinTh: 1000, MaxTh: 2000, Wq: 0.001, MaxP: 0.1}),
	}
	link := NewLink(100, 1500, 2)
	var arrivals []Arrival
	var id uint64
	// 两个队列都持续积压：大包队列 vs 小包队列
	for i := 0; i < 2000; i++ {
		arrivals = append(arrivals,
			Arrival{Tick: int64(i) * 10, Pkt: Packet{ID: id, Queue: 0, Len: 1500}},
			Arrival{Tick: int64(i) * 10, Pkt: Packet{ID: id + 1, Queue: 1, Len: 500}})
		id += 2
	}
	s := NewSim(1, queues, link, arrivals)
	s.Run(20000)

	b0, b1 := queues[0].Stats.BytesOut, queues[1].Stats.BytesOut
	if b0 == 0 || b1 == 0 {
		t.Fatalf("starvation: bytesOut q0=%d q1=%d", b0, b1)
	}
	ratio := float64(b0) / float64(b1)
	if ratio < 0.95 || ratio > 1.05 {
		t.Fatalf("unfair byte share: q0=%d q1=%d ratio=%.4f", b0, b1, ratio)
	}
	// 每个完整覆盖的 2000-tick 窗口两个队列都必须有吞吐
	const window = 2000
	fullWindows := int(20000 / window)
	for q, ws := range s.WindowedBytes(window) {
		for w := 0; w < fullWindows && w < len(ws); w++ {
			if ws[w] == 0 {
				t.Fatalf("queue %d starved in window %d", q, w)
			}
		}
	}
}
