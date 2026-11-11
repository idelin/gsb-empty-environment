package dictneg

import (
	"errors"
	"fmt"
)

// AcceptorConfig 是接收端的配置。
type AcceptorConfig struct {
	// Store 持久化已确认字典与协商状态，必填。
	Store Store
	// Codec 按字典内容构造解压器，必填。
	Codec Codec
	// Name 是该角色在 Store 中的状态命名空间。
	Name string
	// MaxStagedFrames 是每个非活动摘要最多暂存的帧数；<=0 取默认值。
	MaxStagedFrames int
	// MaxStagedBytes 是所有非活动摘要暂存的总字节上限；<=0 取默认值。
	MaxStagedBytes int
}

const (
	defaultMaxStagedFrames = 1024
	defaultMaxStagedBytes  = 16 << 20
)

// Sink 接收按序号重排后的解压数据。
type Sink func(digest Digest, seq uint64, plain []byte)

// Acceptor 是字典协商的接收端（压缩数据的消费方）。
//
// 不变量：
//
//   - 只有 EventEnable 能改变活动摘要；
//   - 数据帧只能用其头部摘要对应的字典解压，未知摘要只暂存、绝不猜测；
//   - 暂存队列有帧数与总字节双重上界，超限拒绝新帧而不是淘汰旧帧。
type Acceptor struct {
	store        Store
	codec        Codec
	name         string
	maxFrames    int
	maxBytes     int
	state        AcceptorState
	activeDecomp Decompressor

	// staging 按摘要保存尚未连续交付的帧队列；活动摘要的乱序帧
	// 也通过同样的队列结构做序号重排。
	staging map[Digest]*frameQueue
}

