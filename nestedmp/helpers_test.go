package nestedmp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

// fakeSink 记录单个部件的写入回调序列。
type fakeSink struct {
	name   string
	mu     sync.Mutex
	buf    bytes.Buffer
	writes []writeCall
	closed bool
	failN  int // >0 时第 N 次 Write 返回错误
	n      int
}

type writeCall struct {
	offset int64
	data   []byte
}

func (s *fakeSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	if s.failN > 0 && s.n == s.failN {
		return 0, errors.New("fake sink injected failure")
	}
	s.writes = append(s.writes, writeCall{offset: int64(s.buf.Len()), data: append([]byte(nil), p...)})
	return s.buf.Write(p)
}

func (s *fakeSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("double close")
	}
	s.closed = true
	return nil
}

func (s *fakeSink) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.buf.Bytes()...)
}

// fakeTxn 实现 StagingTxn，支持提交延迟与可见性控制。
type fakeTxn struct {
	mu        sync.Mutex
	sinks     map[string]*fakeSink
	committed bool
	rolled    bool
	commitCh  chan struct{} // 非 nil 时 Commit 阻塞到此通道关闭
	putErr    error
}

func newFakeTxn() *fakeTxn {
	return &fakeTxn{sinks: map[string]*fakeSink{}}
}

func (t *fakeTxn) Put(_ context.Context, name string) (io.WriteCloser, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rolled || t.committed {
		return nil, fmt.Errorf("txn already finished")
	}
	if t.putErr != nil {
		return nil, t.putErr
	}
	if _, exists := t.sinks[name]; exists {
		return nil, fmt.Errorf("duplicate sink %q", name)
	}
	s := &fakeSink{name: name}
	t.sinks[name] = s
	return s, nil
}

func (t *fakeTxn) Commit(ctx context.Context) error {
	if t.commitCh != nil {
		select {
		case <-t.commitCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rolled {
		return errors.New("cannot commit rolled-back txn")
	}
	t.committed = true
	return nil
}

func (t *fakeTxn) Rollback(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rolled = true
	for _, s := range t.sinks {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}
	return nil
}

// visible 模拟对象库读视图：只有已提交事务的暂存对象可读。
func (t *fakeTxn) visible(name string) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.committed {
		return nil, false
	}
	s, ok := t.sinks[name]
	if !ok {
		return nil, false
	}
	return s.bytes(), true
}

// fakeHost 每次 Begin 产出一个可在测试中取回的事务。
type fakeHost struct{ txn *fakeTxn }

func (h *fakeHost) Begin(context.Context) (StagingTxn, error) {
	h.txn = newFakeTxn()
	return h.txn, nil
}

// recorder 是 Tracer 的内存实现，供验收比对字节位置与写入序列。
type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) OnEvent(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// wireBuilder 按字节精确构造题内格式报文。
type wireBuilder struct {
	buf     bytes.Buffer
	started bool
}

func (b *wireBuilder) open(boundary string) {
	if b.started {
		// 紧邻分隔行的 CRLF 属于分隔行，不属于上一正文。
		b.buf.WriteString("\r\n")
	}
	b.started = true
	fmt.Fprintf(&b.buf, "--%s\r\n", boundary)
}

func (b *wireBuilder) close(boundary string) {
	b.buf.WriteString("\r\n")
	fmt.Fprintf(&b.buf, "--%s--\r\n", boundary)
}

func (b *wireBuilder) header(typ, name string, digest [32]byte, child string) {
	fmt.Fprintf(&b.buf, "Type: %s\r\nName: %s\r\nSHA-256: %s\r\n", typ, name, hex.EncodeToString(digest[:]))
	if child != "" {
		fmt.Fprintf(&b.buf, "Boundary: %s\r\n", child)
	}
	b.buf.WriteString("\r\n")
}

func (b *wireBuilder) body(data []byte) { b.buf.Write(data) }

func (b *wireBuilder) bytes() []byte { return b.buf.Bytes() }

func digestOf(data []byte) [sha256.Size]byte { return sha256.Sum256(data) }

// splitEvery 按固定长度切块。
func splitEvery(data []byte, n int) [][]byte {
	var out [][]byte
	for i := 0; i < len(data); i += n {
		end := i + n
		if end > len(data) {
			end = len(data)
		}
		out = append(out, data[i:end])
	}
	return out
}

// randomSplits 用确定性伪随机产生 1..maxLen 的变长切块。
func randomSplits(data []byte, seed int64, maxLen int) [][]byte {
	var out [][]byte
	state := seed
	next := func() int {
		state = state*1103515245 + 12345
		return int((state >> 16) & 0x7fffffff)
	}
	pos := 0
	for pos < len(data) {
		n := next()%maxLen + 1
		if pos+n > len(data) {
			n = len(data) - pos
		}
		out = append(out, data[pos:pos+n])
		pos += n
	}
	return out
}
