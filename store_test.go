package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustCompress(t *testing.T, s *Store, tenant string, label Label, src []byte) Compressed {
	t.Helper()
	res, err := s.Compress(context.Background(), tenant, label, src)
	if err != nil {
		t.Fatalf("Compress(%q, %s): %v", tenant, label, err)
	}
	return res
}

func mustDecompress(t *testing.T, s *Store, tenant string, label Label, res Compressed, want []byte) {
	t.Helper()
	got, err := s.Decompress(context.Background(), tenant, label, res.Version, res.Data)
	if err != nil {
		t.Fatalf("Decompress(%q, %s): %v", tenant, label, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
	}
}

func TestSameTenantLegitimateReuse(t *testing.T) {
	s := NewStore()
	src := bytes.Repeat([]byte("tenant-secret-material/"), 80)

	first := mustCompress(t, s, "tenant-a", Confidential, src)
	if first.Version.Offset != 0 {
		t.Fatalf("first stream offset = %d, want 0", first.Version.Offset)
	}
	second := mustCompress(t, s, "tenant-a", Confidential, src)
	if second.Version.Offset != uint64(len(src)) {
		t.Fatalf("second stream offset = %d, want %d", second.Version.Offset, len(src))
	}
	if second.Len() >= first.Len()/2 {
		t.Fatalf("legitimate reuse did not help: first=%d second=%d", first.Len(), second.Len())
	}

	mustDecompress(t, s, "tenant-a", Confidential, first, src)
	mustDecompress(t, s, "tenant-a", Confidential, second, src)
}

func TestDictionaryVisibilityIsFixedAtOpen(t *testing.T) {
	s := NewStore()
	src := []byte("visibility-secret-material")

	a, err := s.OpenStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Write(src); err != nil {
		t.Fatal(err)
	}

	// Opened before A commits: must retain the empty snapshot.
	b, err := s.OpenStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatal(err)
	}
	if b.Version().Offset != 0 {
		t.Fatalf("pre-commit stream offset = %d, want 0", b.Version().Offset)
	}
	if _, err := b.Write(src); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Finish(); err != nil {
		t.Fatalf("A.Finish: %v", err)
	}

	// Opened after A commits: sees exactly A's plaintext.
	c, err := s.OpenStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version().Offset != uint64(len(src)) {
		t.Fatalf("post-commit stream offset = %d, want %d", c.Version().Offset, len(src))
	}
	if _, err := c.Write(src); err != nil {
		t.Fatal(err)
	}

	bres, err := b.Finish()
	if err != nil {
		t.Fatalf("B.Finish: %v", err)
	}
	if !bytes.Equal(bres.Data, Compress(nil, src)) {
		t.Fatal("stream opened before commit saw the new dictionary")
	}

	cres, err := c.Finish()
	if err != nil {
		t.Fatalf("C.Finish: %v", err)
	}
	if !bytes.Equal(cres.Data, Compress(src, src)) {
		t.Fatal("stream opened after commit did not see the committed dictionary")
	}
	if cres.Len() >= bres.Len() {
		t.Fatalf("post-commit stream should be shorter: %d vs %d", cres.Len(), bres.Len())
	}
}

func TestCancelPublishesNothing(t *testing.T) {
	s := NewStore()
	secret := []byte("cancelled-secret-material")

	st, err := s.OpenStream(context.Background(), "tenant-a", Confidential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(secret); err != nil {
		t.Fatal(err)
	}
	st.Cancel()
	if _, err := st.Finish(); !errors.Is(err, ErrStreamCanceled) {
		t.Fatalf("Finish after Cancel = %v, want ErrStreamCanceled", err)
	}
	if _, err := st.Write([]byte("x")); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Write after Cancel = %v, want ErrStreamClosed", err)
	}

	next, err := s.OpenStream(context.Background(), "tenant-a", Confidential)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version().Offset != 0 {
		t.Fatalf("canceled stream published %d bytes", next.Version().Offset)
	}
	res, err := next.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Data, Compress(nil, nil)) {
		t.Fatal("unexpected output for empty stream")
	}
}

