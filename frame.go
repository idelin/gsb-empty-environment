package netsec

import "sync"

// FrameType 区分主帧与子帧,能力登记可以按帧类型收窄授权范围。
type FrameType int

const (
	// FrameMain 是浏览器标签页的顶层主帧。
	FrameMain FrameType = iota
	// FrameSubframe 是主帧下的任意嵌套 iframe 帧。
	FrameSubframe
)

// Generation 标识帧内的一次导航代次。每次成功导航都会使代次单调递增,
// 旧代次中捕获的回调因此无法在新文档里继续触达原生函数。
type Generation uint64

// Frame 表示浏览器中的一个主帧或子帧,并持有该帧当前文档的安全状态。
//
// Frame 的方法可被宿主的 IO 线程与 JS 绑定线程并发调用,内部自带互斥保护。
type Frame struct {
	mu         sync.RWMutex
	id         string
	frameType  FrameType
	origin     Origin
	generation Generation
}

// NewFrame 创建一个帧。新帧尚未加载文档,初始导航代次为 1,
// 有效来源为不透明来源(等价于初始 about:blank 且无可信发起者)。
func NewFrame(id string, frameType FrameType) *Frame {
	return &Frame{
		id:         id,
		frameType:  frameType,
		origin:     OpaqueOrigin(),
		generation: 1,
	}
}

// ID 返回构造时分配的帧标识。
func (f *Frame) ID() string {
	return f.id
}

// Type 返回帧类型(主帧/子帧)。
func (f *Frame) Type() FrameType {
	return f.frameType
}

// Generation 返回帧当前的导航代次。
func (f *Frame) Generation() Generation {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.generation
}

// Origin 返回帧当前文档的有效来源。
func (f *Frame) Origin() Origin {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.origin
}

// Navigate 提交一次导航:重新计算有效来源并把导航代次加一,
// 从而作废旧文档中绑定的全部回调。返回新文档的有效来源。
func (f *Frame) Navigate(req NavigationRequest) Origin {
	origin := ResolveEffectiveOrigin(req)
	f.mu.Lock()
	f.origin = origin
	f.generation++
	f.mu.Unlock()
	return origin
}

// Snapshot 返回帧当前安全状态的只读快照。能力表在单次复核期间
// 基于快照判定,避免来源与代次在检查过程中被并发导航撕裂。
func (f *Frame) Snapshot() FrameSnapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return FrameSnapshot{
		FrameID:    f.id,
		Type:       f.frameType,
		Origin:     f.origin,
		Generation: f.generation,
	}
}

// FrameSnapshot 是帧安全状态在某一时刻的一致拷贝。
type FrameSnapshot struct {
	FrameID    string
	Type       FrameType
	Origin     Origin
	Generation Generation
}
