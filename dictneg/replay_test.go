package dictneg

import (
	"errors"
	"fmt"
	"testing"
)

// endpoints 把一对 Proposer/Acceptor 与各自的持久存储绑在一起，
// 模拟双端会话宿主。
type endpoints struct {
	t        *testing.T
	pStore   Store
	aStore   Store
	codec    *spyCodec
	proposer *Proposer
	acceptor *Acceptor
	sink     Sink
	got      *[]deliveredFrame
}

func newEndpoints(t *testing.T, pStore, aStore Store) *endpoints {
	t.Helper()
	codec := newSpyCodec()
	prop, err := NewProposer(ProposerConfig{Store: pStore, Name: "s1"})
	if err != nil {
		t.Fatalf("NewProposer: %v", err)
	}
	acc, err := NewAcceptor(AcceptorConfig{Store: aStore, Codec: codec, Name: "s1"})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}
	got, sink := newSink()
	return &endpoints{
		t: t, pStore: pStore, aStore: aStore, codec: codec,
		proposer: prop, acceptor: acc, sink: sink, got: got,
	}
}

// restart 模拟进程掉电重启：从同一批 Store 重新构造两个角色，内存态
// （暂存队列、解压器）全部丢失，只剩持久化状态。
func (e *endpoints) restart() {
	e.t.Helper()
	prop, err := NewProposer(ProposerConfig{Store: e.pStore, Name: "s1"})
	if err != nil {
		e.t.Fatalf("restart proposer: %v", err)
	}
	acc, err := NewAcceptor(AcceptorConfig{Store: e.aStore, Codec: e.codec, Name: "s1"})
	if err != nil {
		e.t.Fatalf("restart acceptor: %v", err)
	}
	e.proposer = prop
	e.acceptor = acc
}

// deliverInstall 完成一次“安装 -> 确认”往返；启用事件由 mustEnable
// 与 deliverEnable 分开驱动，便于测试精确控制各事件之间的时序。
func (e *endpoints) deliverInstall(content []byte) (Event, Event) {
	e.t.Helper()
	install, err := e.proposer.BeginInstall(content)
	if err != nil {
		e.t.Fatalf("BeginInstall: %v", err)
	}
	ack, err := e.acceptor.HandleEvent(install, e.sink)
	if err != nil {
		e.t.Fatalf("acceptor install: %v", err)
	}
	if ack.Kind != EventAck || ack.Digest != install.Digest {
		e.t.Fatalf("want ack for %s, got %s", install.Digest, ack.Kind)
	}
	if err := e.proposer.HandleEvent(ack); err != nil {
		e.t.Fatalf("proposer ack: %v", err)
	}
	return install, ack
}

// mustEnable 让发送端生成独立的启用事件（尚未投递给接收端）。
func (e *endpoints) mustEnable() Event {
	e.t.Helper()
	enable, err := e.proposer.Enable()
	if err != nil {
		e.t.Fatalf("Enable: %v", err)
	}
	return enable
}

func (e *endpoints) deliverEnable(ev Event) {
	e.t.Helper()
	reply, err := e.acceptor.HandleEvent(ev, e.sink)
	if err != nil {
		e.t.Fatalf("acceptor enable: %v", err)
	}
	if reply.Kind != EventAck || reply.Digest != ev.Digest {
		e.t.Fatalf("enable ack = %s/%s, want ack/%s", reply.Kind, reply.Digest, ev.Digest)
	}
	if err := e.proposer.HandleEvent(reply); err != nil {
		e.t.Fatalf("proposer enable ack: %v", err)
	}
}

func (e *endpoints) sendFrames(seq ...uint64) []Frame {
	e.t.Helper()
	frames := make([]Frame, 0, len(seq))
	for _, s := range seq {
		payload := []byte(fmt.Sprintf("payload-%d", s))
		f, err := e.proposer.WrapFrame(s, payload)
		if err != nil {
			e.t.Fatalf("WrapFrame: %v", err)
		}
		frames = append(frames, f)
	}
	return frames
}

