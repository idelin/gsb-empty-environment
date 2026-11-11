package dictneg

import (
	"errors"
	"testing"
)

// TestReplayDataBeforeEnable 验证启用事件之前到达的新字典帧被暂存，
// Enable 之后按序号一次性冲刷；安装期间旧版字典的流量不受影响。
func TestReplayDataBeforeEnable(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())

	instA, _ := e.deliverInstall([]byte("dict-A"))
	enA := e.mustEnable()
	e.deliverEnable(enA)

	// 安装字典 B，但先不启用；期间 B 的帧到达，只能暂存。
	instB, _ := e.deliverInstall([]byte("dict-B"))
	early := []Frame{
		{Digest: instB.Digest, Seq: 0, Payload: []byte("b0")},
		{Digest: instB.Digest, Seq: 1, Payload: []byte("b1")},
	}
	if err := e.acceptor.HandleFrame(early[0], e.sink); err != nil {
		t.Fatal(err)
	}
	if err := e.acceptor.HandleFrame(early[1], e.sink); err != nil {
		t.Fatal(err)
	}
	if len(*e.got) != 0 {
		t.Fatal("frames decoded before enable")
	}

	// A 的流量仍正常交付，证明安装期间旧版字典继续生效。
	e.recv(e.sendFrames(0, 1)...)
	if len(*e.got) != 2 {
		t.Fatalf("old-dict traffic stalled: %d frames", len(*e.got))
	}
	for _, f := range *e.got {
		if f.digest != instA.Digest {
			t.Fatalf("old frame bound to %s", f.digest)
		}
	}

	// 启用 B：暂存帧按序号冲刷，且由 B 的解压器处理。
	enableB := e.mustEnable()
	e.deliverEnable(enableB)
	if len(*e.got) != 4 {
		t.Fatalf("staged frames not flushed, got %d", len(*e.got))
	}
	if (*e.got)[2].seq != 0 || (*e.got)[3].seq != 1 {
		t.Fatalf("staged frames out of order: %d then %d", (*e.got)[2].seq, (*e.got)[3].seq)
	}
	if e.codec.decodes(instB.Digest) != 2 {
		t.Fatalf("dict B decode count = %d, want 2", e.codec.decodes(instB.Digest))
	}
}

// TestReplayFailedInstallKeepsOldVersion 验证摘要不符与存储失败时，
// 活动摘要保持旧版不变，失败安装被终止。
func TestReplayFailedInstallKeepsOldVersion(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())
	instA, _ := e.deliverInstall([]byte("dict-A"))
	enA := e.mustEnable()
	e.deliverEnable(enA)

	// 场景一：安装内容与声明摘要不一致。
	bad := Event{
		Kind: EventInstall, Epoch: 2,
		Digest: SumDigest([]byte("claimed")), Payload: []byte("actually different"),
	}
	reply, err := e.acceptor.HandleEvent(bad, e.sink)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	if reply.Kind != EventNack {
		t.Fatalf("want nack, got %s", reply.Kind)
	}
	if e.acceptor.ActiveDigest() != instA.Digest || e.acceptor.Epoch() != 1 {
		t.Fatal("acceptor state changed after failed install")
	}

	// 发送端处理 Nack：在途安装清空，活动摘要保留旧版。
	prop, err := NewProposer(ProposerConfig{Store: NewMemStore(), Name: "p2"})
	if err != nil {
		t.Fatal(err)
	}
	good, err := prop.BeginInstall([]byte("dict-X"))
	if err != nil {
		t.Fatal(err)
	}
	prop.state.Active = instA.Digest
	nack := Event{Kind: EventNack, Epoch: good.Epoch, Digest: good.Digest}
	if err := prop.HandleEvent(nack); !errors.Is(err, ErrInstallRejected) {
		t.Fatalf("nack handling: %v", err)
	}
	if prop.ActiveDigest() != instA.Digest {
		t.Fatal("proposer active changed after nack")
	}
	if !prop.Pending().IsZero() {
		t.Fatal("pending install not cleared after nack")
	}

	// 场景二：接收端存储故障，同样回 Nack 且不推进状态。
	failing := NewMemStore()
	failing.FailNext("dict", 1)
	accFail, err := NewAcceptor(AcceptorConfig{Store: failing, Codec: newSpyCodec(), Name: "af"})
	if err != nil {
		t.Fatal(err)
	}
	ev := Event{Kind: EventInstall, Epoch: 1, Digest: SumDigest([]byte("z")), Payload: []byte("z")}
	reply, err = accFail.HandleEvent(ev, e.sink)
	if !errors.Is(err, ErrInjectedFailure) || reply.Kind != EventNack {
		t.Fatalf("storage failure: reply=%s err=%v", reply.Kind, err)
	}
	if accFail.ActiveDigest() != ZeroDigest || accFail.Epoch() != 0 {
		t.Fatal("acceptor state changed on storage failure")
	}
}

