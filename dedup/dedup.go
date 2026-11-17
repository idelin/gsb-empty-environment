// Package dedup 为基于 UDP 的可靠请求/响应服务提供请求去重与响应重放隔离。
//
// 协议服务在收到重传的请求报文时，既不能重复执行昂贵的处理逻辑，也不能把
// 已经发出的大响应重新发送给一个未经验证的来源。每个请求由三个要素构成
// “验证身份”（见 Identity）：
//
//   - AddrGen：来源地址验证代次。来源地址每次重新通过验证（例如地址所有权
//     校验）后递增；未通过验证的来源使用不与任何有效代次冲突的临时代次。
//   - Digest：请求正文摘要，通常为正文的哈希值。
//   - Seq：客户端分配的请求序号。
//
// 只有三个要素全部相同才视为同一请求：
//
//   - 正在处理中的同一请求只执行一次处理器，后续重传作为等待者合并，接收与
//     首个请求完全相同的响应分片流；
//   - 处理完成后，响应按身份缓存，只允许向同一验证身份重放；地址代次或摘要
//     不同的请求会重新执行处理器而不是获得缓存；
//   - 某个等待者取消自己的 Stream（例如其请求上下文超时）只会关闭它自己的
//     事件流，不会取消处理器，也不会影响其他等待者。
//
// 响应以流式分片写入：处理器在生成每个大响应分片时调用 Write，等待者会按
// 相同顺序收到这些分片（“响应部分发送”）。
package dedup

import (
	"context"
	"errors"
	"sync"
)

// ErrDeduperClosed 在 Deduper 关闭后调用 Execute 时返回。
var ErrDeduperClosed = errors.New("dedup: deduper is closed")

// ErrStreamClosed 在响应流结束之后继续写入时返回。
var ErrStreamClosed = errors.New("dedup: stream is closed")

// Identity 是请求的验证身份。三个字段共同决定一个请求是否与另一个请求相同：
// 任一不同都不会命中去重或缓存重放。
type Identity struct {
	// AddrGen 是来源地址的验证代次。地址重新验证后应当递增，从而让旧代次
	// 持有的缓存无法被新（或未验证）来源重放。
	AddrGen uint64

	// Digest 是请求正文的摘要，通常使用加密哈希（如 SHA-256）。
	Digest []byte

	// Seq 是客户端在当前验证身份下分配的请求序号。
	Seq uint64
}

// key 将身份编码为定长、无分隔符歧义的映射键。
// 布局：AddrGen(8) || DigestLength(8) || Digest || Seq(8)。
func (id Identity) key() string {
	digestLen := len(id.Digest)
	key := make([]byte, 24+digestLen)
	putUint64(key[0:8], id.AddrGen)
	putUint64(key[8:16], uint64(digestLen))
	copy(key[16:16+digestLen], id.Digest)
	putUint64(key[16+digestLen:24+digestLen], id.Seq)
	return string(key)
}

// Equal 报告两个验证身份是否完全一致。
func (id Identity) Equal(other Identity) bool {
	return id.AddrGen == other.AddrGen &&
		id.Seq == other.Seq &&
		bytesEqual(id.Digest, other.Digest)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func putUint64(dst []byte, value uint64) {
	dst[0] = byte(value >> 56)
	dst[1] = byte(value >> 48)
	dst[2] = byte(value >> 40)
	dst[3] = byte(value >> 32)
	dst[4] = byte(value >> 24)
	dst[5] = byte(value >> 16)
	dst[6] = byte(value >> 8)
	dst[7] = byte(value)
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func cloneChunks(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, chunk := range in {
		out[i] = cloneBytes(chunk)
	}
	return out
}

// Event 是 Stream 上按序到达的响应事件。Chunk 非 nil 时为一个响应分片；
// Chunk 为 nil 且 Err 为 nil 表示响应正常结束；Err 非 nil 表示处理器失败，
// 失败的响应不会进入缓存。
type Event struct {
	Chunk []byte
	Err   error
}

// Stream 是一次 Execute 调用获得的响应事件流。
//
// 事件按处理器写入分片的顺序投递。读取方可以通过取消传入 Execute 的上下文
// 或调用 Close 提前结束读取；这只会关闭当前 Stream，不影响共享同一请求的
// 其他等待者，也不会取消处理器。
type Stream struct {
	events chan Event

	cancel context.CancelFunc

	closeOnce sync.Once

	// done 在 Stream 被读取方关闭时关闭，用于让投递 goroutine 退出。
	done chan struct{}
}

// Events 返回事件通道。通道关闭表示 Stream 终止；最后一个事件（若存在）
// 说明终止原因，读取方自身上下文取消时则不会再收到事件。
func (s *Stream) Events() <-chan Event {
	return s.events
}

// Close 关闭当前 Stream 并释放其投递 goroutine。可重复调用。
func (s *Stream) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		close(s.done)
	})
}

