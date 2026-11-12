package netsec

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitRefs(t *testing.T, d *Deduper, id Identity, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.mu.Lock()
		var got int
		if c, ok := d.inflight[id]; ok {
			got = c.refs
		}
		d.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("在途请求等待者数=%d, 期望 %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDoMergesInflightRetransmission(t *testing.T) {
	d := New(time.Minute)
	id := NewIdentity(1, 1, []byte("hello"))
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fn := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		close(started)
		<-release
		return []byte("world"), nil
	}

	type result struct {
		resp []byte
		err  error
	}
	results := make(chan result, 3)
	for i := 0; i < 3; i++ {
		go func() {
			resp, err := d.Do(context.Background(), id, fn)
			results <- result{resp, err}
		}()
	}
	<-started
	waitRefs(t, d, id, 3)
	close(release)

	for i := 0; i < 3; i++ {
		r := <-results
		if r.err != nil || string(r.resp) != "world" {
			t.Fatalf("等待者收到 resp=%q err=%v", r.resp, r.err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("重传合并后 fn 应只执行 1 次, 实际 %d 次", got)
	}
}

func TestCancelOneWaiterKeepsOthers(t *testing.T) {
	d := New(time.Minute)
	id := NewIdentity(1, 2, []byte("body"))
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func(ctx context.Context) ([]byte, error) {
		close(started)
		<-release
		return []byte("ok"), nil
	}

	ctx1, cancel := context.WithCancel(context.Background())
	err1 := make(chan error, 1)
	go func() {
		_, err := d.Do(ctx1, id, fn)
		err1 <- err
	}()
	<-started

	type result struct {
		resp []byte
		err  error
	}
	res2 := make(chan result, 1)
	go func() {
		resp, err := d.Do(context.Background(), id, fn)
		res2 <- result{resp, err}
	}()
	waitRefs(t, d, id, 2)

	cancel()
	if err := <-err1; !errors.Is(err, context.Canceled) {
		t.Fatalf("被取消的等待者应收到 context.Canceled, 得到 %v", err)
	}
	close(release)
	r := <-res2
	if r.err != nil || string(r.resp) != "ok" {
		t.Fatalf("其余等待者不应受取消影响, 收到 resp=%q err=%v", r.resp, r.err)
	}

	resp, err := d.Do(context.Background(), id, fn)
	if err != nil || string(resp) != "ok" {
		t.Fatalf("完成后的重传应命中缓存, 收到 resp=%q err=%v", resp, err)
	}
}

func TestSourceAddressChangeForcesRevalidation(t *testing.T) {
	d := New(time.Minute)
	body := []byte("request")
	before := NewIdentity(1, 7, body)
	var calls atomic.Int32
	fn := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte("big response"), nil
	}
	if _, err := d.Do(context.Background(), before, fn); err != nil {
		t.Fatal(err)
	}

	// 来源地址变化，地址验证代次推进，序号与正文保持不变。
	after := NewIdentity(2, 7, body)
	if _, _, ok := d.Replay(after, 0, 1<<20); ok {
		t.Fatal("地址变化后不得重放旧验证身份的缓存响应")
	}
	if _, err := d.Do(context.Background(), after, fn); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("地址变化后的相同序号请求必须重新执行, fn 执行 %d 次", got)
	}

	if chunk, _, ok := d.Replay(before, 0, 4); !ok || string(chunk) != "big " {
		t.Fatalf("原验证身份的缓存响应应可重放, ok=%v chunk=%q", ok, chunk)
	}
}

func TestSameSequenceDifferentBodyIsDistinct(t *testing.T) {
	d := New(time.Minute)
	idA := NewIdentity(1, 9, []byte("body-a"))
	idB := NewIdentity(1, 9, []byte("body-b"))

	var calls atomic.Int32
	fn := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte("resp"), nil
	}
	if _, err := d.Do(context.Background(), idA, fn); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := d.Replay(idB, 0, 16); ok {
		t.Fatal("相同序号不同正文不得命中他人缓存")
	}
	if _, err := d.Do(context.Background(), idB, fn); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("相同序号不同正文应各自执行, fn 执行 %d 次", got)
	}

	chunkA, _, okA := d.Replay(idA, 0, 16)
	chunkB, _, okB := d.Replay(idB, 0, 16)
	if !okA || !okB || string(chunkA) != "resp" || string(chunkB) != "resp" {
		t.Fatalf("两个身份的缓存应各自独立, A=(%q,%v) B=(%q,%v)", chunkA, okA, chunkB, okB)
	}
}

func TestReplayServesPartialResponse(t *testing.T) {
	d := New(time.Minute)
	id := NewIdentity(1, 3, []byte("chunked"))
	full := make([]byte, 250)
	for i := range full {
		full[i] = byte(i)
	}
	var calls atomic.Int32
	fn := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		return full, nil
	}
	if _, err := d.Do(context.Background(), id, fn); err != nil {
		t.Fatal(err)
	}

	const mtu = 100
	var got []byte
	for off := 0; ; {
		chunk, total, ok := d.Replay(id, off, mtu)
		if !ok {
			t.Fatalf("偏移 %d 处重放失败", off)
		}
		if total != len(full) {
			t.Fatalf("total=%d, 期望 %d", total, len(full))
		}
		got = append(got, chunk...)
		off += len(chunk)
		if off >= total {
			break
		}
	}
	if !bytes.Equal(got, full) {
		t.Fatal("分片拼接与完整响应不一致")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("分片重放不应重复执行, fn 执行 %d 次", n)
	}

	if chunk, total, ok := d.Replay(id, len(full), mtu); !ok || len(chunk) != 0 || total != len(full) {
		t.Fatalf("越界偏移应返回空片段: ok=%v len=%d total=%d", ok, len(chunk), total)
	}
	stranger := NewIdentity(2, 3, []byte("chunked"))
	if _, _, ok := d.Replay(stranger, 0, mtu); ok {
		t.Fatal("不得向不同验证代次重放响应分片")
	}
}

func TestCachedResponseExpires(t *testing.T) {
	d := New(20 * time.Millisecond)
	id := NewIdentity(1, 4, []byte("ttl"))
	fn := func(ctx context.Context) ([]byte, error) {
		return []byte("v"), nil
	}
	if _, err := d.Do(context.Background(), id, fn); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, _, ok := d.Replay(id, 0, 8); ok {
		t.Fatal("过期响应不得重放")
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	d := New(time.Minute)
	id := NewIdentity(1, 5, []byte("flaky"))
	fail := errors.New("boom")
	var calls atomic.Int32
	fn := func(ctx context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, fail
		}
		return []byte("recovered"), nil
	}
	if _, err := d.Do(context.Background(), id, fn); !errors.Is(err, fail) {
		t.Fatalf("首次执行应失败, 得到 %v", err)
	}
	resp, err := d.Do(context.Background(), id, fn)
	if err != nil || string(resp) != "recovered" {
		t.Fatalf("错误不应被缓存, 重传应重新执行, 收到 resp=%q err=%v", resp, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("fn 应执行 2 次, 实际 %d 次", got)
	}
}