func (e *endpoints) recv(frames ...Frame) {
	e.t.Helper()
	for _, f := range frames {
		if err := e.acceptor.HandleFrame(f, e.sink); err != nil {
			e.t.Fatalf("HandleFrame seq=%d: %v", f.Seq, err)
		}
	}
}

func assertActive(t *testing.T, e *endpoints, d Digest) {
	t.Helper()
	if e.proposer.ActiveDigest() != d {
		t.Fatalf("proposer active = %s, want %s", e.proposer.ActiveDigest(), d)
	}
	if e.acceptor.ActiveDigest() != d {
		t.Fatalf("acceptor active = %s, want %s", e.acceptor.ActiveDigest(), d)
	}
}

// TestReplayFullNegotiation 覆盖三阶段协商的完整生命周期。
func TestReplayFullNegotiation(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())

	dictA := []byte("dictionary-alpha")
	install, ack := e.deliverInstall(dictA)
	enable := e.mustEnable()
	digestA := install.Digest

	if install.Epoch != 1 || ack.Epoch != 1 || enable.Epoch != 1 {
		t.Fatalf("epoch drift: install=%d ack=%d enable=%d", install.Epoch, ack.Epoch, enable.Epoch)
	}
	e.deliverEnable(enable)
	assertActive(t, e, digestA)

	// 活动字典上的数据帧立即解压，且确实由该字典的解压器处理。
	e.recv(e.sendFrames(0, 1, 2)...)
	if len(*e.got) != 3 {
		t.Fatalf("delivered %d frames, want 3", len(*e.got))
	}
	for i, f := range *e.got {
		if f.digest != digestA || f.seq != uint64(i) {
			t.Fatalf("frame %d = %s/%d, want %s/%d", i, f.digest, f.seq, digestA, i)
		}
	}
	if e.codec.decodes(digestA) != 3 {
		t.Fatalf("decode count = %d, want 3", e.codec.decodes(digestA))
	}

	// 再装第二个字典，验证版本继续推进且旧版不受影响。
	dictB := []byte("dictionary-bravo")
	installB, _ := e.deliverInstall(dictB)
	enableB := e.mustEnable()
	if installB.Epoch != 2 {
		t.Fatalf("second epoch = %d, want 2", installB.Epoch)
	}
	e.deliverEnable(enableB)
	assertActive(t, e, installB.Digest)
	if e.acceptor.ActiveDigest() == digestA {
		t.Fatal("active digest did not advance")
	}
}

// TestReplayDuplicateEvents 验证三个事件与数据帧重复投递都是幂等的。
func TestReplayDuplicateEvents(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())

	install, _ := e.deliverInstall([]byte("dup-dict"))

	// 重复 Install：幂等重发同一个 Ack，Epoch 不重复推进。
	dupAck, err := e.acceptor.HandleEvent(install, e.sink)
	if err != nil {
		t.Fatalf("duplicate install: %v", err)
	}
	if dupAck.Kind != EventAck || dupAck.Epoch != install.Epoch {
		t.Fatalf("duplicate install reply = %s epoch %d", dupAck.Kind, dupAck.Epoch)
	}
	if e.acceptor.Epoch() != 1 {
		t.Fatalf("epoch advanced on duplicate install: %d", e.acceptor.Epoch())
	}

	// 重复 Ack：发送端幂等忽略。
	if err := e.proposer.HandleEvent(dupAck); err != nil {
		t.Fatalf("duplicate ack: %v", err)
	}

	enable := e.mustEnable()
	e.deliverEnable(enable)

	// 重复 Enable：幂等重发确认，活动摘要不变。
	reply, err := e.acceptor.HandleEvent(enable, e.sink)
	if err != nil {
		t.Fatalf("duplicate enable: %v", err)
	}
	if reply.Kind != EventAck || reply.Digest != enable.Digest {
		t.Fatalf("duplicate enable reply = %s", reply.Kind)
	}
	if err := e.proposer.HandleEvent(reply); err != nil {
		t.Fatalf("duplicate enable ack: %v", err)
	}

	// 重复数据帧：只交付一次。
	f := e.sendFrames(0)[0]
	if err := e.acceptor.HandleFrame(f, e.sink); err != nil {
		t.Fatalf("frame: %v", err)
	}
	if err := e.acceptor.HandleFrame(f, e.sink); err != nil {
		t.Fatalf("duplicate frame: %v", err)
	}
	if len(*e.got) != 1 || (*e.got)[0].seq != 0 {
		t.Fatalf("duplicate frame delivered %d times", len(*e.got))
	}
}

