package main

import "testing"

func newTestSim() *Sim {
	paths := []Path{{"p1", 1}, {"p2", 2}, {"p3", 3}}
	flows := make([]Flow, 300)
	for i := range flows {
		flows[i] = Flow{ID: uint64(i + 1), Rate: 1.0}
	}
	return NewSim(NewAssigner(paths), paths, flows, 1.0)
}

// 守恒：产生包数 == 已发送 + 仍在队列
func TestSimConservation(t *testing.T) {
	s := newTestSim()
	s.Run(100)
	produced := 0
	for _, f := range s.flows {
		produced += f.seq
	}
	sent, queued := 0, 0
	for _, lk := range s.links {
		sent += lk.sent
		queued += len(lk.queue)
	}
	if produced != sent+queued {
		t.Fatalf("包不守恒: 产生 %d, 发送 %d, 队列 %d", produced, sent, queued)
	}
}

// 每 tick 各链路发送量不超过容量
func TestSimCapacityPerTick(t *testing.T) {
	s := newTestSim()
	for i := 0; i < 50; i++ {
		sent := s.Step()
		for id, n := range sent {
			if n > s.links[id].capacity {
				t.Fatalf("tick %d 路径 %s 发送 %d 超容量 %d", i, id, n, s.links[id].capacity)
			}
		}
	}
}

// 同一条流的所有包走同一路径
func TestSimSameFlowSamePath(t *testing.T) {
	s := newTestSim()
	s.Run(50)
	a := NewAssigner([]Path{{"p1", 1}, {"p2", 2}, {"p3", 3}})
	for flowID, pathID := range s.sentPath {
		if want := a.Assign(flowID); want != pathID {
			t.Fatalf("flow %d 走路径 %s，应为 %s", flowID, pathID, want)
		}
	}
}

// 路径负载占比近似容量权重比
func TestSimLoadFollowsWeight(t *testing.T) {
	paths := []Path{{"p1", 1}, {"p2", 2}, {"p3", 3}}
	flows := make([]Flow, 3000)
	for i := range flows {
		flows[i] = Flow{ID: uint64(i + 1), Rate: 0.5}
	}
	s := NewSim(NewAssigner(paths), paths, flows, 1000.0) // 容量充足，不积压
	s.Run(200)
	for _, st := range s.Stats() {
		want := map[string]float64{"p1": 1.0 / 6, "p2": 2.0 / 6, "p3": 3.0 / 6}[st.PathID]
		if st.Share < want-0.03 || st.Share > want+0.03 {
			t.Fatalf("路径 %s 负载占比 %.3f，期望约 %.3f", st.PathID, st.Share, want)
		}
		if st.Backlog > 0 {
			t.Fatalf("容量充足时路径 %s 不应积压，峰值 %d", st.PathID, st.Backlog)
		}
	}
}