// ResponseWriter 供处理器流式写出响应分片。
//
// Write 的入参在返回后可能被调用方复用，实现会复制其内容，因此处理器无需
// 自行保留缓冲。Write 在请求已经结束或 Deduper 关闭后返回错误。
type ResponseWriter interface {
	Write(chunk []byte) error
}

// Handler 执行一次实际请求处理。处理器只对每个验证身份执行一次；合并进来的
// 重传不会再次调用处理器。
//
// ctx 与 Deduper 的生命周期绑定而不是与某个等待者的上下文绑定，因此单个
// 等待者取消不会中断处理器；Deduper 关闭时 ctx 会被取消。
type Handler func(ctx context.Context, id Identity, w ResponseWriter) error

// Option 配置 Deduper。
type Option func(*Deduper)

// WithCacheCapacity 设置成功响应缓存的最大条目数。达到上限时按先进先出顺序
// 淘汰最旧的缓存。capacity 为 0 表示不缓存完成的响应（进行中的请求仍然会
// 合并）。默认值为 1024。
func WithCacheCapacity(capacity int) Option {
	return func(d *Deduper) {
		d.cacheCap = capacity
	}
}

// WithEventBuffer 设置每个 Stream 的内部事件缓冲大小（分片数）。默认 8。
func WithEventBuffer(size int) Option {
	return func(d *Deduper) {
		if size > 0 {
			d.eventBuf = size
		}
	}
}

type inflightCall struct {
	// ping 在产生新分片或请求结束时关闭；之后替换为新的通道，形成按代次
	// 推进的广播信号。
	ping chan struct{}

	chunks [][]byte
	err    error
	done   bool
}

type cachedResponse struct {
	chunks [][]byte
}

// Deduper 合并进行中的重复请求，并缓存已完成请求的响应分片。
// 零值不可用，必须通过 New 构造。
type Deduper struct {
	handler Handler

	mu       sync.Mutex
	inflight map[string]*inflightCall
	cache    map[string]*cachedResponse

	// cacheOrder 按插入顺序记录缓存键，用于 FIFO 淘汰。
	cacheOrder []string
	cacheCap   int
	eventBuf   int

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
}

