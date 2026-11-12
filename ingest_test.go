package netsec

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

func acceptAll(context.Context, string, STH) error {
	return nil
}

func newTestIngestor(t *testing.T, verifier STHVerifier, opts ...Option) *Ingestor {
	t.Helper()

	ingestor, err := NewIngestor(verifier, opts...)
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	return ingestor
}

func registerTestLog(t *testing.T, ingestor *Ingestor, logID string, mmd time.Duration) {
	t.Helper()

	if err := ingestor.RegisterLog(logID, mmd); err != nil {
		t.Fatalf("RegisterLog: %v", err)
	}
}

func testSTH(timestamp time.Time, treeSize uint64, marker string) STH {
	return STH{
		TreeSize:  treeSize,
		Timestamp: timestamp,
		RootHash:  []byte("root:" + marker),
		Signature: []byte("signature:" + marker),
	}
}

func requireStatus(t *testing.T, result IngestResult, want IngestStatus) {
	t.Helper()

	if result.Status != want {
		t.Fatalf("status = %q, want %q", result.Status, want)
	}
}

func TestIngestAcceptsVerifiedSTH(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ingestor := newTestIngestor(t, VerifyFunc(acceptAll))
	registerTestLog(t, ingestor, "log-a", 10*time.Minute)

	sth := testSTH(now, 42, "accepted")
	result, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID:      "log-a",
		SourceID:   "peer-a",
		STH:        sth,
		ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)

	latest, ok := ingestor.LatestFromSource("log-a", "peer-a")
	if !ok {
		t.Fatal("accepted STH was not retained")
	}
	if latest.LogID != "log-a" || latest.SourceID != "peer-a" {
		t.Fatalf("unexpected provenance: %+v", latest)
	}
	if latest.STH.TreeSize != sth.TreeSize {
		t.Fatalf("tree size = %d, want %d", latest.STH.TreeSize, sth.TreeSize)
	}
	if !bytes.Equal(latest.STH.RootHash, sth.RootHash) {
		t.Fatalf("root hash changed: %q != %q", latest.STH.RootHash, sth.RootHash)
	}
	if got := ingestor.Len("log-a", "peer-a"); got != 1 {
		t.Fatalf("retained entries = %d, want 1", got)
	}
}

func TestUnverifiedSTHsNeverEnterStore(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	var verifyCalls atomic.Int64
	verifier := VerifyFunc(func(_ context.Context, _ string, sth STH) error {
		verifyCalls.Add(1)
		if bytes.HasPrefix(sth.Signature, []byte("forged:")) {
			return errors.New("bad signature")
		}
		return nil
	})
	ingestor := newTestIngestor(t, verifier, WithMaxEntriesPerSourceLog(3))
	registerTestLog(t, ingestor, "log-a", 6*time.Hour)

	honest := testSTH(now, 100, "honest")
	result, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-a", STH: honest, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("honest ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)

	const forgedCount = 2000
	for j := 0; j < forgedCount; j++ {
		forged := STH{
			TreeSize:  uint64(100 + j),
			Timestamp: now.Add(-time.Duration(j) * time.Millisecond),
			RootHash:  []byte(fmt.Sprintf("forged-root:%d", j)),
			Signature: []byte(fmt.Sprintf("forged:signature:%d", j)),
		}
		result, err := ingestor.Ingest(context.Background(), IngestRequest{
			LogID: "log-a", SourceID: "peer-a", STH: forged, ReceivedAt: now,
		})
		requireStatus(t, result, IngestRejectedSignature)
		if !errors.Is(err, ErrSignatureVerification) {
			t.Fatalf("forged ingest error = %v, want ErrSignatureVerification", err)
		}
	}

	// A forged fork claim at the already accepted tree size must not replace
	// the honest root.
	forkClaim := STH{
		TreeSize:  honest.TreeSize,
		Timestamp: honest.Timestamp,
		RootHash:  []byte("forged-root:same-size"),
		Signature: []byte("forged:fork"),
	}
	result, err = ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-a", STH: forkClaim, ReceivedAt: now,
	})
	requireStatus(t, result, IngestRejectedSignature)
	if !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("fork ingest error = %v, want ErrSignatureVerification", err)
	}

	if got := ingestor.Len("log-a", "peer-a"); got != 1 {
		t.Fatalf("unverified entries changed retained count: %d", got)
	}
	latest, ok := ingestor.LatestFromSource("log-a", "peer-a")
	if !ok || !bytes.Equal(latest.STH.RootHash, honest.RootHash) {
		t.Fatalf("honest latest was replaced: %+v", latest)
	}
	if got := verifyCalls.Load(); got != forgedCount+2 {
		t.Fatalf("verify calls = %d, want %d", got, forgedCount+2)
	}

	other := testSTH(now.Add(time.Minute), 200, "other-source")
	result, err = ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-b", STH: other, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("other source ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)
	if got := ingestor.Len("log-a", "peer-b"); got != 1 {
		t.Fatalf("other source retained entries = %d, want 1", got)
	}
}

func TestTimeWindowBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	const mmd = 6 * time.Hour
	oldest := now.Add(-(mmd + RetentionWindowAfterMMD))

	tests := []struct {
		name string
		ts   time.Time
		want IngestStatus
	}{
		{name: "oldest edge accepted", ts: oldest, want: IngestAccepted},
		{name: "one nanosecond too old", ts: oldest.Add(-time.Nanosecond), want: IngestRejectedExpired},
		{name: "well inside window", ts: now.Add(-12 * time.Hour), want: IngestAccepted},
		{name: "future edge accepted", ts: now.Add(MaxFutureSkew), want: IngestAccepted},
		{name: "one nanosecond too far future", ts: now.Add(MaxFutureSkew + time.Nanosecond), want: IngestRejectedFuture},
		{name: "far future", ts: now.Add(24 * time.Hour), want: IngestRejectedFuture},
	}

	ingestor := newTestIngestor(t, VerifyFunc(acceptAll))
	registerTestLog(t, ingestor, "log-a", mmd)

	for j, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sth := testSTH(tc.ts, uint64(j), tc.name)
			result, err := ingestor.Ingest(context.Background(), IngestRequest{
				LogID: "log-a", SourceID: fmt.Sprintf("peer-%d", j), STH: sth, ReceivedAt: now,
			})
			if err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			requireStatus(t, result, tc.want)
			if tc.want == IngestAccepted && result.Status.Admitted() != true {
				t.Fatalf("accepted status did not report admission")
			}
		})
	}
}

func TestStaleFloodCannotEvictLatest(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	const mmd = time.Hour
	oldest := now.Add(-(mmd + RetentionWindowAfterMMD))

	var verifyCalls atomic.Int64
	verifier := VerifyFunc(func(_ context.Context, _ string, sth STH) error {
		verifyCalls.Add(1)
		return nil
	})
	ingestor := newTestIngestor(t, verifier, WithMaxEntriesPerSourceLog(4))
	registerTestLog(t, ingestor, "log-a", mmd)

	latestAttacker := testSTH(now.Add(-time.Minute), 1000, "attacker-latest")
	result, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-attacker", STH: latestAttacker, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("attacker latest ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)

	otherSource := testSTH(now.Add(2*time.Minute), 900, "other-new")
	result, err = ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-other", STH: otherSource, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("other source ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)

	const floodCount = 20000
	span := 23 * time.Hour
	for j := 0; j < floodCount; j++ {
		// Descending timestamps, all inside the admission window but older than
		// the attacker's real latest STH.
		ts := now.Add(-2*time.Minute - time.Duration(float64(span)*float64(j+1)/float64(floodCount)))
		if ts.Before(oldest) {
			t.Fatalf("test flood timestamp escaped window: %v < %v", ts, oldest)
		}
		stale := testSTH(ts, uint64(2000+j), fmt.Sprintf("stale-%d", j))
		result, err := ingestor.Ingest(context.Background(), IngestRequest{
			LogID: "log-a", SourceID: "peer-attacker", STH: stale, ReceivedAt: now,
		})
		if err != nil {
			t.Fatalf("stale ingest %d: %v", j, err)
		}
		if result.Status != IngestAccepted && result.Status != IngestRejectedCapacity {
			t.Fatalf("stale ingest %d status = %q", j, result.Status)
		}

		if j%1000 == 0 {
			latest, ok := ingestor.LatestFromSource("log-a", "peer-attacker")
			if !ok || !bytes.Equal(latest.STH.RootHash, latestAttacker.RootHash) {
				t.Fatalf("stale flood displaced latest attacker STH at %d: %+v", j, latest)
			}
			if got := ingestor.Len("log-a", "peer-other"); got != 1 {
				t.Fatalf("other source entry evicted at %d: len=%d", j, got)
			}
		}
	}

	if got := ingestor.Len("log-a", "peer-attacker"); got != 4 {
		t.Fatalf("attacker bucket len = %d, want cap 4", got)
	}
	latest, ok := ingestor.LatestFromSource("log-a", "peer-attacker")
	if !ok || !bytes.Equal(latest.STH.RootHash, latestAttacker.RootHash) {
		t.Fatalf("attacker latest STH was evicted: %+v", latest)
	}
	if got := ingestor.Len("log-a", "peer-other"); got != 1 {
		t.Fatalf("other source bucket len = %d, want 1", got)
	}
	globalLatest, ok := ingestor.Latest("log-a")
	if !ok || globalLatest.SourceID != "peer-other" {
		t.Fatalf("global latest = %+v, want peer-other", globalLatest)
	}
	if got := verifyCalls.Load(); got != floodCount+2 {
		t.Fatalf("verify calls = %d, want %d", got, floodCount+2)
	}
}

