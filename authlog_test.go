package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	testKey      = []byte("0123456789abcdef0123456789abcdef")
	testOldMeta  = []byte(`{"name":"old","mode":"0644"}`)
	testNewMeta  = []byte(`{"name":"new","mode":"0644"}`)
	testOldData  = []byte("old-data")
	testNewData  = []byte("new-data")
	testObjectID = "object-7"
	testPolicy   = DigestString("policy-v1")
)

func testAuthority(t *testing.T) *Authority {
	t.Helper()
	a := NewAuthority([]byte("policy-v1"))
	a.GrantAll("alice", 1)
	return a
}

func newTestHost(t *testing.T, root string, auth Authorizer, opts Options) *Host {
	t.Helper()
	opts.Root = root
	opts.HMACKey = testKey
	opts.Authorizer = auth
	if opts.Applier == nil {
		opts.Applier = ReplacePatch{}
	}
	h, err := NewHost(opts)
	if err != nil && h == nil {
		t.Fatalf("NewHost: %v", err)
	}
	return h
}

func seedObject(t *testing.T, root string) {
	t.Helper()
	store, err := NewFileStore(filepath.Join(root, "objects"), filepath.Join(root, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateObject(context.Background(), ObjectRef{ID: testObjectID, Generation: 0}, testOldMeta, testOldData); err != nil {
		t.Fatal(err)
	}
}

func writeRequest(id TxID, patch, data []byte) WriteRequest {
	return WriteRequest{
		TxID:                 id,
		PrincipalID:          "alice",
		CredentialGeneration: 1,
		Object:               ObjectRef{ID: testObjectID, Generation: 0},
		Operation:            OpWrite,
		Params:               []byte("params-" + id),
		OldMetadataDigest:    DigestBytes(testOldMeta),
		Patch:                patch,
		Data:                 data,
	}
}

func TestBasicImportableWrite(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	auth := testAuthority(t)
	h := newTestHost(t, root, auth, Options{})
	defer h.Close()

	receipt, err := h.Execute(context.Background(), writeRequest("tx-basic", testNewMeta, testNewData))
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Published {
		t.Fatal("receipt not published")
	}
	obj, err := h.ReadObjectByID(context.Background(), testObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Metadata) != string(testNewMeta) || string(obj.Data) != string(testNewData) {
		t.Fatalf("object=%q/%q", obj.Metadata, obj.Data)
	}
	records := h.j.trustedRecords()
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}
	if records[0].Stage != StagePrepare || records[1].Stage != StageDataStable || records[2].Stage != StageCommit {
		t.Fatalf("stages=%s,%s,%s", records[0].Stage, records[1].Stage, records[2].Stage)
	}
}

// snapshotAtStage runs one transaction until the requested durable stage,
// clones the whole host directory, and then aborts the online operation.
func snapshotAtStage(t *testing.T, stage Stage) (snapshot string, logBytes []byte, key []byte) {
	t.Helper()
	root := t.TempDir()
	seedObject(t, root)
	snapshot = filepath.Join(t.TempDir(), "snapshot")
	stop := errors.New("injected crash")
	auth := testAuthority(t)
	hook := func(_ context.Context, current Stage, _ TxID) error {
		if current != stage {
			return nil
		}
		if err := cloneDir(root, snapshot); err != nil {
			t.Fatalf("clone: %v", err)
		}
		return stop
	}
	h := newTestHost(t, root, auth, Options{OnStage: hook})
	_, err := h.Execute(context.Background(), writeRequest("tx-snapshot", testNewMeta, testNewData))
	if stage == StagePublished {
		if err == nil {
			t.Fatal("expected injected stop")
		}
	} else if !errors.Is(err, stop) && !errors.Is(err, ErrCommittedPending) {
		t.Fatalf("execute at %s: %v", stage, err)
	}
	_ = h.Close()
	logBytes, err = os.ReadFile(filepath.Join(snapshot, "auth.log"))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, logBytes, append([]byte(nil), testKey...)
}

func anyCommit(records []Record) bool {
	for _, rec := range records {
		if rec.Stage == StageCommit {
			return true
		}
	}
	return false
}

func TestEveryBytePrefixOfEveryDurableWriteStage(t *testing.T) {
	for _, stage := range []Stage{StagePrepare, StageDataStable, StageCommit} {
		stage := stage
		t.Run(string(stage), func(t *testing.T) {
			snapshot, logBytes, key := snapshotAtStage(t, stage)
			for cut := 0; cut <= len(logBytes); cut++ {
				root := filepath.Join(t.TempDir(), "root")
				if err := cloneDir(snapshot, root); err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(filepath.Join(root, "auth.log"), int64(cut)); err != nil {
					t.Fatal(err)
				}
				trusted, _ := ParseJournalBytes(logBytes[:cut], key)
				wantNew := anyCommit(trusted)
				auth := testAuthority(t)
				h, err := Open(root, Options{HMACKey: key, Authorizer: auth, Applier: ReplacePatch{}})
				if err != nil && !errors.Is(err, ErrLogCompromised) {
					t.Fatalf("stage %s open cut=%d: %v", stage, cut, err)
				}
				if h == nil {
					t.Fatalf("stage %s cut=%d nil host", stage, cut)
				}
				obj, readErr := h.ReadObjectByID(context.Background(), testObjectID)
				_ = h.Close()
				if readErr != nil {
					t.Fatalf("stage %s cut=%d read: %v", stage, cut, readErr)
				}
				if wantNew {
					if string(obj.Metadata) != string(testNewMeta) || string(obj.Data) != string(testNewData) {
						t.Fatalf("stage %s cut=%d committed but visible=%q/%q", stage, cut, obj.Metadata, obj.Data)
					}
				} else {
					if string(obj.Metadata) != string(testOldMeta) || string(obj.Data) != string(testOldData) {
						t.Fatalf("stage %s cut=%d uncommitted visible write=%q/%q", stage, cut, obj.Metadata, obj.Data)
					}
				}
				staging, globErr := filepath.Glob(filepath.Join(root, "staging", "*"))
				if globErr != nil {
					t.Fatal(globErr)
				}
				if len(staging) != 0 {
					t.Fatalf("stage %s cut=%d orphan staging files: %v", stage, cut, staging)
				}
			}
		})
	}
}

func TestEveryFrameBoundaryPrefix(t *testing.T) {
	for _, stage := range []Stage{StagePrepare, StageDataStable, StageCommit} {
		snapshot, logBytes, key := snapshotAtStage(t, stage)
		frames := frameBoundaries(logBytes, key)
		boundaries := []int{0}
		boundaries = append(boundaries, frames...)
		boundaries = append(boundaries, len(logBytes))
		for _, cut := range boundaries {
			root := filepath.Join(t.TempDir(), "root")
			if err := cloneDir(snapshot, root); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(filepath.Join(root, "auth.log"), int64(cut)); err != nil {
				t.Fatal(err)
			}
			trusted, _ := ParseJournalBytes(logBytes[:cut], key)
			wantNew := anyCommit(trusted)
			h, err := Open(root, Options{HMACKey: key, Authorizer: testAuthority(t), Applier: ReplacePatch{}})
			if err != nil && !errors.Is(err, ErrLogCompromised) {
				t.Fatalf("stage %s cut=%d: %v", stage, cut, err)
			}
			obj, err := h.ReadObjectByID(context.Background(), testObjectID)
			if err != nil {
				_ = h.Close()
				t.Fatalf("stage %s cut=%d read: %v", stage, cut, err)
			}
			if wantNew && (string(obj.Metadata) != string(testNewMeta) || string(obj.Data) != string(testNewData)) {
				_ = h.Close()
				t.Fatalf("stage %s cut=%d committed but visible=%q/%q", stage, cut, obj.Metadata, obj.Data)
			}
			if !wantNew && (string(obj.Metadata) != string(testOldMeta) || string(obj.Data) != string(testOldData)) {
				_ = h.Close()
				t.Fatalf("stage %s cut=%d uncommitted visible write=%q/%q", stage, cut, obj.Metadata, obj.Data)
			}
			_ = h.Close()
		}
	}
}

func frameBoundaries(logBytes, key []byte) []int {
	var out []int
	offset := 0
	for offset < len(logBytes) {
		if len(logBytes)-offset < logHeaderSize {
			break
		}
		n := int(binaryBigEndianUint32(logBytes[offset+offPayloadLen : offset+offPayloadLen+4]))
		end := offset + logHeaderSize + n
		if end > len(logBytes) {
			break
		}
		out = append(out, end)
		offset = end
	}
	return out
}

func binaryBigEndianUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func cloneDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func TestRevocationAndCredentialRotationNeverCompleteUncommitted(t *testing.T) {
	for _, stage := range []Stage{StagePrepare, StageDataStable} {
		snapshot, _, key := snapshotAtStage(t, stage)
		root := filepath.Join(t.TempDir(), "root")
		if err := cloneDir(snapshot, root); err != nil {
			t.Fatal(err)
		}
		auth := NewAuthority([]byte("policy-v1"))
		auth.GrantAll("alice", 1)
		auth.Revoke("alice")
		h, err := Open(root, Options{HMACKey: key, Authorizer: auth, Applier: ReplacePatch{}})
		if err != nil {
			t.Fatalf("stage %s recover: %v", stage, err)
		}
		obj, err := h.ReadObjectByID(context.Background(), testObjectID)
		if err != nil {
			t.Fatal(err)
		}
		if string(obj.Metadata) != string(testOldMeta) {
			t.Fatalf("revoked uncommitted metadata became visible: %q", obj.Metadata)
		}
		// Even though the log contains a Prepare/DataStable, a new online
		// attempt with the revoked credential must not resume it.
		req := writeRequest("tx-after-revoke", testNewMeta, testNewData)
		if _, err := h.Execute(context.Background(), req); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("post-revoke execute err=%v", err)
		}
		_ = h.Close()

		// Same principal name, new credential generation.  The uncommitted
		// old-generation transaction still must not be adopted.
		root2 := filepath.Join(t.TempDir(), "root")
		if err := cloneDir(snapshot, root2); err != nil {
			t.Fatal(err)
		}
		auth2 := NewAuthority([]byte("policy-v1"))
		auth2.GrantAll("alice", 2)
		h2, err := Open(root2, Options{HMACKey: key, Authorizer: auth2, Applier: ReplacePatch{}})
		if err != nil {
			t.Fatalf("stage %s generation rotate recover: %v", stage, err)
		}
		obj2, err := h2.ReadObjectByID(context.Background(), testObjectID)
		if err != nil {
			t.Fatal(err)
		}
		if string(obj2.Metadata) != string(testOldMeta) {
			t.Fatalf("same-name new-generation adopted old auth: %q", obj2.Metadata)
		}
		if _, err := h2.Execute(context.Background(), writeRequest("tx-old-gen", testNewMeta, testNewData)); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("old credential generation err=%v", err)
		}
		newReq := writeRequest("tx-new-gen", testNewMeta, testNewData)
		newReq.CredentialGeneration = 2
		if _, err := h2.Execute(context.Background(), newReq); err != nil {
			t.Fatalf("new generation execute: %v", err)
		}
		_ = h2.Close()
	}
}

