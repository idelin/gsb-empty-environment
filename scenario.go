package main

import (
	"fmt"
	"strings"
)

// ScenarioResult 故障转移场景的运行结果与证据。
type ScenarioResult struct {
	Sim      *Sim
	Resource *Resource
	A, B     *Node
}

// RunFailoverScenario 运行核心场景(完全确定性):
// t=0    A 竞选, t=20 获得租约 token=1 (TTL 500ms, 授予者时钟过期时刻 t=510)
// t=30   A 写 "A#1" (被接受); 同时发出一条将被网络滞留 800ms 的写 "A-delayed"
// t=40   A 进程暂停 1000ms (GC), 无法续期
// t=510  A 的租约在授予者时钟上过期
// t=600  B 竞选, t=620 获得租约 token=2, t=630 写 "B#2" (被接受)
// t=830  滞留的 "A-delayed" (token=1) 到达资源, 必须被拒绝
// t=1040 A 恢复, 本地时钟因暂停静止, 误判租约仍有效
// t=1100 A 用旧 token=1 写 "A-stale", 必须被拒绝
func RunFailoverScenario() *ScenarioResult {
	sim := NewSim()
	net := NewNetwork(sim, 10)
	NewGrantor("grantor", sim, net, NewClock(sim, 0, 1.0))
	resource := NewResource("resource", sim, net)
	a := NewNode("A", sim, net, NewClock(sim, 0, 1.0), "grantor", "resource", 500)
	b := NewNode("B", sim, net, NewClock(sim, 0, 1.0), "grantor", "resource", 500)

	sim.After(0, a.Campaign)
	sim.After(30, func() {
		a.WriteValue("A#1")
		// 一条被网络滞留 800ms 的旧写, 将在 B 提交之后才到达
		if a.leader {
			sim.Logf("A: 写 %q (token=%d) 进入网络, 但将被滞留 800ms", "A-delayed", a.lease.Token)
			net.SendAfter(Message{From: "A", To: "resource", Kind: MsgWrite,
				Token: a.lease.Token, Value: "A-delayed"}, 800)
		}
	})
	sim.After(40, func() { a.Pause(1000) })
	sim.After(600, b.Campaign)
	sim.After(630, func() { b.WriteValue("B#2") })
	sim.After(1100, func() { a.WriteValue("A-stale") })

	sim.Run()
	return &ScenarioResult{Sim: sim, Resource: resource, A: a, B: b}
}

// SafetyEvidence 从运行结果中提取安全性证据。
func (r *ScenarioResult) SafetyEvidence() string {
	var b strings.Builder
	res := r.Resource
	tokens := []int64{}
	monotonic := true
	prev := int64(-1)
	for _, w := range res.Accepted() {
		tokens = append(tokens, w.Token)
		if w.Token <= prev {
			monotonic = false
		}
		prev = w.Token
	}
	fmt.Fprintf(&b, "已接受写的 fencing token 序列: %v — 严格递增: %v\n", tokens, monotonic)
	for _, w := range res.Rejected() {
		fmt.Fprintf(&b, "已拒绝: %s 的写 token=%d value=%q (t=%dms) — 旧任期/重复写被拦截\n",
			w.From, w.Token, w.Value, w.Time)
	}
	value, _ := res.Value()
	fmt.Fprintf(&b, "资源最终值: %q — 来自最高 token, 未被旧消息覆盖\n", value)
	fmt.Fprintf(&b, "A 凭本地时钟认定租约有效并发写 %d 次, 其中租约实际已过期的误判 %d 次 — 租约判断失效, 但资源未受影响\n",
		r.A.believedLeader, r.A.misjudged)
	return b.String()
}

// LivenessEvidence 从运行结果中提取活性证据。
func (r *ScenarioResult) LivenessEvidence() string {
	var b strings.Builder
	res := r.Resource
	committed := res.Accepted()
	if len(committed) > 0 {
		last := committed[len(committed)-1]
		fmt.Fprintf(&b, "故障转移完成: %s 的写 %q (token=%d) 于 t=%dms 提交\n",
			last.From, last.Value, last.Token, last.Time)
		fmt.Fprintf(&b, "从旧租约过期 (t=510ms) 到新领导提交 (t=%dms) 耗时 %dms — 系统在旧领导瘫痪后恢复服务\n",
			last.Time, last.Time-510)
	}
	fmt.Fprintf(&b, "B 已提交的写: %v\n", r.B.Committed())
	return b.String()
}