func TestPerSourceCapsAreIndependent(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ingestor := newTestIngestor(t, VerifyFunc(acceptAll), WithMaxEntriesPerSourceLog(3))
	registerTestLog(t, ingestor, "log-a", time.Hour)

	for j := 0; j < 100; j++ {
		sth := testSTH(now.Add(-40*time.Minute+time.Duration(j)*10*time.Second), uint64(j), fmt.Sprintf("a-%d", j))
		result, err := ingestor.Ingest(context.Background(), IngestRequest{
			LogID: "log-a", SourceID: "peer-a", STH: sth, ReceivedAt: now,
		})
		if err != nil {
			t.Fatalf("peer-a ingest %d: %v", j, err)
		}
		requireStatus(t, result, IngestAccepted)
	}

	for j := 0; j < 5; j++ {
		sth := testSTH(now.Add(-5*time.Minute+time.Duration(j)*10*time.Second), uint64(j), fmt.Sprintf("b-%d", j))
		result, err := ingestor.Ingest(context.Background(), IngestRequest{
			LogID: "log-a", SourceID: "peer-b", STH: sth, ReceivedAt: now,
		})
		if err != nil {
			t.Fatalf("peer-b ingest %d: %v", j, err)
		}
		requireStatus(t, result, IngestAccepted)
	}

	// An old entry from a third source is retained independently; no other
	// source's flood may evict it.
	third := testSTH(now.Add(-30*time.Minute), 500, "c-only")
	result, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-c", STH: third, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("peer-c ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)

	if got := ingestor.Len("log-a", "peer-a"); got != 3 {
		t.Fatalf("peer-a len = %d, want 3", got)
	}
	if got := ingestor.Len("log-a", "peer-b"); got != 3 {
		t.Fatalf("peer-b len = %d, want 3", got)
	}
	if got := ingestor.Len("log-a", "peer-c"); got != 1 {
		t.Fatalf("peer-c len = %d, want 1", got)
	}
	if got := len(ingestor.Entries("log-a")); got != 7 {
		t.Fatalf("total entries = %d, want 7", got)
	}

	aEntries := ingestor.EntriesFromSource("log-a", "peer-a")
	if got, want := len(aEntries), 3; got != want {
		t.Fatalf("peer-a entries len = %d, want %d", got, want)
	}
	for j, wantSize := range []uint64{99, 98, 97} {
		if aEntries[j].STH.TreeSize != wantSize {
			t.Fatalf("peer-a entry %d tree size = %d, want %d", j, aEntries[j].STH.TreeSize, wantSize)
		}
	}

	bLatest, ok := ingestor.LatestFromSource("log-a", "peer-b")
	if !ok || bLatest.STH.TreeSize != 4 {
		t.Fatalf("peer-b latest = %+v, want tree size 4", bLatest)
	}
	cLatest, ok := ingestor.LatestFromSource("log-a", "peer-c")
	if !ok || !bytes.Equal(cLatest.STH.RootHash, third.RootHash) {
		t.Fatalf("peer-c latest = %+v", cLatest)
	}
	global, ok := ingestor.Latest("log-a")
	if !ok || global.SourceID != "peer-b" {
		t.Fatalf("global latest = %+v, want peer-b", global)
	}
}

func TestMassiveExpiredSTHsAreDroppedBeforeVerification(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	const mmd = time.Hour
	oldest := now.Add(-(mmd + RetentionWindowAfterMMD))

	var verifyCalls atomic.Int64
	verifier := VerifyFunc(func(_ context.Context, _ string, _ STH) error {
		verifyCalls.Add(1)
		return nil
	})
	ingestor := newTestIngestor(t, verifier, WithMaxEntriesPerSourceLog(8))
	registerTestLog(t, ingestor, "log-a", mmd)

	const expiredCount = 30000
	for j := 0; j < expiredCount; j++ {
		sth := testSTH(oldest.Add(-time.Duration(j+1)*time.Second), uint64(j), fmt.Sprintf("expired-%d", j))
		result, err := ingestor.Ingest(context.Background(), IngestRequest{
			LogID: "log-a", SourceID: "peer-attacker", STH: sth, ReceivedAt: now,
		})
		if err != nil {
			t.Fatalf("expired ingest %d: %v", j, err)
		}
		requireStatus(t, result, IngestRejectedExpired)
	}

	if got := ingestor.Len("log-a", "peer-attacker"); got != 0 {
		t.Fatalf("expired flood retained %d entries", got)
	}
	if _, ok := ingestor.Latest("log-a"); ok {
		t.Fatal("expired flood produced a latest STH")
	}
	if got := verifyCalls.Load(); got != 0 {
		t.Fatalf("expired STHs reached verifier %d times", got)
	}

	fresh := testSTH(now, 1, "fresh-after-flood")
	result, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-good", STH: fresh, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("fresh ingest: %v", err)
	}
	requireStatus(t, result, IngestAccepted)
}