func TestCommittedSurvivesRevocationAndNeverReauthorizes(t *testing.T) {
	snapshot, _, key := snapshotAtStage(t, StageCommit)
	root := filepath.Join(t.TempDir(), "root")
	if err := cloneDir(snapshot, root); err != nil {
		t.Fatal(err)
	}
	counter := &countingAuthorizer{inner: NewAuthority([]byte("policy-v1"))}
	counter.inner.Revoke("alice")
	counter.inner.SetPolicy([]byte("policy-v2"))
	counter.inner.GrantAll("alice", 2)
	h, err := Open(root, Options{HMACKey: key, Authorizer: counter, Applier: ReplacePatch{}})
	if err != nil {
		t.Fatalf("recover committed after revoke: %v", err)
	}
	if counter.calls != 0 {
		t.Fatalf("recovery called authorizer %d times", counter.calls)
	}
	obj, err := h.ReadObjectByID(context.Background(), testObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Metadata) != string(testNewMeta) || string(obj.Data) != string(testNewData) {
		t.Fatalf("committed result lost after revoke: %q/%q", obj.Metadata, obj.Data)
	}
	_ = h.Close()
}

type countingAuthorizer struct {
	inner *Authority
	calls int
}

func (c *countingAuthorizer) Authorize(ctx context.Context, req AuthRequest) (Authorization, error) {
	c.calls++
	return c.inner.Authorize(ctx, req)
}