func TestContextCancellationPublishesNothing(t *testing.T) {
	t.Run("cancel before finish", func(t *testing.T) {
		s := NewStore()
		ctx, cancel := context.WithCancel(context.Background())
		st, err := s.OpenStream(ctx, "tenant-a", Public)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Write([]byte("not-published")); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := st.Finish(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Finish = %v, want context.Canceled", err)
		}
		next, err := s.OpenStream(context.Background(), "tenant-a", Public)
		if err != nil {
			t.Fatal(err)
		}
		if next.Version().Offset != 0 {
			t.Fatalf("canceled stream published %d bytes", next.Version().Offset)
		}
	})

	t.Run("cancel during compression", func(t *testing.T) {
		s := NewStore()
		ctx, cancel := context.WithCancel(context.Background())
		st, err := s.OpenStream(ctx, "tenant-a", Public)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Write(randomBytes(4<<20, 99)); err != nil {
			t.Fatal(err)
		}

		done := make(chan struct{})
		go func() {
			time.Sleep(time.Millisecond)
			cancel()
			st.Cancel()
			close(done)
		}()

		_, err = st.Finish()
		<-done
		if err == nil {
			t.Fatal("Finish succeeded despite cancellation")
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrStreamCanceled) {
			t.Fatalf("Finish = %v, want cancellation error", err)
		}

		next, err := s.OpenStream(context.Background(), "tenant-a", Public)
		if err != nil {
			t.Fatal(err)
		}
		if next.Version().Offset != 0 {
			t.Fatalf("cancelled compression published %d bytes", next.Version().Offset)
		}
	})
}

func TestResetTenantInvalidatesStreamsAndVersions(t *testing.T) {
	s := NewStore()
	secret := bytes.Repeat([]byte("reset-secret/"), 40)
	old := mustCompress(t, s, "tenant-a", Public, secret)
	oldEpoch := old.Version.Epoch

	inflight, err := s.OpenStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inflight.Write([]byte("inflight-secret")); err != nil {
		t.Fatal(err)
	}

	if err := s.ResetTenant("tenant-a"); err != nil {
		t.Fatalf("ResetTenant: %v", err)
	}
	if _, err := inflight.Finish(); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("in-flight Finish after reset = %v, want ErrStaleDictionary", err)
	}
	if _, err := s.Decompress(context.Background(), "tenant-a", Public, old.Version, old.Data); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("old version decode after reset = %v, want ErrStaleDictionary", err)
	}

	fresh, err := s.OpenStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Version().Offset != 0 {
		t.Fatalf("reset partition offset = %d, want 0", fresh.Version().Offset)
	}
	if fresh.Version().Epoch == oldEpoch {
		t.Fatal("reset did not allocate a new epoch")
	}
	res := mustCompress(t, s, "tenant-a", Public, secret)
	if !bytes.Equal(res.Data, Compress(nil, secret)) {
		t.Fatal("reset dictionary leaked into new stream")
	}
	mustDecompress(t, s, "tenant-a", Public, res, secret)
}