// NewAcceptor 从 Store 恢复接收端，并用已持久确认的活动字典重建解压器。
func NewAcceptor(cfg AcceptorConfig) (*Acceptor, error) {
	if cfg.Store == nil {
		return nil, errors.New("dictneg: nil store")
	}
	if cfg.Codec == nil {
		return nil, errors.New("dictneg: nil codec")
	}
	name := cfg.Name
	if name == "" {
		name = "default"
	}
	maxFrames := cfg.MaxStagedFrames
	if maxFrames <= 0 {
		maxFrames = defaultMaxStagedFrames
	}
	maxBytes := cfg.MaxStagedBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxStagedBytes
	}
	state, err := cfg.Store.LoadAcceptorState(name)
	if err != nil {
		return nil, fmt.Errorf("dictneg: load acceptor state: %w", err)
	}
	if state == nil {
		state = &AcceptorState{NextSeq: map[string]uint64{}}
	}
	if state.NextSeq == nil {
		state.NextSeq = map[string]uint64{}
	}
	a := &Acceptor{
		store:     cfg.Store,
		codec:     cfg.Codec,
		name:      name,
		maxFrames: maxFrames,
		maxBytes:  maxBytes,
		state:     *state,
		staging:   map[Digest]*frameQueue{},
	}
	if !a.state.Active.IsZero() {
		if err := a.openActive(a.state.Active); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// ActiveDigest 返回当前用于解码的字典摘要。
func (a *Acceptor) ActiveDigest() Digest {
	return a.state.Active
}

// Epoch 返回已处理的最大事件 Epoch。
func (a *Acceptor) Epoch() uint64 {
	return a.state.Epoch
}

// StagedCount 返回某摘要当前暂存的帧数（测试/可观测性用）。
func (a *Acceptor) StagedCount(d Digest) int {
	q := a.staging[d]
	if q == nil {
		return 0
	}
	return q.size()
}

// StagedBytes 返回所有非活动摘要暂存的总字节数。
func (a *Acceptor) StagedBytes() int {
	total := 0
	for d, q := range a.staging {
		if d != a.state.Active {
			total += q.bytes()
		}
	}
	return total
}

// HandleEvent 处理发送端的协商事件，返回需要回传给发送端的确认事件。
//
// 过期 Epoch 的事件返回 ErrStaleEvent，驱动层据此丢弃旧重放；同一
// Epoch 的重复 Install/Enable 幂等重发 Ack（修复 Ack 丢失）。Enable
// 成功后会用 sink 立即冲刷该摘要下已暂存的数据帧。全新（无任何持久
// 状态）的接收端直接采纳首个事件的 Epoch，完成会话引导。
func (a *Acceptor) HandleEvent(ev Event, sink Sink) (Event, error) {
	if a.virgin() && ev.Epoch >= 1 {
		// 全新接收端：没有对端 Epoch 的先验，直接采纳，避免状态文件
		// 丢失后永远无法重连。
	} else if ev.Epoch < a.state.Epoch {
		return Event{}, fmt.Errorf("%w: epoch %d < %d", ErrStaleEvent, ev.Epoch, a.state.Epoch)
	} else if ev.Epoch > a.state.Epoch+1 {
		return Event{}, fmt.Errorf("%w: epoch gap %d -> %d", ErrInvalidWire, a.state.Epoch, ev.Epoch)
	}

	switch ev.Kind {
	case EventInstall:
		// 同 Epoch 重复 Install：内容已安装且确实在库则幂等重发 Ack，
		// 便于对端在 Ack 丢失后靠重试恢复；若状态在库而内容丢失
		// （掉电造成的持久化空洞），按新安装重新落盘内容。
		if ev.Epoch == a.state.Epoch && a.hasInstalled(ev.Digest) && a.store.HasDictionary(ev.Digest) {
			return Event{Kind: EventAck, Epoch: ev.Epoch, Digest: ev.Digest}, nil
		}
		return a.handleInstall(ev)
	case EventEnable:
		if ev.Epoch == a.state.EnabledEpoch && ev.Digest == a.state.Active {
			// 重复 Enable：幂等重发确认，修复启用确认丢失。
			return Event{Kind: EventAck, Epoch: ev.Epoch, Digest: ev.Digest}, nil
		}
		if ev.Epoch == a.state.EnabledEpoch && ev.Digest != a.state.Active {
			// 同一 Epoch 不可能有两个不同的启用目标。
			return Event{}, fmt.Errorf("%w: conflicting enable at epoch %d", ErrInvalidWire, ev.Epoch)
		}
		if err := a.handleEnable(ev); err != nil {
			return Event{}, err
		}
		a.flush(ev.Digest, sink)
		return Event{Kind: EventAck, Epoch: ev.Epoch, Digest: ev.Digest}, nil
	case EventAck, EventNack:
		return Event{}, fmt.Errorf("%w: acceptor does not handle %s", ErrInvalidWire, ev.Kind)
	default:
		return Event{}, fmt.Errorf("%w: unknown event kind", ErrInvalidWire)
	}
}

func (a *Acceptor) handleInstall(ev Event) (Event, error) {
	nack := func(cause error) (Event, error) {
		// Nack 不推进 Epoch、不改变活动摘要：旧版字典继续生效。
		return Event{Kind: EventNack, Epoch: ev.Epoch, Digest: ev.Digest}, cause
	}
	if SumDigest(ev.Payload) != ev.Digest {
		return nack(ErrDigestMismatch)
	}
	if err := a.store.PutDictionary(ev.Digest, ev.Payload); err != nil {
		return nack(err)
	}
	a.state.Epoch = ev.Epoch
	a.addInstalled(ev.Digest)
	if err := a.store.SaveAcceptorState(a.name, &a.state); err != nil {
		// 状态没落盘等同于安装未发生：回 Nack 让对端放弃或重试。
		return nack(err)
	}
	return Event{Kind: EventAck, Epoch: ev.Epoch, Digest: ev.Digest}, nil
}

func (a *Acceptor) handleEnable(ev Event) error {
	if !a.hasInstalled(ev.Digest) {
		// 绝不启用未持久安装的字典。
		return fmt.Errorf("%w: digest %s", ErrUnknownDictionary, ev.Digest)
	}
	if err := a.openActive(ev.Digest); err != nil {
		return err
	}
	a.state.Epoch = ev.Epoch
	a.state.EnabledEpoch = ev.Epoch
	a.state.Active = ev.Digest
	return a.store.SaveAcceptorState(a.name, &a.state)
}

// virgin 报告接收端是否从未持久化过任何协商状态。
func (a *Acceptor) virgin() bool {
	return a.state.Epoch == 0 && a.state.Active.IsZero() && len(a.state.Installed) == 0
}

// HandleFrame 处理一帧绑定字典摘要的数据帧。
//
// 活动摘要且序号连续的帧立即解压并交付 sink；活动摘要的乱序帧、以及
// 非活动（已安装或完全未知）摘要的帧进入有界暂存。未知摘要不会被解码，
// 只会等待后续 Install/Enable；超过任一容量上界返回错误并拒绝该帧。
func (a *Acceptor) HandleFrame(f Frame, sink Sink) error {
	if sink == nil {
		return errors.New("dictneg: nil sink")
	}
	if f.Digest.IsZero() && a.state.Active.IsZero() {
		// 无活动字典时的零摘要帧无处可去：拒绝而不是无限暂存。
		return ErrNoActiveDictionary
	}
	if f.Digest != a.state.Active {
		return a.stage(f)
	}
	if err := a.stage(f); err != nil {
		return err
	}
	a.flush(a.state.Active, sink)
	return nil
}

// Flush 让宿主在重放完一批事件后显式冲刷活动摘要下可交付的暂存帧。
func (a *Acceptor) Flush(sink Sink) {
	if a.state.Active.IsZero() {
		return
	}
	a.flush(a.state.Active, sink)
}

func (a *Acceptor) stage(f Frame) error {
	// 活动摘要下序号落后于交付进度的帧是迟到的重传副本，直接丢弃。
	if f.Digest == a.state.Active && f.Seq < a.state.NextSeq[f.Digest.String()] {
		return nil
	}
	q := a.staging[f.Digest]
	if q == nil {
		q = newFrameQueue()
		a.staging[f.Digest] = q
	}
	if f.Digest != a.state.Active {
		if q.size() >= a.maxFrames {
			return fmt.Errorf("%w: digest %s", ErrBufferFull, f.Digest)
		}
		if a.StagedBytes()+len(f.Payload) > a.maxBytes {
			return fmt.Errorf("%w: digest %s", ErrBufferLimit, f.Digest)
		}
	}
	return q.push(f)
}

// flush 按记录的 NextSeq 连续交付活动摘要的帧；缺号即停，等待重传。
// 解压失败时保留该帧并停止，绝不换字典重试或猜测解码。
func (a *Acceptor) flush(d Digest, sink Sink) {
	q := a.staging[d]
	if q == nil || a.activeDecomp == nil {
		return
	}
	next := a.state.NextSeq[d.String()]
	dirty := false
	for {
		f, ok := q.pop(next)
		if !ok {
			break
		}
		plain, err := a.activeDecomp.Decompress(f.Payload)
		if err != nil {
			q.pushFront(f)
			break
		}
		sink(d, f.Seq, plain)
		next = f.Seq + 1
		dirty = true
	}
	if dirty {
		a.state.NextSeq[d.String()] = next
		_ = a.store.SaveAcceptorState(a.name, &a.state)
	}
}

// openActive 用持久化的字典内容构造新的活动解压器。
func (a *Acceptor) openActive(d Digest) error {
	var content []byte
	if !d.IsZero() {
		var err error
		content, err = a.store.GetDictionary(d)
		if err != nil {
			return err
		}
	}
	decomp, err := a.codec.Open(content)
	if err != nil {
		return err
	}
	a.activeDecomp = decomp
	return nil
}

func (a *Acceptor) hasInstalled(d Digest) bool {
	for _, x := range a.state.Installed {
		if x == d {
			return true
		}
	}
	return false
}

func (a *Acceptor) addInstalled(d Digest) {
	if a.hasInstalled(d) {
		return
	}
	a.state.Installed = append(a.state.Installed, d)
}

// RemoveDictionary 在宿主确认某摘要永不复用后回收其暂存队列。
// 活动字典不允许删除；FileStore 中的字典文件由宿主自行回收。
func (a *Acceptor) RemoveDictionary(d Digest) error {
	if d == a.state.Active {
		return errors.New("dictneg: cannot remove active dictionary")
	}
	delete(a.staging, d)
	return nil
}

// frameQueue 是按 Seq 索引的暂存队列，支持缺号等待与重复去重。
type frameQueue struct {
	frames map[uint64]Frame
	seqs   []uint64
	nbytes int
}

func newFrameQueue() *frameQueue {
	return &frameQueue{frames: map[uint64]Frame{}}
}

func (q *frameQueue) push(f Frame) error {
	if _, dup := q.frames[f.Seq]; dup {
		return nil // 重复帧：幂等保留先到者
	}
	q.frames[f.Seq] = f
	q.seqs = append(q.seqs, f.Seq)
	q.nbytes += len(f.Payload)
	return nil
}

func (q *frameQueue) pushFront(f Frame) {
	if _, ok := q.frames[f.Seq]; ok {
		return
	}
	q.frames[f.Seq] = f
	q.seqs = append(q.seqs, f.Seq)
	q.nbytes += len(f.Payload)
}

func (q *frameQueue) pop(want uint64) (Frame, bool) {
	f, ok := q.frames[want]
	if !ok {
		return Frame{}, false
	}
	delete(q.frames, want)
	q.nbytes -= len(f.Payload)
	remaining := q.seqs[:0]
	for _, s := range q.seqs {
		if s != want {
			remaining = append(remaining, s)
		}
	}
	q.seqs = remaining
	return f, true
}

func (q *frameQueue) size() int  { return len(q.seqs) }
func (q *frameQueue) bytes() int { return q.nbytes }