func TestObjectNumberReuseDoesNotMigrateAuthorization(t *testing.T) {
	for _, stage := range []Stage{StageDataStable, StageCommit} {
		snapshot, _, key := snapshotAtStage(t, stage)
		root := filepath.Join(t.TempDir(), "root")
		if err := cloneDir(snapshot, root); err != nil {
			t.Fatal(err)
		}
		store, err := NewFileStore(filepath.Join(root, "objects"), filepath.Join(root, "staging"))
		if err != nil {
			t.Fatal(err)
		}
		newMeta := []byte("generation-2-meta")
		newData := []byte("generation-2-data")
		if err := store.CreateObject(context.Background(), ObjectRef{ID: testObjectID, Generation: 1}, newMeta, newData); err != nil {
			t.Fatalf("reuse object: %v", err)
		}
		h, err := Open(root, Options{HMACKey: key, Authorizer: testAuthority(t), Applier: ReplacePatch{}})
		if stage == StageCommit && err == nil {
			_ = h.Close()
			t.Fatalf("committed old-generation transaction unexpectedly completed after reuse")
		}
		if stage == StageDataStable && err != nil && !errors.Is(err, ErrLogCompromised) {
			t.Fatalf("recover data-stable reuse: %v", err)
		}
		if h == nil {
			t.Fatal("nil host")
		}
		obj, err := h.ReadObject(context.Background(), ObjectRef{ID: testObjectID, Generation: 1})
		if err != nil {
			t.Fatal(err)
		}
		if string(obj.Metadata) != string(newMeta) || string(obj.Data) != string(newData) {
			t.Fatalf("stage %s old auth migrated into new generation: %q/%q", stage, obj.Metadata, obj.Data)
		}
		_ = h.Close()
	}
}

