package bank_test

import (
	"fmt"
	"strings"
	"testing"

	"outboxsim/bank"
	"outboxsim/sim"
)

// 基线：无故障，每个命令恰好正常完成。
func TestBaseline(t *testing.T) {
	r := newRig(1, commands(3))
	r.run(t, 1000)
	r.assertFinal(t, 60)
	if r.s.Bus.DeliveredCount("cmd") != 3 {
		t.Fatalf("cmd deliveries = %d, want 3", r.s.Bus.DeliveredCount("cmd"))
	}
}

// 窗口 A：提交前崩溃。事务整体不存在；客户端重投后正常完成；
// 余额只增加一次，且崩溃前的消息从未外发。
func TestCrashBeforeCommit(t *testing.T) {
	cmds := commands(1)
	r := newRig(10, cmds)
	r.s.Arm("svc.before_commit:c1", 1)
	r.run(t, 1000)
	r.assertFinal(t, 10)
	if r.s.Bus.DeliveredCount("event") != 1 {
		t.Fatalf("event deliveries = %d, want 1 (nothing leaked pre-commit)",
			r.s.Bus.DeliveredCount("event"))
	}
}

// 窗口 B：提交成功、响应丢失（提交后崩溃）。重投命中 inbox，
// 不再修改业务；最终余额仍只增加一次，但 cmd 至少投递了两次。
func TestCommitThenResponseLost(t *testing.T) {
	cmds := commands(1)
	r := newRig(20, cmds)
	r.s.Arm("svc.after_commit:c1", 1)
	r.run(t, 2000)
	r.assertFinal(t, 10)
	if r.s.Bus.DeliveriesTo("svc") < 2 {
		t.Fatalf("expected cmd redelivery, got %d", r.s.Bus.DeliveriesTo("svc"))
	}
}

// 窗口 C：outbox 事件已发布、在标记 sent 前崩溃（或 ack 丢失）。
// relay 重发事件，下游去重：效果恰好一次，但事件确实投递过多次。
func TestPublishedBeforeMark(t *testing.T) {
	cmds := commands(1)
	r := newRig(30, cmds)
	r.s.Arm("svc.after_publish:ev-c1", 1)
	r.run(t, 2000)
	r.assertFinal(t, 10)
	if r.s.Bus.DeliveriesTo("ds") < 2 {
		t.Fatalf("expected duplicate event delivery, got %d", r.s.Bus.DeliveriesTo("ds"))
	}
	if got := bank.CountPrefix(r.dsDB, "effect:"); got != 1 {
		t.Fatalf("effects = %d, want 1 (downstream dedup)", got)
	}
}

// 下游在提交效果后、发 ack 前崩溃：上游超时重发，重复事件只重发 ack。
func TestDownstreamAckLost(t *testing.T) {
	cmds := commands(1)
	r := newRig(40, cmds)
	r.s.Arm("ds.after_commit:ev-c1", 1)
	r.run(t, 2000)
	r.assertFinal(t, 10)
}

// 纯通信故障：在传输中丢掉首个响应，重投完成；业务不受影响。
func TestResponseDroppedByNetwork(t *testing.T) {
	cmds := commands(1)
	r := newRig(50, cmds)
	dropped := false
	r.s.Bus.SetDropRule(func(m *sim.Msg) bool {
		if !dropped && m.Kind == "receipt" {
			dropped = true
			return true
		}
		return false
	})
	r.run(t, 2000)
	r.assertFinal(t, 10)
}

// 随机重放：每个故障点在每个对象上最多注入一次崩溃，并加入有界随机丢包。
// 安全不变量由模拟器在每个事件后检查；这里断言最终活性与去重结果。
func TestRandomReplaySafetyAndLiveness(t *testing.T) {
	const n = 4
	labels := []string{
		"svc.before_commit:",
		"svc.after_commit:",
		"svc.after_publish:ev-",
		"svc.before_mark:ev-",
		"ds.before_commit:ev-",
		"ds.after_commit:ev-",
	}
	for seed := int64(1); seed <= 40; seed++ {
		t.Run("", func(t *testing.T) {
			cmds := commands(n)
			r := newRig(seed, cmds)
			for _, l := range labels {
				for i := 1; i <= n; i++ {
					r.s.Arm(l+idOf(i), 1)
				}
			}
			// 有界丢包：每个 (kind,id) 的前两次发送各丢一次的概率事件，
			// 用确定性规则限制，保证之后必通 -> 活性可在有限时间内达成。
			seen := map[string]int{}
			r.s.Bus.SetDropRule(func(m *sim.Msg) bool {
				key := m.Kind + ":" + m.ID
				seen[key]++
				if seen[key] <= 2 && r.s.RNG().Intn(2) == 0 {
					return true
				}
				return false
			})
			r.run(t, 20000)
			var want int64
			for _, c := range cmds {
				want += c.Amount
			}
			r.assertFinal(t, want)
			if len(r.client.DoneReceipts()) != n {
				t.Fatalf("client receipts = %d, want %d", len(r.client.DoneReceipts()), n)
			}
		})
	}
}

// 确定性：同一种子两次运行的轨迹完全一致（故障窗口可重放）。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		r := newRig(777, commands(2))
		r.s.Arm("svc.after_publish:ev-c1", 1)
		r.s.Arm("ds.after_commit:ev-c2", 1)
		droppedAck := false
		r.s.Bus.SetDropRule(func(m *sim.Msg) bool {
			if !droppedAck && m.Kind == "ack" && m.ID == "ev-c1" {
				droppedAck = true
				return true
			}
			return false
		})
		r.run(t, 5000)
		var b strings.Builder
		for _, x := range r.s.Traces() {
			if x.Msg != nil {
				fmt.Fprintf(&b, "%d %s %s %s\n", x.Tick, x.Kind, x.Msg.Kind, x.Msg.ID)
			} else {
				fmt.Fprintf(&b, "%d %s %s %s\n", x.Tick, x.Kind, x.Node, x.Label)
			}
		}
		return b.String()
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("traces differ for same seed")
	}
}

func idOf(i int) string { return "c" + itoa(i) }
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