// TestReplayStaleEpochDropped 验证过期 Epoch 的事件被丢弃，
// 不会把两端版本拉回旧字典。
func TestReplayStaleEpochDropped(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())
	instA, _ := e.deliverInstall([]byte("dict-A"))
	enA := e.mustEnable()
	e.deliverEnable(enA)
	instB, _ := e.deliverInstall([]byte("dict-B"))
	enB := e.mustEnable()
	e.deliverEnable(enB)

	if _, err := e.acceptor.HandleEvent(
		Event{Kind: EventInstall, Epoch: instA.Epoch, Digest: instA.Digest, Payload: []byte("dict-A")},
		e.sink,
	); !errors.Is(err, ErrStaleEvent) {
		t.Fatalf("stale install: %v", err)
	}
	if _, err := e.acceptor.HandleEvent(
		Event{Kind: EventEnable, Epoch: instA.Epoch, Digest: instA.Digest},
		e.sink,
	); !errors.Is(err, ErrStaleEvent) {
		t.Fatalf("stale enable: %v", err)
	}
	assertActive(t, e, instB.Digest)

	oldAck := Event{Kind: EventAck, Epoch: instA.Epoch, Digest: instA.Digest}
	if err := e.proposer.HandleEvent(oldAck); !errors.Is(err, ErrStaleEvent) {
		t.Fatalf("stale ack: %v", err)
	}
	if e.proposer.ActiveDigest() != instB.Digest {
		t.Fatal("proposer rolled back on stale ack")
	}
}

// TestReconnectResyncConfirmed 验证“已确认未启用”时掉电：
// 重连后 Resync 重放安装与启用，两端收敛到同一版本。
func TestReconnectResyncConfirmed(t *testing.T) {
	pStore, aStore := NewMemStore(), NewMemStore()
	e := newEndpoints(t, pStore, aStore)
	instA, _ := e.deliverInstall([]byte("dict-A"))
	enA := e.mustEnable()
	e.deliverEnable(enA)

	// 新字典完成安装与确认，但启用事件尚未送达接收端时双端掉电。
	instB, _ := e.deliverInstall([]byte("dict-B"))

	e.restart()
	if e.proposer.ActiveDigest() != instA.Digest {
		t.Fatal("proposer lost old active version")
	}
	if e.proposer.Pending().Digest != instB.Digest || !e.proposer.Pending().Confirmed {
		t.Fatal("confirmed pending install not recovered")
	}
	if e.acceptor.ActiveDigest() != instA.Digest {
		t.Fatal("acceptor active changed without enable")
	}

	// 重连重放：Install + Enable。
	events, err := e.proposer.Resync()
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventInstall || events[1].Kind != EventEnable {
		t.Fatalf("resync events = %v", events)
	}
	ack, err := e.acceptor.HandleEvent(events[0], e.sink)
	if err != nil {
		t.Fatalf("resync install: %v", err)
	}
	if ack.Kind != EventAck {
		t.Fatalf("resync install reply = %s", ack.Kind)
	}
	e.deliverEnable(events[1])
	assertActive(t, e, instB.Digest)

	// 再次重连：已完全收敛，无需任何重放，活动版本仍然一致。
	e.restart()
	events, err = e.proposer.Resync()
	if err != nil {
		t.Fatalf("second Resync: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("unexpected resync after convergence: %v", events)
	}
	assertActive(t, e, instB.Digest)
}

// TestReconnectResyncUnconfirmed 验证“已安装未确认”时掉电：
// 重连只重放安装，接收端幂等重发 Ack，流程重新走完。
func TestReconnectResyncUnconfirmed(t *testing.T) {
	pStore, aStore := NewMemStore(), NewMemStore()
	e := newEndpoints(t, pStore, aStore)

	install, err := e.proposer.BeginInstall([]byte("dict-C"))
	if err != nil {
		t.Fatal(err)
	}
	// 接收端已落盘并回 Ack，但 Ack 在网络上丢失、发送端未处理。
	if _, err := e.acceptor.HandleEvent(install, e.sink); err != nil {
		t.Fatal(err)
	}

	e.restart()
	events, err := e.proposer.Resync()
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventInstall {
		t.Fatalf("unconfirmed resync = %v", events)
	}

	// 接收端幂等处理重复安装并重发 Ack。
	ack2, err := e.acceptor.HandleEvent(events[0], e.sink)
	if err != nil {
		t.Fatalf("replayed install: %v", err)
	}
	if ack2.Kind != EventAck || ack2.Digest != install.Digest {
		t.Fatalf("idempotent ack failed: %s", ack2.Kind)
	}
	if err := e.proposer.HandleEvent(ack2); err != nil {
		t.Fatalf("recovered ack: %v", err)
	}
	enable, err := e.proposer.Enable()
	if err != nil {
		t.Fatal(err)
	}
	e.deliverEnable(enable)
	assertActive(t, e, install.Digest)
}

// TestReconnectEnableLost 验证启用事件本身丢失（发送端已持久化启用，
// 接收端从未收到）时，重连重放安装+启用链使两端收敛。
func TestReconnectEnableLost(t *testing.T) {
	pStore, aStore := NewMemStore(), NewMemStore()
	e := newEndpoints(t, pStore, aStore)
	instA, _ := e.deliverInstall([]byte("dict-A"))
	enA := e.mustEnable()
	e.deliverEnable(enA)

	// 安装并确认 B，发送端持久化启用，但启用事件不投递。
	e.deliverInstall([]byte("dict-B"))
	enableB := e.mustEnable()
	_ = enableB // 丢弃：模拟网络丢失

	e.restart()
	if e.proposer.ActiveDigest() == e.acceptor.ActiveDigest() {
		t.Fatal("expected divergence before resync")
	}
	events, err := e.proposer.Resync()
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("enable-lost resync = %v events, want 2", len(events))
	}
	ack, err := e.acceptor.HandleEvent(events[0], e.sink)
	if err != nil {
		t.Fatalf("replay install: %v", err)
	}
	if ack.Kind != EventAck {
		t.Fatalf("replay install reply = %s", ack.Kind)
	}
	e.deliverEnable(events[1])
	assertActive(t, e, e.proposer.ActiveDigest())
	if e.acceptor.ActiveDigest() == instA.Digest {
		t.Fatal("acceptor stuck on old dictionary")
	}
}

