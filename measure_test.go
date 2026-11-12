package netsec

import (
	"context"
	"crypto"
	"crypto/sha256"
	_ "crypto/sha512"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// consumeAll 模拟真实执行器：完整读取不可变视图并对实际消费的字节算摘要。
func consumeAll(_ context.Context, view View, measured []byte) ([]byte, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, view)
	if err != nil {
		return nil, 0, err
	}
	return h.Sum(nil), n, nil
}

func digestOf(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func TestStableExecutionConsumesImmutableView(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/kernel.bin", []byte("payload-v1"))

	pathOpensBefore := store.PathOpenCount()
	result, err := MeasureAndExecute(context.Background(), obj, nil, consumeAll)
	if err != nil {
		t.Fatalf("MeasureAndExecute: %v", err)
	}

	if result.Attempts != 1 {
		t.Fatalf("expected single attempt, got %d", result.Attempts)
	}
	wantProof, _ := obj.Proof(context.Background())
	if !result.Proof.Equal(wantProof) {
		t.Fatalf("proof = %v, want %v", result.Proof, wantProof)
	}
	if string(result.Digest) != string(digestOf([]byte("payload-v1"))) {
		t.Fatal("measured digest does not match object content")
	}
	if store.PathOpenCount() != pathOpensBefore {
		t.Fatal("measurement must not reopen the object by path")
	}
}

func TestViewIsImmutableAcrossLaterMutation(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/pinned", []byte("original-bytes"))

	view, err := obj.OpenView(context.Background())
	if err != nil {
		t.Fatalf("OpenView: %v", err)
	}
	pinnedProof := view.Proof()

	// 视图打开后对象原地改写，视图字节与证明都不得受影响。
	obj.ModifyInPlace([]byte("completely-different-bytes"))

	got, err := io.ReadAll(view)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "original-bytes" {
		t.Fatalf("view changed after mutation: %q", got)
	}
	if !view.Proof().Equal(pinnedProof) {
		t.Fatal("view proof must stay pinned to the opened version")
	}
	if err := view.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if store.PathOpenCount() != 0 {
		t.Fatal("OpenView must not count as a path open")
	}
}

func TestCustomHashOption(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/sha512", []byte("hash-me"))

	result, err := MeasureAndExecute(context.Background(), obj, nil,
		func(_ context.Context, view View, measured []byte) ([]byte, int64, error) {
			h := crypto.SHA512.New()
			n, err := io.Copy(h, view)
			if err != nil {
				return nil, 0, err
			}
			return h.Sum(nil), n, nil
		},
		WithHash(crypto.SHA512),
	)
	if err != nil {
		t.Fatalf("MeasureAndExecute: %v", err)
	}
	if len(result.Digest) != crypto.SHA512.Size() {
		t.Fatalf("digest length = %d, want %d", len(result.Digest), crypto.SHA512.Size())
	}
	h := crypto.SHA512.New()
	h.Write([]byte("hash-me"))
	if string(result.Digest) != string(h.Sum(nil)) {
		t.Fatal("measured digest does not match the SHA-512 of the content")
	}
}

func TestInPlaceModificationDuringMeasurementRetries(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/initrd.img", []byte("version-one"))

	gate := NewBarrier()
	obj.SetReadGate(gate)

	type outcome struct {
		result *Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := MeasureAndExecute(context.Background(), obj, nil, consumeAll)
		done <- outcome{r, err}
	}()

	// 第一次测量读到一半时，另一线程原地改写同一对象。
	select {
	case <-gate.Fired():
	case <-time.After(2 * time.Second):
		t.Fatal("measurement never reached the blocking read")
	}
	secondProof := obj.ModifyInPlace([]byte("version-two-much-longer"))
	gate.Release()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("MeasureAndExecute: %v", got.err)
		}
		if got.result.Attempts < 2 {
			t.Fatalf("expected retry after in-place modification, attempts = %d", got.result.Attempts)
		}
		if !got.result.Proof.Equal(secondProof) {
			t.Fatalf("executed proof = %v, want %v", got.result.Proof, secondProof)
		}
		if string(got.result.Digest) != string(digestOf([]byte("version-two-much-longer"))) {
			t.Fatal("executed digest must match the new immutable version")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MeasureAndExecute did not finish")
	}

	if store.PathOpenCount() != 0 {
		t.Fatalf("path reopen count = %d, want 0", store.PathOpenCount())
	}
}

func TestRenameKeepsVersionAndSucceeds(t *testing.T) {
	store := NewStore()
	obj := store.Create("/old/path/boot.cfg", []byte("stable-config"))
	before, _ := obj.Proof(context.Background())

	gate := NewBarrier()
	obj.SetReadGate(gate)

	done := make(chan error, 1)
	go func() {
		_, err := MeasureAndExecute(context.Background(), obj, nil, consumeAll)
		done <- err
	}()

	select {
	case <-gate.Fired():
	case <-time.After(2 * time.Second):
		t.Fatal("measurement never reached the blocking read")
	}
	obj.Rename("/new/path/boot.cfg")
	gate.Release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rename must not invalidate the version: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MeasureAndExecute did not finish")
	}

	after, _ := obj.Proof(context.Background())
	if !after.Equal(before) {
		t.Fatalf("proof changed across rename: before=%v after=%v", before, after)
	}
	if obj.Path() != "/new/path/boot.cfg" {
		t.Fatalf("path = %q, want renamed path", obj.Path())
	}
}

