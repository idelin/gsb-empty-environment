package dictneg

import (
	"errors"
	"math/rand"
	"testing"
)

// TestShuffledEventReplay 把发送端真实产生的事件日志（含重复副本）
// 打乱顺序后重放给接收端，驱动层按“失败即重试”投递，最终两端必须
// 收敛到同一字典版本。
func TestShuffledEventReplay(t *testing.T) {
	dictA := []byte("shuffle-dict-A")
	dictB := []byte("shuffle-dict-B")
	digestA := SumDigest(dictA)
	digestB := SumDigest(dictB)

	// 发送端按正常流程产生事件日志；Ack 直接回喂以推进状态机。
	prop, err := NewProposer(ProposerConfig{Store: NewMemStore(), Name: "s"})
	if err != nil {
		t.Fatal(err)
	}
	installA, err := prop.BeginInstall(dictA)
	if err != nil {
		t.Fatal(err)
	}
	if err := prop.HandleEvent(Event{Kind: EventAck, Epoch: installA.Epoch, Digest: digestA}); err != nil {
		t.Fatal(err)
	}
	enableA, err := prop.Enable()
	if err != nil {
		t.Fatal(err)
	}
	installB, err := prop.BeginInstall(dictB)
	if err != nil {
		t.Fatal(err)
	}
	if err := prop.HandleEvent(Event{Kind: EventAck, Epoch: installB.Epoch, Digest: digestB}); err != nil {
		t.Fatal(err)
	}
	enableB, err := prop.Enable()
	if err != nil {
		t.Fatal(err)
	}

	// 每个事件复制两份后彻底打乱，模拟丢包重传与乱序。
	log := []Event{installA, enableA, installB, enableB}
	var shuffled []Event
	for _, ev := range log {
		shuffled = append(shuffled, ev, ev)
	}
	rng := rand.New(rand.NewSource(20261111))
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	acc, err := NewAcceptor(AcceptorConfig{Store: NewMemStore(), Codec: newSpyCodec(), Name: "s"})
	if err != nil {
		t.Fatal(err)
	}
	got, sink := newSink()
	_ = got

	// 重试驱动：过期重放直接丢弃，其余失败进入待重试队列。
	var backlog []Event
	for _, ev := range shuffled {
		if _, err := acc.HandleEvent(ev, sink); err != nil {
			if errors.Is(err, ErrStaleEvent) {
				continue
			}
			backlog = append(backlog, ev)
		}
	}
	for len(backlog) > 0 {
		var next []Event
		progress := false
		for _, ev := range backlog {
			if _, err := acc.HandleEvent(ev, sink); err != nil {
				if errors.Is(err, ErrStaleEvent) {
					continue // 已被另一份副本应用
				}
				next = append(next, ev)
				continue
			}
			progress = true
		}
		if !progress && len(next) == len(backlog) {
			t.Fatalf("replay stuck, %d events undeliverable", len(next))
		}
		backlog = next
	}

	if acc.ActiveDigest() != digestB {
		t.Fatalf("acceptor active = %s, want %s", acc.ActiveDigest(), digestB)
	}
	if acc.Epoch() != installB.Epoch {
		t.Fatalf("acceptor epoch = %d, want %d", acc.Epoch(), installB.Epoch)
	}
	if prop.ActiveDigest() != digestB {
		t.Fatalf("proposer active = %s, want %s", prop.ActiveDigest(), digestB)
	}
}

// TestShuffledFrameReplay 在协商完成后，把带重复副本的数据帧打乱
// 重放，验证接收端恰好按序交付一次。
func TestShuffledFrameReplay(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())
	inst, _ := e.deliverInstall([]byte("frame-shuffle-dict"))
	e.deliverEnable(e.mustEnable())

	const n = 10
	var frames []Frame
	for i := 0; i < n; i++ {
		f := e.sendFrames(uint64(i))[0]
		frames = append(frames, f, f) // 每帧一个重复副本
	}
	rng := rand.New(rand.NewSource(42))
	rng.Shuffle(len(frames), func(i, j int) { frames[i], frames[j] = frames[j], frames[i] })

	for _, f := range frames {
		if err := e.acceptor.HandleFrame(f, e.sink); err != nil {
			t.Fatalf("frame seq=%d: %v", f.Seq, err)
		}
	}
	if len(*e.got) != n {
		t.Fatalf("delivered %d frames, want %d", len(*e.got), n)
	}
	for i, f := range *e.got {
		if f.seq != uint64(i) || f.digest != inst.Digest {
			t.Fatalf("position %d = seq %d digest %s", i, f.seq, f.digest)
		}
	}
	if e.acceptor.StagedCount(inst.Digest) != 0 {
		t.Fatal("staging queue not drained")
	}
}