func TestDuplicateIsIdempotentAndSourceScoped(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ingestor := newTestIngestor(t, VerifyFunc(acceptAll), WithMaxEntriesPerSourceLog(2))
	registerTestLog(t, ingestor, "log-a", time.Hour)

	sth := testSTH(now, 10, "duplicate")
	first, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-a", STH: sth, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	requireStatus(t, first, IngestAccepted)

	second, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-a", STH: sth, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("duplicate ingest: %v", err)
	}
	requireStatus(t, second, IngestDuplicate)
	if second.Evicted != nil {
		t.Fatalf("duplicate evicted an entry: %+v", second.Evicted)
	}
	if got := ingestor.Len("log-a", "peer-a"); got != 1 {
		t.Fatalf("duplicate created entries: %d", got)
	}

	otherSource, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "peer-b", STH: sth, ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("other source ingest: %v", err)
	}
	requireStatus(t, otherSource, IngestAccepted)
	if got := ingestor.Len("log-a", "peer-b"); got != 1 {
		t.Fatalf("same STH from another source was not retained: %d", got)
	}
}

func TestConfigurationAndUnknownLog(t *testing.T) {
	if _, err := NewIngestor(nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil verifier error = %v", err)
	}
	if _, err := NewIngestor(VerifyFunc(nil)); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil verify func error = %v", err)
	}
	if _, err := NewIngestor(VerifyFunc(acceptAll), WithMaxEntriesPerSourceLog(0)); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("zero cap error = %v", err)
	}
	if _, err := NewIngestor(VerifyFunc(acceptAll), WithClock(nil)); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil clock error = %v", err)
	}

	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	var verifyCalls atomic.Int64
	ingestor := newTestIngestor(t, VerifyFunc(func(_ context.Context, _ string, _ STH) error {
		verifyCalls.Add(1)
		return nil
	}))
	if err := ingestor.RegisterLog("", time.Hour); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("empty log ID error = %v", err)
	}
	if err := ingestor.RegisterLog("log-a", -time.Second); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("negative MMD error = %v", err)
	}

	result, err := ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "missing", SourceID: "peer-a", STH: testSTH(now, 1, "missing"), ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("unknown log ingest: %v", err)
	}
	requireStatus(t, result, IngestRejectedUnknownLog)
	if got := verifyCalls.Load(); got != 0 {
		t.Fatalf("unknown log reached verifier %d times", got)
	}

	result, err = ingestor.Ingest(context.Background(), IngestRequest{
		LogID: "log-a", SourceID: "", STH: testSTH(now, 1, "missing-source"), ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("invalid request ingest: %v", err)
	}
	requireStatus(t, result, IngestRejectedInvalid)
}

func TestConcurrentIngestRespectsPerSourceCap(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	const (
		sourceCount = 8
		perSource   = 500
		capPerLog   = 5
	)

	ingestor := newTestIngestor(t, VerifyFunc(acceptAll), WithMaxEntriesPerSourceLog(capPerLog))
	registerTestLog(t, ingestor, "log-a", time.Hour)

	var wg sync.WaitGroup
	errs := make(chan error, sourceCount)
	for source := 0; source < sourceCount; source++ {
		sourceID := fmt.Sprintf("peer-%d", source)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perSource; j++ {
				sth := testSTH(now.Add(-time.Duration(j)*time.Second), uint64(j), fmt.Sprintf("%s-%d", sourceID, j))
				result, err := ingestor.Ingest(context.Background(), IngestRequest{
					LogID: "log-a", SourceID: sourceID, STH: sth, ReceivedAt: now,
				})
				if err != nil {
					errs <- err
					return
				}
				if result.Status != IngestAccepted && result.Status != IngestRejectedCapacity {
					errs <- fmt.Errorf("%s entry %d status %q", sourceID, j, result.Status)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ingest: %v", err)
	}

	for source := 0; source < sourceCount; source++ {
		sourceID := fmt.Sprintf("peer-%d", source)
		if got := ingestor.Len("log-a", sourceID); got != capPerLog {
			t.Fatalf("%s len = %d, want %d", sourceID, got, capPerLog)
		}
		latest, ok := ingestor.LatestFromSource("log-a", sourceID)
		if !ok || latest.STH.TreeSize != 0 {
			t.Fatalf("%s latest = %+v, want newest tree size 0", sourceID, latest)
		}
	}
}