// New 创建 Deduper。handler 在缓存未命中时执行实际请求处理。
func New(handler Handler, opts ...Option) *Deduper {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Deduper{
		handler:  handler,
		inflight: make(map[string]*inflightCall),
		cache:    make(map[string]*cachedResponse),
		cacheCap: 1024,
		eventBuf: 8,
		ctx:      ctx,
		cancel:   cancel,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Execute 提交一次请求。
//
//   - 同一验证身份的请求正在处理：当前调用成为等待者，收到与首个请求相同
//     的分片流，不会再次执行处理器；
//   - 同一验证身份的请求已成功完成：返回缓存响应的重放流，处理器不执行；
//   - 身份不同（地址验证代次、摘要或序号任一不同）：作为新请求独立处理。
//
// reqDigest 为请求正文摘要，会被复制进验证身份；调用方之后复用底层数组
// 不会影响本次去重。ctx 只控制本次调用读取响应的生命周期，取消它（或关闭
// 返回的 Stream）不会取消共享请求的处理器，也不会影响其他等待者。
func (d *Deduper) Execute(ctx context.Context, id Identity, reqDigest []byte) (*Stream, error) {
	if len(reqDigest) > 0 {
		id.Digest = append([]byte(nil), reqDigest...)
	}

	d.mu.Lock()
	if d.ctx.Err() != nil {
		d.mu.Unlock()
		return nil, ErrDeduperClosed
	}

	key := id.key()
	if cached, ok := d.cache[key]; ok {
		stream := d.newStream(ctx)
		d.mu.Unlock()
		go d.pumpCache(stream, cached)
		return stream, nil
	}

	call, ok := d.inflight[key]
	if !ok {
		call = &inflightCall{ping: make(chan struct{})}
		d.inflight[key] = call
	}
	stream := d.newStream(ctx)
	d.mu.Unlock()

	if !ok {
		go d.runHandler(key, id, call)
	}
	go d.pumpInflight(stream, call)
	return stream, nil
}

// Close 停止接受新请求并关闭 Deduper。已经在处理的请求会随上下文取消而
// 尽快结束；已经缓存的响应不再接受新的重放。可重复调用。
func (d *Deduper) Close() {
	d.closeOnce.Do(d.cancel)
}

func (d *Deduper) newStream(parent context.Context) *Stream {
	_, cancel := context.WithCancel(parent)
	return &Stream{
		events: make(chan Event, d.eventBuf),
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// pumpCache 向一个新 Stream 重放缓存的完整响应。缓存分片在投递时复制，
// 避免读取方修改缓冲污染缓存或其他重放流。
func (d *Deduper) pumpCache(stream *Stream, cached *cachedResponse) {
	defer close(stream.events)
	for _, chunk := range cached.chunks {
		select {
		case stream.events <- Event{Chunk: cloneBytes(chunk)}:
		case <-stream.done:
			return
		case <-d.ctx.Done():
			return
		}
	}
	select {
	case stream.events <- Event{}:
	case <-stream.done:
	case <-d.ctx.Done():
	}
}

// pumpInflight 把一个进行中请求已产生及后续产生的分片按序投递给等待者。
// 等待者中途退出只停止本 goroutine，对请求本身和其他等待者无影响。
func (d *Deduper) pumpInflight(stream *Stream, call *inflightCall) {
	defer close(stream.events)

	sent := 0
	for {
		d.mu.Lock()
		ping := call.ping
		chunks := call.chunks
		err := call.err
		done := call.done
		d.mu.Unlock()

		for ; sent < len(chunks); sent++ {
			select {
			case stream.events <- Event{Chunk: cloneBytes(chunks[sent])}:
			case <-stream.done:
				return
			case <-d.ctx.Done():
				return
			}
		}

		if done {
			event := Event{}
			if err != nil {
				event = Event{Err: err}
			}
			select {
			case stream.events <- event:
			case <-stream.done:
			case <-d.ctx.Done():
			}
			return
		}

		select {
		case <-ping:
		case <-stream.done:
			return
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *Deduper) runHandler(key string, id Identity, call *inflightCall) {
	writer := &responseWriter{d: d, call: call}
	err := d.handler(d.ctx, id, writer)

	d.mu.Lock()
	defer d.mu.Unlock()

	// 以 call 当前状态幂等收尾；Close 之后处理器返回时同样走这条路径。
	call.err = err
	call.done = true
	close(call.ping)

	if err == nil && d.ctx.Err() == nil && d.cacheCap > 0 {
		cached := &cachedResponse{chunks: cloneChunks(call.chunks)}
		d.cache[key] = cached
		d.cacheOrder = append(d.cacheOrder, key)
		d.evictLocked()
	}
	delete(d.inflight, key)
}

func (d *Deduper) evictLocked() {
	for len(d.cacheOrder) > d.cacheCap {
		oldest := d.cacheOrder[0]
		d.cacheOrder = d.cacheOrder[1:]
		delete(d.cache, oldest)
	}
}

type responseWriter struct {
	d    *Deduper
	call *inflightCall
}

func (w *responseWriter) Write(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	w.d.mu.Lock()
	defer w.d.mu.Unlock()

	if w.call.done {
		return ErrStreamClosed
	}
	if w.d.ctx.Err() != nil {
		return ErrDeduperClosed
	}

	w.call.chunks = append(w.call.chunks, cloneBytes(chunk))

	// 先替换再关闭旧通道：等待者要么在旧通道上等待并被唤醒看到新分片，
	// 要么已经拿到新通道，不会漏掉广播。
	oldPing := w.call.ping
	w.call.ping = make(chan struct{})
	close(oldPing)
	return nil
}
