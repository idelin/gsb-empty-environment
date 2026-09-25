package main

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func rejectedContains(res *Resource, value string) bool {
	for _, w := range res.Rejected() {
		if w.Value == value {
			return true
		}
	}
	return false
}

// 安全性: 旧领导暂停后恢复, 其写必须被资源拒绝。
func TestStaleLeaderWriteRejectedAfterResume(t *testing.T) {
	r := RunFailoverScenario()
	if !rejectedContains(r.Resource, "A-stale") {
		t.Fatalf("旧领导恢复后的写未被拒绝; rejected=%v", r.Resource.Rejected())
	}
	if value, _ := r.Resource.Value(); value != "B#2" {
		t.Fatalf("资源最终值 = %q, 期望 %q", value, "B#2")
	}
}

// 安全性: 新领导成功后, 网络中滞留的旧消息不能覆盖新值。
func TestDelayedOldMessageCannotOverwrite(t *testing.T) {
	r := RunFailoverScenario()
	if !rejectedContains(r.Resource, "A-delayed") {
		t.Fatalf("滞留旧消息未被拒绝; rejected=%v", r.Resource.Rejected())
	}
	accepted := r.Resource.Accepted()
	last := accepted[len(accepted)-1]
	if last.Value != "B#2" {
		t.Fatalf("最后接受的写 = %q, 期望 %q (旧消息覆盖了新值)", last.Value, "B#2")
	}
}

// 安全性: token 重复(相等)非法。
func TestDuplicateTokenRejected(t *testing.T) {
	sim := NewSim()
	res := &Resource{id: "r", sim: sim}
	if err := res.Write("x", 5, "v1"); err != nil {
		t.Fatalf("首次写应被接受: %v", err)
	}
	if err := res.Write("x", 5, "v2"); err != ErrStaleToken {
		t.Fatalf("重复 token 应被拒绝, 得到 %v", err)
	}
	if value, _ := res.Value(); value != "v1" {
		t.Fatalf("重复写覆盖了原值: %q", value)
	}
}

// 安全性: token 回退(更小)非法。
func TestRegressiveTokenRejected(t *testing.T) {
	sim := NewSim()
	res := &Resource{id: "r", sim: sim}
	if err := res.Write("x", 5, "v1"); err != nil {
		t.Fatalf("首次写应被接受: %v", err)
	}
	for _, token := range []int64{4, 1, 0} {
		if err := res.Write("y", token, "stale"); err != ErrStaleToken {
			t.Fatalf("回退 token=%d 应被拒绝, 得到 %v", token, err)
		}
	}
	if value, _ := res.Value(); value != "v1" {
		t.Fatalf("回退写覆盖了原值: %q", value)
	}
}

// 安全性: fencing token 单调性是显式不变量, 场景运行中必须保持。
func TestFencingTokenMonotonicInvariant(t *testing.T) {
	r := RunFailoverScenario()
	prev := int64(-1)
	for _, w := range r.Resource.Accepted() {
		if w.Token <= prev {
			t.Fatalf("token 单调性被破坏: ... %d 之后接受了 %d", prev, w.Token)
		}
		prev = w.Token
	}
}

// 安全性: 时钟偏差使租约判断失效(旧领导误判自己仍是领导),
// 但资源端 fencing token 仍提供真正隔离 —— 租约本身不是强隔离。
func TestLeaseAloneIsNotIsolation(t *testing.T) {
	r := RunFailoverScenario()
	// A 恢复时本地时钟静止在 t=30ms, 判定租约(过期时刻 t=510ms)仍有效
	if r.A.misjudged == 0 {
		t.Fatal("预期 A 在时钟偏差下误判自己仍是领导")
	}
	found := false
	for _, line := range r.Sim.Log() {
		if strings.Contains(line, "A: 本地时钟") && strings.Contains(line, "判定租约有效") {
			found = true
		}
	}
	if !found {
		t.Fatal("事件日志中缺少 A 误判租约有效的证据")
	}
	// 尽管如此, 资源拒绝了 A 的全部过期写
	for _, w := range r.Resource.Accepted() {
		if w.From == "A" && w.Time > 510 {
			t.Fatalf("租约过期后 A 的写仍被接受: %+v", w)
		}
	}
}

// 活性: 旧领导瘫痪后, 新领导在有限时间内获得租约并提交写。
func TestLivenessNewLeaderCommits(t *testing.T) {
	r := RunFailoverScenario()
	committed := r.B.Committed()
	if len(committed) == 0 {
		t.Fatal("新领导 B 未提交任何写")
	}
	// 从旧租约过期 (t=510ms) 到新写提交, 必须有界
	var commitTime int64 = -1
	for _, w := range r.Resource.Accepted() {
		if w.From == "B" {
			commitTime = w.Time
		}
	}
	if commitTime < 0 {
		t.Fatal("资源未接受 B 的任何写")
	}
	if bound := int64(1000); commitTime-510 > bound {
		t.Fatalf("故障转移耗时 %dms 超过上界 %dms", commitTime-510, bound)
	}
}

// 安全性(随机故障注入): 随机网络延迟、随机暂停、多候选人竞争下,
// 资源接受的 token 序列始终严格递增, 且系统始终能提交写(活性)。
func TestMonotonicityUnderRandomChaos(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sim := NewSim()
		net := NewNetwork(sim, 1+rng.Int63n(30))
		NewGrantor("grantor", sim, net, NewClock(sim, 0, 1.0))
		resource := NewResource("resource", sim, net)

		nodes := []*Node{
			NewNode("N1", sim, net, NewClock(sim, rng.Int63n(20), 1.0), "grantor", "resource", 150),
			NewNode("N2", sim, net, NewClock(sim, 0, 1.0), "grantor", "resource", 150),
			NewNode("N3", sim, net, NewClock(sim, 0, 1.0+0.01*float64(rng.Int63n(3))), "grantor", "resource", 150),
		}
		// 随机竞选与随机暂停
		for t := int64(0); t < 2000; t += 100 {
			for _, n := range nodes {
				n := n
				if rng.Intn(2) == 0 {
					sim.After(t+rng.Int63n(50), n.Campaign)
				}
				if rng.Intn(10) == 0 {
					sim.After(t, func() { n.Pause(50 + rng.Int63n(200)) })
				}
			}
		}
		// 每个节点在认为自己是领导时周期性尝试写
		for t := int64(50); t < 2000; t += 50 {
			for _, n := range nodes {
				n := n
				sim.After(t, func() { n.WriteValue(n.id + "@" + strconv.FormatInt(t, 10)) })
			}
		}
		sim.Run()

		prev := int64(-1)
		for _, w := range resource.Accepted() {
			if w.Token <= prev {
				t.Fatalf("seed=%d: token 单调性被破坏: %d 之后接受了 %d", seed, prev, w.Token)
			}
			prev = w.Token
		}
		if len(resource.Accepted()) == 0 {
			t.Fatalf("seed=%d: 活性失败, 2000ms 内没有任何写被提交", seed)
		}
	}
}