func TestVersionNumberReuseIsDetected(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/firmware", []byte("firmware-original"))

	// 复用相同 Seq：证明里只有 Seq 相同，ObjectID/Nonce 不同。
	replacement := obj.ReplaceWithReusedSeq([]byte("firmware-reused-seq"))

	oldProof, _ := obj.Proof(context.Background())
	newProof, _ := replacement.Proof(context.Background())
	if oldProof.Seq != newProof.Seq {
		t.Fatalf("test setup: reused seq = %d, want %d", newProof.Seq, oldProof.Seq)
	}
	if oldProof.Equal(newProof) {
		t.Fatal("full proofs must differ even when the sequence number is reused")
	}

	anchor := oldProof
	if _, err := MeasureAndExecute(context.Background(), replacement, &anchor, consumeAll); !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("err = %v, want ErrAnchorMismatch", err)
	}

	// 不锚定旧版本时，复用版本号的新对象可以作为全新稳定版本执行。
	result, err := MeasureAndExecute(context.Background(), replacement, nil, consumeAll)
	if err != nil {
		t.Fatalf("MeasureAndExecute: %v", err)
	}
	if !result.Proof.Equal(newProof) {
		t.Fatalf("proof = %v, want %v", result.Proof, newProof)
	}
	if string(result.Digest) != string(digestOf([]byte("firmware-reused-seq"))) {
		t.Fatal("digest must match the replacement content")
	}
}

func TestSeqOnlyComparisonMissesReuse(t *testing.T) {
	store := NewStore()
	original := store.Create("/x", []byte("a"))
	reused := original.ReplaceWithReusedSeq([]byte("b"))

	p1, _ := original.Proof(context.Background())
	p2, _ := reused.Proof(context.Background())

	// 仅比较 Seq 的错误实现会把两个不同版本判为相同。
	if p1.Seq != p2.Seq {
		t.Fatal("test setup expects equal sequence numbers")
	}
	if p1.Equal(p2) {
		t.Fatal("VersionProof.Equal must compare ObjectID and Nonce, not only Seq")
	}
}

func TestCallbackDigestCheck(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/digest-check", []byte("abcdef"))

	t.Run("short read is rejected", func(t *testing.T) {
		shortRead := func(_ context.Context, view View, _ []byte) ([]byte, int64, error) {
			buf := make([]byte, 3)
			n, err := io.ReadFull(view, buf)
			if err != nil && err != io.ErrUnexpectedEOF {
				return nil, 0, err
			}
			sum := sha256.Sum256(buf[:n])
			return sum[:], int64(n), nil
		}
		_, err := MeasureAndExecute(context.Background(), obj, nil, shortRead)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want ErrDigestMismatch", err)
		}
	})

	t.Run("forged digest is rejected", func(t *testing.T) {
		forged := func(_ context.Context, view View, measured []byte) ([]byte, int64, error) {
			n, err := io.Copy(io.Discard, view)
			if err != nil {
				return nil, 0, err
			}
			fake := append([]byte(nil), measured...)
			fake[0] ^= 0xFF
			return fake, n, nil
		}
		_, err := MeasureAndExecute(context.Background(), obj, nil, forged)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want ErrDigestMismatch", err)
		}
	})

	t.Run("full consumption is accepted", func(t *testing.T) {
		result, err := MeasureAndExecute(context.Background(), obj, nil, consumeAll)
		if err != nil {
			t.Fatalf("MeasureAndExecute: %v", err)
		}
		if string(result.Digest) != string(digestOf([]byte("abcdef"))) {
			t.Fatal("unexpected measured digest")
		}
	})
}

func TestConstantChurnExhaustsRetries(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/churn", []byte("gen-0"))

	obj.SetOpenHook(func() {
		obj.ModifyInPlace([]byte(fmt.Sprintf("gen-%d", time.Now().UnixNano())))
	})

	_, err := MeasureAndExecute(context.Background(), obj, nil, consumeAll, WithMaxAttempts(3))
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("err = %v, want ErrExhausted", err)
	}
	if store.PathOpenCount() != 0 {
		t.Fatalf("path reopen count = %d, want 0", store.PathOpenCount())
	}
}

func TestContextCanceledDuringBlockingRead(t *testing.T) {
	store := NewStore()
	obj := store.Create("/images/cancel", []byte("blocked"))
	gate := NewBarrier()
	obj.SetReadGate(gate)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := MeasureAndExecute(ctx, obj, nil, consumeAll)
		errCh <- err
	}()

	select {
	case <-gate.Fired():
	case <-time.After(2 * time.Second):
		t.Fatal("measurement never reached the blocking read")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MeasureAndExecute did not respond to cancellation")
	}
}

func TestNoPathReopenUnderConcurrency(t *testing.T) {
	store := NewStore()
	store.Create("/images/concurrent", make([]byte, 4096))

	// 宿主只在初始化时按路径打开一次。
	handle, ok := store.OpenPath("/images/concurrent")
	if !ok {
		t.Fatal("initial OpenPath failed")
	}
	baseline := store.PathOpenCount()

	firstRead := NewBarrier()
	handle.SetReadGate(firstRead)

	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-firstRead.Fired():
			handle.ModifyInPlace([]byte("concurrent-writer-content"))
			firstRead.Release()
		case <-time.After(2 * time.Second):
			firstRead.Release()
		}
	}()

	result, err := MeasureAndExecute(context.Background(), handle, nil, consumeAll, WithMaxAttempts(5))
	<-done
	if err != nil {
		t.Fatalf("MeasureAndExecute: %v", err)
	}
	if result.Attempts < 2 {
		t.Fatalf("expected at least one retry under concurrent modification, got %d", result.Attempts)
	}

	if store.PathOpenCount() != baseline {
		t.Fatalf("path opens grew from %d to %d", baseline, store.PathOpenCount())
	}
}