// TestReplayOutOfOrderFrames 验证乱序帧按序号重排，缺号等待。
func TestReplayOutOfOrderFrames(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())
	install, _ := e.deliverInstall([]byte("order-dict"))
	enable := e.mustEnable()
	e.deliverEnable(enable)

	// 先到 2、1，再到 0：0 到达前不能交付任何帧。
	e.recv(e.sendFrames(2, 1)...)
	if len(*e.got) != 0 {
		t.Fatalf("frames delivered before gap filled: %d", len(*e.got))
	}
	if e.acceptor.StagedCount(install.Digest) != 2 {
		t.Fatalf("staged = %d, want 2", e.acceptor.StagedCount(install.Digest))
	}

	e.recv(e.sendFrames(0)[0])
	if len(*e.got) != 3 {
		t.Fatalf("delivered = %d, want 3", len(*e.got))
	}
	for i, f := range *e.got {
		if f.seq != uint64(i) {
			t.Fatalf("position %d got seq %d", i, f.seq)
		}
	}

	// 继续出现缺号：4 先到，等待 3。
	e.recv(e.sendFrames(4)[0])
	if len(*e.got) != 3 {
		t.Fatal("frame delivered across new gap")
	}
	e.recv(e.sendFrames(3)[0])
	if len(*e.got) != 5 || (*e.got)[4].seq != 4 {
		t.Fatalf("gap fill failed, got %d frames", len(*e.got))
	}
}

// TestReplayUnknownDigestBounded 验证未知摘要只暂存不解码，且有界。
func TestReplayUnknownDigestBounded(t *testing.T) {
	e := newEndpoints(t, NewMemStore(), NewMemStore())
	e.deliverInstall([]byte("known-dict"))
	e.deliverEnable(e.mustEnable())

	unknown := SumDigest([]byte("some-future-dictionary"))
	unknownFrame := Frame{Digest: unknown, Seq: 0, Payload: []byte("x")}

	// 未知摘要：入队暂存，但绝不能触发任何解压。
	if err := e.acceptor.HandleFrame(unknownFrame, e.sink); err != nil {
		t.Fatalf("stage unknown: %v", err)
	}
	if e.acceptor.StagedCount(unknown) != 1 {
		t.Fatalf("unknown staged = %d", e.acceptor.StagedCount(unknown))
	}
	if len(*e.got) != 0 {
		t.Fatal("unknown-digest frame was decoded")
	}
	if e.codec.decodes(unknown) != 0 {
		t.Fatalf("unknown digest decoded %d times", e.codec.decodes(unknown))
	}

	// 小容量接收端：超过每摘要帧数上限必须拒绝，而不是淘汰旧帧。
	small, err := NewAcceptor(AcceptorConfig{
		Store: NewMemStore(), Codec: newSpyCodec(), Name: "small",
		MaxStagedFrames: 1, MaxStagedBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := small.HandleFrame(unknownFrame, e.sink); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	err = small.HandleFrame(Frame{Digest: unknown, Seq: 1, Payload: []byte("y")}, e.sink)
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("want ErrBufferFull, got %v", err)
	}

	// 总字节上限同样生效。
	tight, err := NewAcceptor(AcceptorConfig{
		Store: NewMemStore(), Codec: newSpyCodec(), Name: "tight",
		MaxStagedFrames: 100, MaxStagedBytes: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tight.HandleFrame(unknownFrame, e.sink); err != nil {
		t.Fatalf("first tight stage: %v", err)
	}
	err = tight.HandleFrame(Frame{Digest: unknown, Seq: 1, Payload: []byte("toolong")}, e.sink)
	if !errors.Is(err, ErrBufferLimit) {
		t.Fatalf("want ErrBufferLimit, got %v", err)
	}
}
