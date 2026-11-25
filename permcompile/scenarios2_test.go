package permcompile

import (
	"testing"
	"time"
)

// TestCrossSessionReplay 跨会话重放：事件绑定会话与计划摘要，
// nonce 防重放；过期会话的读取与事件都被永久拒绝。
func TestCrossSessionReplay(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindChoice,
		On:   "pick",
		Kids: []*Node{
			{Kind: KindLeaf, Opt: "a", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 1}},
			{Kind: KindLeaf, Opt: "b", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 1}},
		},
	}}
	clock := &fakeClock{t: time.Unix(0, 0)}
	it := newInterp(t, plan, WithClock(clock.now), WithExpiry(time.Unix(100, 0)))

	good := it.decision("pick", "a")

	badSession := good
	badSession.Session = "sess-B"
	badSession.Nonce = it.nonce()
	it.ingestFail(badSession, ErrBindSession)

	badDigest := good
	badDigest.Digest = PlanDigest{}
	badDigest.Nonce = it.nonce()
	it.ingestFail(badDigest, ErrBindDigest)

	badAuth := good
	badAuth.Nonce = it.nonce()
	badAuth.Raw = []byte("tampered")
	prevAuth := it.rt.auth
	it.rt.auth = func(Event) bool { return false }
	it.ingestFail(badAuth, ErrUnauthenticated)
	it.rt.auth = prevAuth

	it.ingest(good)
	it.ingestFail(good, ErrReplay)

	clock.advance(200 * time.Second)
	it.ingestFail(it.decision("pick", "b"), ErrExpired)
	d := it.rt.Authorize(req("o", 1, 0, 1))
	if d.Verdict != VerdictDeny || d.Reason != "session expired" {
		t.Fatalf("expired read = %+v", d)
	}

	// 新会话使用相同计划：独立 nonce 空间，可重新执行。
	rt2 := NewRuntime(it.cp, "sess-B", func(ev Event) bool {
		return ev.Digest == it.cp.digest && ev.Session == "sess-B"
	})
	ev2 := Event{Kind: EventDecision, Session: "sess-B", Digest: it.cp.digest, Gate: "pick", Option: "b", Nonce: "n-1"}
	if err := rt2.Ingest(ev2); err != nil {
		t.Fatalf("fresh session ingest: %v", err)
	}
}

// TestSimultaneousCancel 同时取消：原子批次取消多个分支，
// 被取消叶子配额清空且永不复活；取消某选择项会强制消歧。
func TestSimultaneousCancel(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindParallel,
		Kids: []*Node{
			{Kind: KindLeaf, Gate: "g1", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 1}},
			{Kind: KindLeaf, Gate: "g2", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{10, 20}, MaxReads: 1}},
			{
				Kind: KindChoice, On: "pick",
				Kids: []*Node{
					{Kind: KindLeaf, Opt: "a", Gate: "g3", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{20, 30}, MaxReads: 1}},
					{Kind: KindLeaf, Opt: "b", Gate: "g4", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{30, 40}, MaxReads: 1}},
				},
			},
		},
	}}
	it := newInterp(t, plan)

	// 同时取消 g1 与选择项 g4：批次原子生效，选择被强制为 a。
	it.ingest(it.cancel("g1"), it.cancel("g4"))
	it.expectWorlds(1,
		"L0[canceled]b=Er=E",
		"L1[active]b=[10:20]r=[0:1]",
		"L2[pending]b=[20:30]r=[0:1]",
		"L3[canceled]b=Er=E")

	// 取消分支永不复活：对已取消门再次取消被拒。
	it.ingestFail(it.cancel("g1"), ErrCannotCancel)

	// g1 区间读取被拒；其余两个存活叶子可拼接 [10,30)。
	it.deny(req("o", 1, 0, 5))
	it.allow(req("o", 1, 10, 30))
}

// TestFallback 条件回退：主分支成功则回退分支关闭；
// 回退事件在主分支部分消费后触发，已消费配额保留且切换到回退分支。
func TestFallback(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindFallback, Gate: "fb",
		Kids: []*Node{
			leafGrant("o", 1, 0, 10, 2),
			leafGrant("o", 1, 100, 110, 1),
		},
	}}
	it := newInterp(t, plan)

	// 主分支部分读取。
	it.allow(req("o", 1, 0, 5))
	it.expectWorlds(1, "L0[active]b=[5:10]r=[1:2]", "L1[pending]b=[100:110]r=[0:1]")

	// 回退分支区域尚未可达：等待回退事件。
	waiting := it.wait(req("o", 1, 100, 110))
	if len(waiting) != 1 || waiting[0].Kind != EventFallback || waiting[0].Gate != "fb" {
		t.Fatalf("unexpected waits: %+v", waiting)
	}

	it.ingest(it.fallback("fb"))
	// 主分支取消（已消费区间不复用），回退分支激活。
	it.expectWorlds(1, "L0[canceled]b=Er=E", "L1[active]b=[100:110]r=[0:1]")
	it.allow(req("o", 1, 100, 110))

	// 回退后主分支永不开放。
	it.deny(req("o", 1, 5, 10))
}

// TestSequenceOrdering 顺序节点只暴露当前许可，满足部分先后约束。
// TestFallbackBeforeConsume 回退事件先到达时立即切换到回退分支，
// 主分支未被消费即关闭；主分支已完成后事件会被拒绝。
func TestFallbackBeforeConsume(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindFallback, Gate: "fb",
		Kids: []*Node{
			leafGrant("o", 1, 0, 10, 1),
			leafGrant("o", 1, 100, 110, 1),
		},
	}}
	it := newInterp(t, plan)

	it.ingest(it.fallback("fb"))
	it.expectWorlds(1, "L0[canceled]b=Er=E", "L1[active]b=[100:110]r=[0:1]")
	// 主分支永不开放，回退分支可读。
	it.deny(req("o", 1, 0, 10))
	it.allow(req("o", 1, 100, 110))

	// 主分支已完成的计划上，回退事件被拒绝。
	done := newInterp(t, plan)
	done.allow(req("o", 1, 0, 10))
	done.ingestFail(done.fallback("fb"), ErrGateSettled)
}

func TestSequenceOrdering(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindSequence,
		Kids: []*Node{
			leafGrant("o", 1, 0, 10, 1),
			leafGrant("o", 1, 10, 20, 1),
		},
	}}
	it := newInterp(t, plan)

	it.deny(req("o", 1, 10, 20))
	it.allow(req("o", 1, 0, 10))
	it.allow(req("o", 1, 10, 20))
}