// TestPowerLossDuringInstall 枚举安装窗口内的多个掉电点，
// 重启后重放事件必须收敛，且任何时刻活动摘要都不发生错位。
func TestPowerLossDuringInstall(t *testing.T) {
	cases := []struct {
		name          string
		volatileDict  bool // 字典内容写入掉电丢失
		volatileState bool // 接收端状态写入掉电丢失
		failDict      int  // 字典写入硬失败次数
		failState     int  // 状态写入硬失败次数
	}{
		{name: "dict write vanishes", volatileDict: true},
		{name: "state write vanishes", volatileState: true},
		{name: "dict and state vanish", volatileDict: true, volatileState: true},
		{name: "dict write hard fails", failDict: 1},
		{name: "state write hard fails", failState: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pStore := NewMemStore()
			aStore := NewMemStore()
			e := newEndpoints(t, pStore, aStore)
			instA, _ := e.deliverInstall([]byte("dict-A"))
			enA := e.mustEnable()
			e.deliverEnable(enA)

			// 注入故障后发起第二次安装。
			aStore.VolatileDicts = tc.volatileDict
			aStore.VolatileState = tc.volatileState
			aStore.FailNext("dict", tc.failDict)
			aStore.FailNext("acceptor", tc.failState)

			install, err := e.proposer.BeginInstall([]byte("dict-D"))
			if err != nil {
				t.Fatalf("BeginInstall: %v", err)
			}
			reply, handleErr := e.acceptor.HandleEvent(install, e.sink)

			// 故障解除，双端掉电重启。
			aStore.VolatileDicts = false
			aStore.VolatileState = false
			e.restart()

			// 不变量：无论故障形态如何，接收端活动摘要仍是旧版。
			if e.acceptor.ActiveDigest() != instA.Digest {
				t.Fatalf("acceptor active = %s after crash, want %s",
					e.acceptor.ActiveDigest(), instA.Digest)
			}

			if handleErr != nil {
				// 硬失败：接收端回 Nack，发送端放弃本次安装，保留旧版。
				if reply.Kind != EventNack {
					t.Fatalf("want nack on hard failure, got %s", reply.Kind)
				}
				if err := e.proposer.HandleEvent(reply); !errors.Is(err, ErrInstallRejected) {
					t.Fatalf("nack handling: %v", err)
				}
				if e.proposer.ActiveDigest() != instA.Digest {
					t.Fatal("proposer active changed after nack")
				}
				return
			}

			// 易失写入：Ack 已发出但接收端没落盘。发送端已持久化确认，
			// 重连重放安装+启用链，接收端把安装当作新事件正常处理。
			if err := e.proposer.HandleEvent(reply); err != nil {
				t.Fatalf("proposer ack: %v", err)
			}
			e.mustEnable()
			e.restart() // 发送端启用已持久化，接收端仍未知
			events, err := e.proposer.Resync()
			if err != nil {
				t.Fatalf("Resync: %v", err)
			}
			if len(events) != 2 {
				t.Fatalf("resync after volatile crash = %v", events)
			}
			ack, err := e.acceptor.HandleEvent(events[0], e.sink)
			if err != nil {
				t.Fatalf("replay install: %v", err)
			}
			if ack.Kind != EventAck {
				t.Fatalf("replay install reply = %s", ack.Kind)
			}
			e.deliverEnable(events[1])
			assertActive(t, e, install.Digest)
		})
	}
}
