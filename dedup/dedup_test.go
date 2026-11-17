package dedup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// digest 是测试用的确定性摘要：不同正文必然产生不同摘要。
func digest(body string) []byte {
	return []byte("sha256:" + body)
}

func identity(addrGen, seq uint64, body string) Identity {
	return Identity{AddrGen: addrGen, Seq: seq, Digest: digest(body)}
}

// collect 读取整个 Stream，返回收到的响应分片、终止错误以及是否在通道关闭
// 前读到正常结束事件。
func collect(t *testing.T, stream *Stream) ([][]byte, error) {
	t.Helper()

	var chunks [][]byte
	var terminal error
	for event := range stream.Events() {
		if event.Err != nil {
			terminal = event.Err
		}
		if event.Chunk != nil {
			chunks = append(chunks, append([]byte(nil), event.Chunk...))
		}
	}
	return chunks, terminal
}

func joined(chunks [][]byte) []byte {
	return bytes.Join(chunks, nil)
}

// waitFor 轮询条件成立，避免在同步时序上依赖固定 sleep。
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// TestSourceAddressChangeRejectsReplay 验证地址验证代次变化后：
// 进行中请求不会被错误合并，已完成响应也不能跨身份重放。
func TestSourceAddressChangeRejectsReplay(t *testing.T) {
	var calls int32

	// 不同身份的处理器分别在收到关闭信号后才结束，保证第一次请求在第二次
	// 请求到达时仍处于进行中。
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})

	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		which := atomic.AddInt32(&calls, 1)
		if err := w.Write([]byte(fmt.Sprintf("response-gen-%d", id.AddrGen))); err != nil {
			return err
		}
		if which == 1 {
			select {
			case <-firstRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			select {
			case <-secondRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	defer d.Close()

	oldID := identity(1, 42, "GET /large")
	oldStream, err := d.Execute(context.Background(), oldID, digest("GET /large"))
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}

	// 来源地址变化（重新验证，代次从 1 变为 2），即使序号与正文完全相同，
	// 也必须作为独立请求处理。
	newID := identity(2, 42, "GET /large")
	newStream, err := d.Execute(context.Background(), newID, digest("GET /large"))
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&calls) == 2 })

	// 进行中阶段：两个身份各自拥有独立的请求与首片响应。
	select {
	case event := <-oldStream.Events():
		if string(event.Chunk) != "response-gen-1" {
			t.Fatalf("old stream chunk = %q", event.Chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("old stream did not receive partial response")
	}
	select {
	case event := <-newStream.Events():
		if string(event.Chunk) != "response-gen-2" {
			t.Fatalf("new stream chunk = %q", event.Chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("new stream did not receive partial response")
	}

	close(firstRelease)
	close(secondRelease)

	// 首片已在上面手动读出，这里读取剩余事件后手动拼回完整响应。
	oldRest, oldErr := collect(t, oldStream)
	if oldErr != nil {
		t.Fatalf("old stream terminal error: %v", oldErr)
	}
	newRest, newErr := collect(t, newStream)
	if newErr != nil {
		t.Fatalf("new stream terminal error: %v", newErr)
	}
	oldChunks := append([][]byte{[]byte("response-gen-1")}, oldRest...)
	newChunks := append([][]byte{[]byte("response-gen-2")}, newRest...)
	if string(joined(oldChunks)) != "response-gen-1" {
		t.Fatalf("old response = %q", joined(oldChunks))
	}
	if string(joined(newChunks)) != "response-gen-2" {
		t.Fatalf("new response = %q", joined(newChunks))
	}

	// 完成阶段：旧代次的重传命中自己的缓存；新代次不能重放旧代次缓存，
	// 也不能反过来。
	replayOld, err := d.Execute(context.Background(), oldID, digest("GET /large"))
	if err != nil {
		t.Fatalf("replay old: %v", err)
	}
	replayNew, err := d.Execute(context.Background(), newID, digest("GET /large"))
	if err != nil {
		t.Fatalf("replay new: %v", err)
	}

	oldReplayed, err := collect(t, replayOld)
	if err != nil {
		t.Fatalf("collect replay old: %v", err)
	}
	newReplayed, err := collect(t, replayNew)
	if err != nil {
		t.Fatalf("collect replay new: %v", err)
	}
	if string(joined(oldReplayed)) != "response-gen-1" {
		t.Fatalf("old cache replay = %q", joined(oldReplayed))
	}
	if string(joined(newReplayed)) != "response-gen-2" {
		t.Fatalf("new cache replay = %q", joined(newReplayed))
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("handler calls = %d, want 2 (cache replays must not re-execute)", got)
	}
}

// TestSameSequenceDifferentBodyDedupsSeparately 验证序号相同但正文摘要不同
// 的请求不会互相命中去重或缓存。
func TestSameSequenceDifferentBodyDedupsSeparately(t *testing.T) {
	var mu sync.Mutex
	bodies := make(map[string]int)

	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		mu.Lock()
		bodies[string(id.Digest)]++
		mu.Unlock()

		chunk := []byte("reply:" + string(id.Digest))
		if err := w.Write(chunk); err != nil {
			return err
		}
		return nil
	})
	defer d.Close()

	idA := identity(7, 99, "body-A")
	idB := identity(7, 99, "body-B")

	firstA, err := d.Execute(context.Background(), idA, digest("body-A"))
	if err != nil {
		t.Fatalf("execute A: %v", err)
	}
	firstB, err := d.Execute(context.Background(), idB, digest("body-B"))
	if err != nil {
		t.Fatalf("execute B: %v", err)
	}

	chunksA, err := collect(t, firstA)
	if err != nil {
		t.Fatalf("collect A: %v", err)
	}
	chunksB, err := collect(t, firstB)
	if err != nil {
		t.Fatalf("collect B: %v", err)
	}
	if string(joined(chunksA)) != "reply:"+string(digest("body-A")) {
		t.Fatalf("response A = %q", joined(chunksA))
	}
	if string(joined(chunksB)) != "reply:"+string(digest("body-B")) {
		t.Fatalf("response B = %q", joined(chunksB))
	}

	// 同身份重传命中缓存；异正文重传仍然独立。
	replayA, err := d.Execute(context.Background(), idA, digest("body-A"))
	if err != nil {
		t.Fatalf("replay A: %v", err)
	}
	replayB, err := d.Execute(context.Background(), idB, digest("body-B"))
	if err != nil {
		t.Fatalf("replay B: %v", err)
	}
	if _, err := collect(t, replayA); err != nil {
		t.Fatalf("collect replay A: %v", err)
	}
	if _, err := collect(t, replayB); err != nil {
		t.Fatalf("collect replay B: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if bodies[string(digest("body-A"))] != 1 || bodies[string(digest("body-B"))] != 1 {
		t.Fatalf("handler execution counts = %v, want each body executed once", bodies)
	}
}

// TestInflightRetransmitMergesAndStreamsPartials 验证未完成请求的重传只执行
// 一次处理器，且每个等待者都按写入顺序收到“部分发送”的响应分片；完成后的
// 重传从缓存得到完全相同的分片序列。
func TestInflightRetransmitMergesAndStreamsPartials(t *testing.T) {
	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})

	wantChunks := [][]byte{
		[]byte("chunk-0"),
		[]byte("chunk-1"),
		[]byte("chunk-2"),
	}

	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		atomic.AddInt32(&calls, 1)
		close(started)

		// 先把前两片发出去并让等待者观察到“部分发送”，再等待测试放行后
		// 发送最后一片，覆盖进行中与完成两个阶段。
		for _, chunk := range wantChunks[:2] {
			if err := w.Write(chunk); err != nil {
				return err
			}
		}
		<-release
		if err := w.Write(wantChunks[2]); err != nil {
			return err
		}
		return nil
	})
	defer d.Close()

	id := identity(3, 7, "streaming")
	leader, err := d.Execute(context.Background(), id, digest("streaming"))
	if err != nil {
		t.Fatalf("leader execute: %v", err)
	}
	<-started

	// 重传发生在请求尚未完成时，必须合并为等待者而不是再次执行处理器。
	follower, err := d.Execute(context.Background(), id, digest("streaming"))
	if err != nil {
		t.Fatalf("follower execute: %v", err)
	}

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&calls) == 1 })

	receiveFirstTwo := func(name string, stream *Stream) {
		t.Helper()
		for i := 0; i < 2; i++ {
			select {
			case event := <-stream.Events():
				if event.Err != nil || !bytes.Equal(event.Chunk, wantChunks[i]) {
					t.Fatalf("%s event %d = %+v, want chunk %q", name, i, event, wantChunks[i])
				}
			case <-time.After(time.Second):
				t.Fatalf("%s timed out waiting for partial chunk %d", name, i)
			}
		}
	}
	receiveFirstTwo("leader", leader)
	receiveFirstTwo("follower", follower)

	close(release)

	leaderChunks, leaderErr := collect(t, leader)
	if leaderErr != nil {
		t.Fatalf("leader terminal error: %v", leaderErr)
	}
	followerChunks, followerErr := collect(t, follower)
	if followerErr != nil {
		t.Fatalf("follower terminal error: %v", followerErr)
	}

	// collect 读取的是剩余分片，因此期望仅包含最后一片。
	if !bytes.Equal(joined(leaderChunks), wantChunks[2]) {
		t.Fatalf("leader remaining chunks = %q", joined(leaderChunks))
	}
	if !bytes.Equal(joined(followerChunks), wantChunks[2]) {
		t.Fatalf("follower remaining chunks = %q", joined(followerChunks))
	}

	// 完成后的重传从缓存重放完整的三分片，且处理器调用次数保持为 1。
	replay, err := d.Execute(context.Background(), id, digest("streaming"))
	if err != nil {
		t.Fatalf("replay execute: %v", err)
	}
	replayed, err := collect(t, replay)
	if err != nil {
		t.Fatalf("replay collect: %v", err)
	}
	if len(replayed) != len(wantChunks) {
		t.Fatalf("replayed chunk count = %d, want %d", len(replayed), len(wantChunks))
	}
	for i, chunk := range wantChunks {
		if !bytes.Equal(replayed[i], chunk) {
			t.Fatalf("replayed chunk %d = %q, want %q", i, replayed[i], chunk)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

// TestCancelingOneWaiterDoesNotCancelOthers 验证取消一个等待者只会关闭它
// 自己的流：处理器继续运行，其他等待者仍能收完整响应。
func TestCancelingOneWaiterDoesNotCancelOthers(t *testing.T) {
	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})

	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		atomic.AddInt32(&calls, 1)
		close(started)
		if err := w.Write([]byte("only-chunk")); err != nil {
			return err
		}
		<-release
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	})
	defer d.Close()

	id := identity(5, 1, "cancel")
	leader, err := d.Execute(context.Background(), id, digest("cancel"))
	if err != nil {
		t.Fatalf("leader execute: %v", err)
	}
	<-started

	cancelCtx, cancel := context.WithCancel(context.Background())
	victim, err := d.Execute(cancelCtx, id, digest("cancel"))
	if err != nil {
		t.Fatalf("victim execute: %v", err)
	}
	survivor, err := d.Execute(context.Background(), id, digest("cancel"))
	if err != nil {
		t.Fatalf("survivor execute: %v", err)
	}

	// 被取消的等待者先读到已产生的分片，随后主动放弃；取消动作不能传播。
	select {
	case event := <-victim.Events():
		if string(event.Chunk) != "only-chunk" {
			t.Fatalf("victim chunk = %q", event.Chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("victim did not receive partial chunk")
	}
	cancel()
	victim.Close()

	// 处理器仍在运行（release 尚未关闭），且只执行过一次。
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler calls after cancel = %d, want 1", got)
	}

	close(release)

	leaderChunks, leaderErr := collect(t, leader)
	if leaderErr != nil {
		t.Fatalf("leader terminal error: %v", leaderErr)
	}
	survivorChunks, survivorErr := collect(t, survivor)
	if survivorErr != nil {
		t.Fatalf("survivor terminal error: %v", survivorErr)
	}
	if string(joined(leaderChunks)) != "only-chunk" {
		t.Fatalf("leader response = %q", joined(leaderChunks))
	}
	if string(joined(survivorChunks)) != "only-chunk" {
		t.Fatalf("survivor response = %q", joined(survivorChunks))
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler calls at end = %d, want 1", got)
	}
}

