package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// Channel 标识一条可能产生外部可观察输出的通道。
type Channel string

const (
	// ChannelResult 结果构造通道：结果对象在内存中构造。
	ChannelResult Channel = "result"
	// ChannelSend 网络发送通道：向客户端连接写入字节。
	ChannelSend Channel = "send"
	// ChannelCache 共享缓存通道：向多租户共享缓存发布结果。
	ChannelCache Channel = "cache"
	// ChannelCallback 外部回调通道：调用外部 webhook/回调。
	ChannelCallback Channel = "callback"
)

// RequiredChannels 返回退款证明必须覆盖的全部输出通道。
// 缺少任一通道的证据时，资金必须保持占用。
func RequiredChannels() []Channel {
	return []Channel{ChannelResult, ChannelSend, ChannelCache, ChannelCallback}
}

// EventKind 是执行器可记录的事件类型。
type EventKind string

const (
	EventResultConstructed EventKind = "result_constructed"
	EventBytesSent         EventKind = "bytes_sent"
	EventCachePublished    EventKind = "cache_published"
	EventCallbackInvoked   EventKind = "callback_invoked"
	// EventChannelQuiet 是执行器对某通道“全程无输出”的显式证言，
	// 使该通道的证据覆盖成立而不引入副作用。
	EventChannelQuiet EventKind = "channel_quiet"
)

// Event 是执行器追加到证据日志中的一条记录。
type Event struct {
	Seq     int64
	Kind    EventKind
	Channel Channel
	Bytes   int64  // 仅 EventBytesSent 使用
	Err     string // 仅 EventCallbackInvoked 使用：回调异常信息
	At      time.Time
}

// digest 返回该事件在证据链哈希中的编码。
func (e Event) digest() [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(e.Seq))
	h.Write(buf[:])
	h.Write([]byte(e.Kind))
	h.Write([]byte{0})
	h.Write([]byte(e.Channel))
	h.Write([]byte{0})
	binary.BigEndian.PutUint64(buf[:], uint64(e.Bytes))
	h.Write(buf[:])
	h.Write([]byte(e.Err))
	h.Write([]byte{0})
	binary.BigEndian.PutUint64(buf[:], uint64(e.At.UnixNano()))
	h.Write(buf[:])
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// Recorder 是执行器侧的仅追加证据日志。
// 事件带单调序号并做链式哈希，删改事件会破坏摘要。
type Recorder struct {
	mu     sync.Mutex
	seq    int64
	events []Event
	chain  [32]byte
}

// NewRecorder 创建一个空证据日志。
func NewRecorder() *Recorder { return &Recorder{} }

// Record 追加一条事件并更新链式摘要。
func (r *Recorder) Record(kind EventKind, ch Channel, bytes int64, err string) Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e := Event{
		Seq:     r.seq,
		Kind:    kind,
		Channel: ch,
		Bytes:   bytes,
		Err:     err,
		At:      time.Now(),
	}
	d := e.digest()
	h := sha256.New()
	h.Write(r.chain[:])
	h.Write(d[:])
	copy(r.chain[:], h.Sum(nil))
	r.events = append(r.events, e)
	return e
}

// ChannelProof 是单个输出通道的证据摘要。
type ChannelProof struct {
	Channel    Channel
	Covered    bool // 执行器是否就该通道提交了证据
	EventCount int
	BytesSent  int64
	SideEffect bool // 该通道是否已发生外部可观察副作用
	Digest     [32]byte
}

// Proof 是提交给账本宿主的退款证明。
// 它必须覆盖 RequiredChannels 中的每一条通道。
type Proof struct {
	QueryID   string
	Channels  map[Channel]ChannelProof
	LogDigest [32]byte // 全量事件日志的链式摘要
}

// Prove 基于当前日志为 queryID 生成退款证明。
func (r *Recorder) Prove(queryID string) Proof {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := Proof{
		QueryID:   queryID,
		Channels:  make(map[Channel]ChannelProof, len(r.events)),
		LogDigest: r.chain,
	}
	for _, e := range r.events {
		cp := p.Channels[e.Channel]
		cp.Channel = e.Channel
		cp.Covered = true
		cp.EventCount++
		h := sha256.New()
		h.Write(cp.Digest[:])
		d := e.digest()
		h.Write(d[:])
		copy(cp.Digest[:], h.Sum(nil))
		switch e.Kind {
		case EventBytesSent:
			cp.BytesSent += e.Bytes
			if e.Bytes > 0 {
				// 已发出的字节不可收回，即使连接随后断开。
				cp.SideEffect = true
			}
		case EventCachePublished:
			// 共享缓存一旦发布即可能被其他租户观察到。
			cp.SideEffect = true
		case EventCallbackInvoked:
			// 外部回调已被调用即构成副作用，回调异常不代表未发生。
			cp.SideEffect = true
		case EventResultConstructed:
			// 结果构造本身不可外部观察，不计副作用，但必须被证据覆盖。
		case EventChannelQuiet:
			// 显式无输出证言：仅提供覆盖，不构成副作用。
		}
		p.Channels[e.Channel] = cp
	}
	return p
}

// Verdict 是账本宿主的退款判定结果。
type Verdict int

const (
	// VerdictHold 保持占用：不退款。
	VerdictHold Verdict = iota
	// VerdictRefund 允许退款。
	VerdictRefund
)

func (v Verdict) String() string {
	if v == VerdictRefund {
		return "refund"
	}
	return "hold"
}

// Decision 是判定结果及原因。
type Decision struct {
	QueryID string
	Verdict Verdict
	Reasons []string
}

// Request 描述一次待判定的查询。TimedOut 仅作记录：
// 超时不等于没有输出，不能作为退款依据。
type Request struct {
	QueryID  string
	TimedOut bool
}

// Adjudicator 由账本宿主调用，依据退款证明做判定。
type Adjudicator struct {
	required []Channel
}

// NewAdjudicator 创建使用默认必需通道集合的判定器。
func NewAdjudicator() *Adjudicator {
	return &Adjudicator{required: RequiredChannels()}
}

// Decide 判定是否退款。规则：
//  1. 任一必需通道缺失证据 → 保持占用；
//  2. 任一通道已发生副作用 → 保持占用；
//  3. 全部通道有证据且无副作用 → 退款。
// 请求是否超时不影响判定。
func (a *Adjudicator) Decide(req Request, p Proof) Decision {
	d := Decision{QueryID: req.QueryID, Verdict: VerdictHold}
	if p.QueryID != "" && req.QueryID != "" && p.QueryID != req.QueryID {
		d.Reasons = append(d.Reasons,
			fmt.Sprintf("proof query %q does not match request %q", p.QueryID, req.QueryID))
		return d
	}
	for _, ch := range a.required {
		cp, ok := p.Channels[ch]
		if !ok || !cp.Covered {
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("missing evidence for channel %q", ch))
			continue
		}
		if cp.SideEffect {
			d.Reasons = append(d.Reasons,
				fmt.Sprintf("side effect already observed on channel %q", ch))
		}
	}
	if len(d.Reasons) == 0 {
		d.Verdict = VerdictRefund
	}
	return d
}