func TestReusedObjectAfterPublishedCommitDoesNotConflict(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	auth := testAuthority(t)
	h := newTestHost(t, root, auth, Options{})
	if _, err := h.Execute(context.Background(), writeRequest("tx-applied-before-reuse", testNewMeta, testNewData)); err != nil {
		t.Fatal(err)
	}
	_ = h.Close()

	store, err := NewFileStore(filepath.Join(root, "objects"), filepath.Join(root, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateObject(context.Background(), ObjectRef{ID: testObjectID, Generation: 1}, []byte("gen1-meta"), []byte("gen1-data")); err != nil {
		t.Fatal(err)
	}
	h2, err := Open(root, Options{HMACKey: testKey, Authorizer: auth, Applier: ReplacePatch{}})
	if err != nil {
		t.Fatalf("published old-generation commit blocked reuse: %v", err)
	}
	defer h2.Close()
	obj, err := h2.ReadObject(context.Background(), ObjectRef{ID: testObjectID, Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Metadata) != "gen1-meta" || string(obj.Data) != "gen1-data" {
		t.Fatalf("old authorization migrated: %q/%q", obj.Metadata, obj.Data)
	}
}

func TestConcurrentMetadataCAS(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	auth := testAuthority(t)
	h := newTestHost(t, root, auth, Options{})
	defer h.Close()

	const workers = 8
	type result struct {
		id   TxID
		meta []byte
		err  error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("tx-cas-%d", i)
			meta := []byte(fmt.Sprintf("winner-%d", i))
			req := writeRequest(id, meta, nil)
			_, err := h.Execute(context.Background(), req)
			results <- result{id: id, meta: meta, err: err}
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	var winner []byte
	for r := range results {
		if r.err == nil {
			successes++
			winner = r.meta
		} else if !errors.Is(r.err, ErrMetadataConflict) {
			t.Fatalf("%s unexpected error: %v", r.id, r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("got %d CAS successes, want 1", successes)
	}
	obj, err := h.ReadObjectByID(context.Background(), testObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Metadata) != string(winner) {
		t.Fatalf("visible metadata=%q winner=%q", obj.Metadata, winner)
	}
	staged, err := filepath.Glob(filepath.Join(root, "staging", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("staging remnants: %v", staged)
	}
}

func TestDuplicateTxIDDifferentContentIsolatesSuffix(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	idA := rawIdentity("dup", "object-A", testOldMeta, testNewMeta)
	idB := rawIdentity("dup", "object-B", testOldMeta, testNewMeta)
	frames := []Record{
		testRecord(idA, StagePrepare, testNewMeta),
		testRecord(idB, StagePrepare, testNewMeta),
	}
	writeRawJournal(t, filepath.Join(root, "auth.log"), testKey, frames)
	h, err := Open(root, Options{HMACKey: testKey, Authorizer: testAuthority(t), Applier: ReplacePatch{}})
	if err == nil || !errors.Is(err, ErrLogCompromised) {
		if h != nil {
			_ = h.Close()
		}
		t.Fatalf("duplicate txID not isolated: %v", err)
	}
	defer h.Close()
	obj, readErr := h.ReadObjectByID(context.Background(), testObjectID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(obj.Metadata) != string(testOldMeta) {
		t.Fatalf("duplicate log produced visible write")
	}
}

func TestChainForkIsolatesSuffix(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	id1 := rawIdentity("fork-1", testObjectID, testOldMeta, testNewMeta)
	id2 := rawIdentity("fork-2", testObjectID, testOldMeta, testNewMeta)
	id3 := rawIdentity("fork-3", testObjectID, testOldMeta, testNewMeta)

	rec1 := testRecord(id1, StagePrepare, testNewMeta)
	frame1 := rawFrame(rec1, 1, ZeroDigest, testKey)
	rec2a := testRecord(id2, StagePrepare, testNewMeta)
	frame2a := rawFrame(rec2a, 2, frame1.digest, testKey)
	rec2b := testRecord(id3, StagePrepare, testNewMeta)
	frame2b := rawFrame(rec2b, 2, frame1.digest, testKey)
	data := append(frame1.bytes, frame2a.bytes...)
	data = append(data, frame2b.bytes...)
	if err := os.WriteFile(filepath.Join(root, "auth.log"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := Open(root, Options{HMACKey: testKey, Authorizer: testAuthority(t), Applier: ReplacePatch{}})
	if err == nil || !errors.Is(err, ErrLogCompromised) {
		if h != nil {
			_ = h.Close()
		}
		t.Fatalf("fork not isolated: %v", err)
	}
	defer h.Close()
	obj, readErr := h.ReadObjectByID(context.Background(), testObjectID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(obj.Metadata) != string(testOldMeta) {
		t.Fatalf("fork produced visible write")
	}
}

func TestHMACFailureIsolatesLaterCommit(t *testing.T) {
	snapshot, logBytes, key := snapshotAtStage(t, StageCommit)
	if len(logBytes) == 0 {
		t.Fatal("empty log")
	}
	tampered := append([]byte(nil), logBytes...)
	tampered[len(tampered)-1] ^= 0x01
	root := filepath.Join(t.TempDir(), "root")
	if err := cloneDir(snapshot, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "auth.log"), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := Open(root, Options{HMACKey: key, Authorizer: testAuthority(t), Applier: ReplacePatch{}})
	if err == nil || !errors.Is(err, ErrLogCompromised) {
		if h != nil {
			_ = h.Close()
		}
		t.Fatalf("tampered commit not isolated: %v", err)
	}
	defer h.Close()
	obj, readErr := h.ReadObjectByID(context.Background(), testObjectID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(obj.Metadata) != string(testOldMeta) || string(obj.Data) != string(testOldData) {
		t.Fatalf("tampered commit became visible: %q/%q", obj.Metadata, obj.Data)
	}
}

func TestBadReceiptCannotBeReplayedForAnotherObject(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	idA := rawIdentity("receipt", "object-A", testOldMeta, testNewMeta)
	idB := rawIdentity("receipt", "object-B", testOldMeta, testNewMeta)
	rec := testRecord(idA, StagePrepare, testNewMeta)
	receipt, _ := ComputeAuthReceiptDigest(idB, idB.PolicyDigest, true)
	rec.AuthReceiptDigest = receipt
	writeRawJournal(t, filepath.Join(root, "auth.log"), testKey, []Record{rec})
	h, err := Open(root, Options{HMACKey: testKey, Authorizer: testAuthority(t), Applier: ReplacePatch{}})
	if err == nil || !errors.Is(err, ErrLogCompromised) {
		if h != nil {
			_ = h.Close()
		}
		t.Fatalf("replayed receipt accepted: %v", err)
	}
	defer h.Close()
}

func TestCheckpointTrustedPrefix(t *testing.T) {
	root := t.TempDir()
	seedObject(t, root)
	auth := testAuthority(t)
	h := newTestHost(t, root, auth, Options{})
	defer h.Close()
	if _, err := h.Execute(context.Background(), writeRequest("tx-checkpoint-1", testNewMeta, testNewData)); err != nil {
		t.Fatal(err)
	}
	cp, err := h.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = h.Close()

	logBytes, err := os.ReadFile(filepath.Join(root, "auth.log"))
	if err != nil {
		t.Fatal(err)
	}
	frames := frameBoundaries(logBytes, testKey)
	if len(frames) < 3 {
		t.Fatal("expected three frames")
	}

	// Keeping the checkpoint record is enough to trust already-applied state
	// even if all earlier log records are gone.
	root2 := filepath.Join(t.TempDir(), "root")
	if err := cloneDir(root, root2); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(root2, "auth.log"), int64(frames[2])); err != nil {
		t.Fatal(err)
	}
	h2, err := Open(root2, Options{HMACKey: testKey, Authorizer: auth, Applier: ReplacePatch{}})
	if err != nil && !errors.Is(err, ErrLogCompromised) {
		t.Fatalf("checkpoint reopen: %v", err)
	}
	obj, err := h2.ReadObjectByID(context.Background(), testObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Metadata) != string(testNewMeta) {
		t.Fatal("checkpointed committed state missing")
	}
	_ = h2.Close()

	// Removing the checkpoint record entirely means the trusted checkpoint
	// no longer connects to the log.  Fail closed.
	root3 := filepath.Join(t.TempDir(), "root")
	if err := cloneDir(root, root3); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(root3, "auth.log"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root3, Options{HMACKey: testKey, Authorizer: auth, Applier: ReplacePatch{}}); !errors.Is(err, ErrCheckpointMissing) {
		t.Fatalf("missing checkpoint err=%v (cp=%+v)", err, cp)
	}
}

func rawIdentity(txID, objectID string, oldMeta, newMeta []byte) TxIdentity {
	return TxIdentity{
		TxID:                 txID,
		PrincipalID:          "alice",
		CredentialGeneration: 1,
		PolicyDigest:         testPolicy,
		ObjectID:             objectID,
		ObjectGeneration:     0,
		Operation:            OpWrite,
		ParamsDigest:         DigestBytes([]byte("params-" + txID)),
		PatchDigest:          DigestBytes(newMeta),
		OldMetadataDigest:    DigestBytes(oldMeta),
		NewMetadataDigest:    DigestBytes(newMeta),
		DataDigest:           DigestBytes(testNewData),
	}
}

func testRecord(id TxIdentity, stage Stage, patch []byte) Record {
	receipt, err := ComputeAuthReceiptDigest(id, id.PolicyDigest, true)
	if err != nil {
		panic(err)
	}
	return Record{
		Identity:          id,
		Stage:             stage,
		AuthReceiptDigest: receipt,
		Patch:             patch,
	}
}

type rawEncodedFrame struct {
	bytes  []byte
	digest Digest
}

func rawFrame(rec Record, seq uint64, prev Digest, key []byte) rawEncodedFrame {
	rec.Seq = seq
	rec.Prev = prev
	b, d, err := encodeFrame(rec, key)
	if err != nil {
		panic(err)
	}
	return rawEncodedFrame{bytes: b, digest: d}
}

func writeRawJournal(t *testing.T, path string, key []byte, records []Record) {
	t.Helper()
	var prev Digest
	var data []byte
	for i, rec := range records {
		frame := rawFrame(rec, uint64(i+1), prev, key)
		data = append(data, frame.bytes...)
		prev = frame.digest
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
