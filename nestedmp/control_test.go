package nestedmp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCancelRollsBack(t *testing.T) {
	s := buildSample(t)
	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	half := len(s.msg) / 2
	if _, err := p.Write(s.msg[:half]); err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	if _, err := p.Write(s.msg[half:]); !errors.Is(err, ErrCanceled) {
		t.Fatalf("want ErrCanceled, got %v", err)
	}
	if _, err := p.CloseCtx(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-cancel Close want ErrClosed, got %v", err)
	}
	if !host.txn.rolled || host.txn.committed {
		t.Fatal("cancel did not roll back")
	}
	for name := range host.txn.sinks {
		if _, ok := host.txn.visible(name); ok {
			t.Fatalf("part %q readable after cancel", name)
		}
	}
}

func TestContextCancelRollsBack(t *testing.T) {
	s := buildSample(t)
	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.WriteCtx(ctx, s.msg)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if !host.txn.rolled {
		t.Fatal("ctx cancel did not roll back")
	}
}

// blockingSink 在 release 关闭前阻塞 Write，用来验证背压传导。
type blockingSink struct {
	release chan struct{}
	started chan struct{}
}

func (b *blockingSink) Write([]byte) (int, error) {
	close(b.started)
	<-b.release
	return 0, errors.New("blocking sink aborted")
}

func (b *blockingSink) Close() error { return nil }

type blockingTxn struct {
	sink    *blockingSink
	rolled  bool
	putName string
}

func (t *blockingTxn) Put(_ context.Context, name string) (io.WriteCloser, error) {
	t.putName = name
	return t.sink, nil
}

func (t *blockingTxn) Commit(context.Context) error { return nil }
func (t *blockingTxn) Rollback(context.Context) error {
	t.rolled = true
	return nil
}

type blockingHost struct{ txn *blockingTxn }

func (h *blockingHost) Begin(context.Context) (StagingTxn, error) {
	h.txn = &blockingTxn{sink: &blockingSink{release: make(chan struct{}), started: make(chan struct{})}}
	return h.txn, nil
}

func TestBackpressureBlocksWrite(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 10000)
	var b wireBuilder
	b.open(rootB)
	b.header(TypeAttachment, "big", digestOf(body), "")
	b.body(body)
	b.close(rootB)

	host := &blockingHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB, Window: 64})
	if err != nil {
		t.Fatal(err)
	}
	writeReturned := make(chan struct{})
	go func() {
		_, _ = p.Write(b.bytes())
		close(writeReturned)
	}()
	select {
	case <-host.txn.sink.started:
	case <-time.After(2 * time.Second):
		t.Fatal("sink Write never started")
	}
	select {
	case <-writeReturned:
		t.Fatal("Write returned while sink is applying backpressure")
	case <-time.After(50 * time.Millisecond):
	}
	close(host.txn.sink.release)
	select {
	case <-writeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Write did not unblock after sink release")
	}
}

func TestMemoryBudgetExceededTerminates(t *testing.T) {
	s := buildSample(t)
	host := &fakeHost{}
	_, err := New(context.Background(), Options{
		Host:         host,
		RootBoundary: rootB,
		Window:       64,
		MemoryBudget: 70, // 连根层匹配器容量都不够，构造期即终止
	})
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("want ErrMemoryLimit at construction, got %v", err)
	}

	// 足够容纳根层、但推入子层时超预算。
	p, err := New(context.Background(), Options{
		Host:         &fakeHost{},
		RootBoundary: rootB,
		Window:       32,
		MemoryBudget: 140,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range splitEvery(s.msg, 11) {
		if _, werr := p.WriteCtx(context.Background(), ch); werr != nil {
			if !errors.Is(werr, ErrMemoryLimit) {
				t.Fatalf("want ErrMemoryLimit, got %v", werr)
			}
			return
		}
	}
	t.Fatal("expected memory limit error while nesting")
}

func TestRejectPreambleAndTrailing(t *testing.T) {
	s := buildSample(t)
	cases := map[string][]byte{
		"preamble": append([]byte("junk"), s.msg...),
		"trailing": append(append([]byte{}, s.msg...), 'x'),
	}
	for name, msg := range cases {
		host := &fakeHost{}
		p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Write(msg)
		if err == nil {
			_, err = p.CloseCtx(context.Background())
		}
		if err == nil || (!errors.Is(err, ErrPreamble) && !errors.Is(err, ErrTrailing)) {
			t.Fatalf("%s: want preamble/trailing error, got %v", name, err)
		}
		if !host.txn.rolled {
			t.Fatalf("%s: not rolled back", name)
		}
	}
}

func TestDuplicateBoundaryRejected(t *testing.T) {
	// 容器头声明的子边界与根边界完全相同：在切分任何正文之前就必须拒绝。
	dupChild := rootB
	var root wireBuilder
	root.open(rootB)
	root.header(TypeContainer, "box", digestOf([]byte("ignored")), dupChild)
	root.body(nil)
	root.close(rootB)

	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	_, werr := p.Write(root.bytes())
	cerr := error(nil)
	if werr == nil {
		_, cerr = p.CloseCtx(context.Background())
	}
	if !errors.Is(werr, ErrBoundary) && !errors.Is(cerr, ErrBoundary) {
		t.Fatalf("want ErrBoundary, got write=%v close=%v", werr, cerr)
	}
	if _, err := ReferenceParse(root.bytes(), rootB); !errors.Is(err, ErrBoundary) {
		t.Fatalf("reference: want ErrBoundary, got %v", err)
	}
}

func TestInvalidBoundaryNames(t *testing.T) {
	bad := []string{"", strings.Repeat("a", 71), "ab-cd", "ab cd", "ab!cd"}
	for _, name := range bad {
		if validBoundary(name) {
			t.Fatalf("boundary %q should be invalid", name)
		}
	}
	good := []string{"a", "Z", "0", strings.Repeat("a", 70), "Ab9Z"}
	for _, name := range good {
		if !validBoundary(name) {
			t.Fatalf("boundary %q should be valid", name)
		}
	}
}

func TestCaseSensitiveBoundary(t *testing.T) {
	body := []byte("ok")
	var b wireBuilder
	b.open("ab")
	b.header(TypeAttachment, "p", digestOf(body), "")
	b.body(body)
	b.close("ab")
	msg := b.bytes()

	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: "AB"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Write(msg)
	if err == nil {
		_, err = p.CloseCtx(context.Background())
	}
	if err == nil {
		t.Fatal("case-insensitive boundary match must be rejected")
	}
}