// TestFailedResponseIsNotCached 验证处理器失败时不写缓存，同身份重传会重新
// 执行处理器；而随后成功的响应才被缓存。
func TestFailedResponseIsNotCached(t *testing.T) {
	var calls int32
	boom := errors.New("upstream boom")

	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		which := atomic.AddInt32(&calls, 1)
		if err := w.Write([]byte("partial-before-failure")); err != nil {
			return err
		}
		if which == 1 {
			return boom
		}
		return w.Write([]byte("after-recovery"))
	})
	defer d.Close()

	id := identity(9, 3, "may-fail")

	failed, err := d.Execute(context.Background(), id, digest("may-fail"))
	if err != nil {
		t.Fatalf("failed execute: %v", err)
	}
	failedChunks, failedErr := collect(t, failed)
	if !errors.Is(failedErr, boom) {
		t.Fatalf("terminal error = %v, want %v", failedErr, boom)
	}
	if string(joined(failedChunks)) != "partial-before-failure" {
		t.Fatalf("failed chunks = %q", joined(failedChunks))
	}

	retried, err := d.Execute(context.Background(), id, digest("may-fail"))
	if err != nil {
		t.Fatalf("retry execute: %v", err)
	}
	retriedChunks, retriedErr := collect(t, retried)
	if retriedErr != nil {
		t.Fatalf("retry terminal error: %v", retriedErr)
	}
	if want := "partial-before-failureafter-recovery"; string(joined(retriedChunks)) != want {
		t.Fatalf("retry chunks = %q, want %q", joined(retriedChunks), want)
	}

	// 第三次调用必须命中第二次成功调用产生的缓存。
	cached, err := d.Execute(context.Background(), id, digest("may-fail"))
	if err != nil {
		t.Fatalf("cached execute: %v", err)
	}
	if _, err := collect(t, cached); err != nil {
		t.Fatalf("cached collect: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

// TestCacheCapacityEvictsFIFO 验证缓存按 FIFO 淘汰，被淘汰身份的重传会重新
// 执行处理器，而未淘汰身份仍命中缓存。
func TestCacheCapacityEvictsFIFO(t *testing.T) {
	var calls int32
	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		atomic.AddInt32(&calls, 1)
		return w.Write([]byte(fmt.Sprintf("resp-%d", id.Seq)))
	}, WithCacheCapacity(2))
	defer d.Close()

	for _, seq := range []uint64{1, 2} {
		stream, err := d.Execute(context.Background(), identity(1, seq, "body"), digest("body"))
		if err != nil {
			t.Fatalf("execute seq %d: %v", seq, err)
		}
		if _, err := collect(t, stream); err != nil {
			t.Fatalf("collect seq %d: %v", seq, err)
		}
	}

	// 插入第三个身份后，seq=1 的缓存应被淘汰。
	third, err := d.Execute(context.Background(), identity(1, 3, "body"), digest("body"))
	if err != nil {
		t.Fatalf("execute seq 3: %v", err)
	}
	if _, err := collect(t, third); err != nil {
		t.Fatalf("collect seq 3: %v", err)
	}

	// 先验证未被淘汰的 seq=2 仍命中缓存。
	retained, err := d.Execute(context.Background(), identity(1, 2, "body"), digest("body"))
	if err != nil {
		t.Fatalf("replay retained: %v", err)
	}
	chunks, err := collect(t, retained)
	if err != nil {
		t.Fatalf("collect retained: %v", err)
	}
	if string(joined(chunks)) != "resp-2" {
		t.Fatalf("retained response = %q", joined(chunks))
	}

	// 再验证被淘汰的 seq=1 重新执行处理器。
	evicted, err := d.Execute(context.Background(), identity(1, 1, "body"), digest("body"))
	if err != nil {
		t.Fatalf("re-execute evicted: %v", err)
	}
	chunks, err = collect(t, evicted)
	if err != nil {
		t.Fatalf("collect evicted: %v", err)
	}
	if string(joined(chunks)) != "resp-1" {
		t.Fatalf("evicted response = %q", joined(chunks))
	}

	// seq 1、2、3 各执行一次，淘汰后 seq 1 再执行一次，共 4 次。
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("handler calls = %d, want 4", got)
	}
}

// TestExecuteAfterClose 验证关闭后的 Deduper 拒绝新请求。
func TestExecuteAfterClose(t *testing.T) {
	d := New(func(ctx context.Context, id Identity, w ResponseWriter) error {
		return nil
	})
	d.Close()

	if _, err := d.Execute(context.Background(), identity(1, 1, "x"), digest("x")); !errors.Is(err, ErrDeduperClosed) {
		t.Fatalf("execute after close error = %v, want %v", err, ErrDeduperClosed)
	}
}