func TestReleasedTenantIDReuseStartsFresh(t *testing.T) {
	s := NewStore()
	secret := bytes.Repeat([]byte("reuse-secret/"), 40)
	old := mustCompress(t, s, "tenant-a", Confidential, secret)
	oldEpoch := old.Version.Epoch

	inflight, err := s.OpenStream(context.Background(), "tenant-a", Confidential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inflight.Write([]byte("old-identity")); err != nil {
		t.Fatal(err)
	}

	if err := s.ReleaseTenant("tenant-a"); err != nil {
		t.Fatalf("ReleaseTenant: %v", err)
	}
	if _, err := inflight.Finish(); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("in-flight Finish after release = %v, want ErrStaleDictionary", err)
	}
	if _, err := s.Decompress(context.Background(), "tenant-a", Confidential, old.Version, old.Data); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("old version decode after release = %v, want ErrStaleDictionary", err)
	}

	if err := s.RegisterTenant("tenant-a"); err != nil {
		t.Fatalf("re-register tenant: %v", err)
	}
	epoch, ok := s.TenantEpoch("tenant-a")
	if !ok || epoch == oldEpoch {
		t.Fatalf("reused tenant epoch = %d, ok=%v; old epoch %d", epoch, ok, oldEpoch)
	}

	res := mustCompress(t, s, "tenant-a", Confidential, secret)
	if !bytes.Equal(res.Data, Compress(nil, secret)) {
		t.Fatal("reused tenant ID saw the old dictionary")
	}
	if _, err := s.Decompress(context.Background(), "tenant-a", Confidential, old.Version, old.Data); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("old version decode after reuse = %v, want ErrStaleDictionary", err)
	}
	mustDecompress(t, s, "tenant-a", Confidential, res, secret)
}

func TestVersionMustMatchTenantAndLabel(t *testing.T) {
	s := NewStore()
	res := mustCompress(t, s, "tenant-a", Public, []byte("versioned-secret"))

	if _, err := s.Decompress(context.Background(), "tenant-b", Public, res.Version, res.Data); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("cross-tenant version error = %v, want ErrVersionMismatch", err)
	}
	if _, err := s.Decompress(context.Background(), "tenant-a", Confidential, res.Version, res.Data); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("cross-label version error = %v, want ErrVersionMismatch", err)
	}
}

func TestConcurrentSnapshotAndCommit(t *testing.T) {
	s := NewStore()
	const n = 24
	messages := make([][]byte, n)
	streams := make([]*Stream, n)

	for i := 0; i < n; i++ {
		messages[i] = bytes.Repeat([]byte(fmt.Sprintf("stream-%02d/", i)), 80+i)
		st, err := s.OpenStream(context.Background(), "shared", Public)
		if err != nil {
			t.Fatal(err)
		}
		if st.Version().Offset != 0 {
			t.Fatalf("stream %d opened with offset %d, want 0", i, st.Version().Offset)
		}
		streams[i] = st
		if _, err := st.Write(messages[i]); err != nil {
			t.Fatal(err)
		}
	}

	results := make([]Compressed, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			res, err := streams[i].Finish()
			if err != nil {
				errs <- fmt.Errorf("stream %d: %w", i, err)
				return
			}
			results[i] = res
			errs <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < n; i++ {
		if results[i].Version.Offset != 0 {
			t.Fatalf("stream %d commit version offset = %d, want snapshot 0", i, results[i].Version.Offset)
		}
		if !bytes.Equal(results[i].Data, Compress(nil, messages[i])) {
			t.Fatalf("stream %d did not use its open-time snapshot", i)
		}
	}

	decodeErrs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			got, err := s.Decompress(context.Background(), "shared", Public, results[i].Version, results[i].Data)
			if err != nil {
				decodeErrs <- err
				return
			}
			if !bytes.Equal(got, messages[i]) {
				decodeErrs <- fmt.Errorf("stream %d decoded mismatch", i)
				return
			}
			decodeErrs <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-decodeErrs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentCompressAndDecodeSamePartition(t *testing.T) {
	s := NewStore()
	const (
		workers = 16
		rounds  = 12
	)

	mustCompress(t, s, "shared", Confidential, []byte("seed-dictionary-material/seed-dictionary-material/"))

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				src := bytes.Repeat([]byte(fmt.Sprintf("worker-%02d-round-%02d/", i, j)), 30+j)
				res, err := s.Compress(context.Background(), "shared", Confidential, src)
				if err != nil {
					errs <- err
					return
				}
				got, err := s.Decompress(context.Background(), "shared", Confidential, res.Version, res.Data)
				if err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(got, src) {
					errs <- fmt.Errorf("worker %d round %d decoded mismatch", i, j)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
