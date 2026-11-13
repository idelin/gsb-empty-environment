package netsec

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func testHost(t *testing.T, path string, mutate ...func(*Options)) *Host {
	t.Helper()
	opts := Options{
		LogPath:            path,
		AuthKey:            testKey,
		DisableAutoReclaim: true,
	}
	for _, fn := range mutate {
		fn(&opts)
	}
	h, err := NewHost(opts)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return h
}

func mustDir(t *testing.T, h *Host) DirectoryKey {
	t.Helper()
	dir, err := h.CreateDirectory(PermAll)
	if err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	return dir
}

func mustInode(t *testing.T, h *Host, data string) InodeKey {
	t.Helper()
	inode, err := h.CreateInode(CreateInodeOptions{Data: []byte(data)})
	if err != nil {
		t.Fatalf("CreateInode: %v", err)
	}
	return inode
}

func mustLink(t *testing.T, h *Host, inode InodeKey, dir DirectoryKey, name string) DirectoryEntry {
	t.Helper()
	if err := h.Link(inode, dir, name); err != nil {
		t.Fatalf("Link(%s): %v", name, err)
	}
	entry, err := h.Lookup(dir, name)
	if err != nil {
		t.Fatalf("Lookup(%s): %v", name, err)
	}
	return entry
}

func mustInodeState(t *testing.T, h *Host, key InodeKey) Inode {
	t.Helper()
	inode, ok := h.GetInode(key)
	if !ok {
		t.Fatalf("inode %s is missing", key)
	}
	return inode
}

func matchingReferences(refs []DirectoryEntry, key InodeKey) int {
	n := 0
	for _, entry := range refs {
		if entry.InodeKey() == key {
			n++
		}
	}
	return n
}

func TestTwoHardLinksAndReclaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.log")
	var h *Host
	var callbackScan ReferenceProof
	var order []string
	h = testHost(t, path, func(opts *Options) {
		opts.ClearData = func(key InodeKey) error {
			order = append(order, "clear:"+key.String())
			return nil
		}
		opts.OnReclaim = func(event ReclaimEvent) error {
			order = append(order, "callback:"+event.Inode.Key().String())
			refs := h.ScanReferences()
			callbackScan = ReferenceProof{
				DirectoriesScanned: len(h.ListDirectories()),
				EntriesScanned:     len(refs),
				Matching:           nil,
			}
			for _, entry := range refs {
				if entry.InodeKey() == event.Inode.Key() {
					callbackScan.Matching = append(callbackScan.Matching, entry)
				}
			}
			if len(event.Proof.Matching) != 0 {
				t.Fatalf("callback proof was not empty: %+v", event.Proof)
			}
			return nil
		}
	})
	dir := mustDir(t, h)
	inode := mustInode(t, h, "payload")
	a := mustLink(t, h, inode, dir, "a")
	b := mustLink(t, h, inode, dir, "b")

	got := mustInodeState(t, h, inode)
	if got.LinkCount != 2 || got.Status != InodeLive {
		t.Fatalf("after two links: %+v", got)
	}
	if refs := h.ScanReferences(); matchingReferences(refs, inode) != 2 {
		t.Fatalf("scan found %d references, want 2", matchingReferences(refs, inode))
	}

	lease, err := h.Open(a)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := h.Unlink(a); err != nil {
		t.Fatalf("Unlink a: %v", err)
	}
	got = mustInodeState(t, h, inode)
	if got.LinkCount != 1 || got.Status != InodeLive {
		t.Fatalf("after first unlink: %+v", got)
	}
	if err := h.Unlink(b); err != nil {
		t.Fatalf("Unlink b: %v", err)
	}
	got = mustInodeState(t, h, inode)
	if got.LinkCount != 0 || got.OpenCount != 1 || got.Status != InodeOrphan {
		t.Fatalf("after last unlink with an open lease: %+v", got)
	}
	if data, err := h.ReadInode(inode); err != nil || string(data) != "payload" {
		t.Fatalf("orphan data was not retained: %q, %v", data, err)
	}
	if refs := h.ScanReferences(); len(refs) != 0 {
		t.Fatalf("scan after unlink = %+v, want empty", refs)
	}

	if err := h.Close(lease); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	got = mustInodeState(t, h, inode)
	if got.OpenCount != 0 || got.Status != InodeDeleting {
		t.Fatalf("after last close: %+v", got)
	}
	reclaimed, err := h.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0] != inode {
		t.Fatalf("reclaimed = %+v", reclaimed)
	}
	if _, ok := h.GetInode(inode); ok {
		t.Fatal("inode survived reclaim")
	}
	if _, err := h.ReadInode(inode); !errors.Is(err, ErrInodeGenerationMismatch) {
		t.Fatalf("ReadInode after reclaim = %v", err)
	}
	if len(callbackScan.Matching) != 0 {
		t.Fatalf("callback saw references: %+v", callbackScan.Matching)
	}
	if len(order) != 2 || order[0] != "clear:"+inode.String() || order[1] != "callback:"+inode.String() {
		t.Fatalf("callback order = %+v", order)
	}

	reused, err := h.CreateInode(CreateInodeOptions{ID: inode.ID, Data: []byte("new")})
	if err != nil {
		t.Fatalf("reuse inode ID: %v", err)
	}
	if reused.Generation <= inode.Generation {
		t.Fatalf("reused generation %d did not advance past %d", reused.Generation, inode.Generation)
	}
	if err := h.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	reopened := testHost(t, path)
	defer reopened.Shutdown()
	got = mustInodeState(t, reopened, reused)
	if got.LinkCount != 0 || got.Status != InodeOrphan {
		t.Fatalf("recovered reused inode: %+v", got)
	}
	if data, err := reopened.ReadInode(reused); err != nil || string(data) != "new" {
		t.Fatalf("recovered reused data = %q, %v", data, err)
	}
}

func TestExchangeAndTornExchange(t *testing.T) {
	t.Run("committed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "exchange.log")
		h := testHost(t, path)
		dir := mustDir(t, h)
		aInode := mustInode(t, h, "a")
		bInode := mustInode(t, h, "b")
		a := mustLink(t, h, aInode, dir, "a")
		b := mustLink(t, h, bInode, dir, "b")

		if err := h.Exchange(a, b); err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		gotA, _ := h.Lookup(dir, "a")
		gotB, _ := h.Lookup(dir, "b")
		if gotA.InodeKey() != bInode || gotB.InodeKey() != aInode {
			t.Fatalf("exchange result a=%+v b=%+v", gotA, gotB)
		}
		if mustInodeState(t, h, aInode).LinkCount != 1 || mustInodeState(t, h, bInode).LinkCount != 1 {
			t.Fatal("exchange changed link counts")
		}
		if err := h.Shutdown(); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		h = testHost(t, path)
		defer h.Shutdown()
		gotA, _ = h.Lookup(dir, "a")
		gotB, _ = h.Lookup(dir, "b")
		if gotA.InodeKey() != bInode || gotB.InodeKey() != aInode {
			t.Fatalf("recovered exchange a=%+v b=%+v", gotA, gotB)
		}
	})

	t.Run("uncommitted", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "exchange-torn.log")
		h := testHost(t, path)
		dir := mustDir(t, h)
		aInode := mustInode(t, h, "a")
		bInode := mustInode(t, h, "b")
		a := mustLink(t, h, aInode, dir, "a")
		b := mustLink(t, h, bInode, dir, "b")
		if err := h.Exchange(a, b); err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if err := h.Shutdown(); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		truncated := withoutLastRecord(data)
		if err := os.WriteFile(path, truncated, 0o600); err != nil {
			t.Fatal(err)
		}
		h = testHost(t, path)
		defer h.Shutdown()
		gotA, _ := h.Lookup(dir, "a")
		gotB, _ := h.Lookup(dir, "b")
		if gotA.InodeKey() != aInode || gotB.InodeKey() != bInode {
			t.Fatalf("rolled-back exchange a=%+v b=%+v", gotA, gotB)
		}
		if len(h.RecoveryReport().RolledBack) != 1 {
			t.Fatalf("rollback report = %+v", h.RecoveryReport())
		}
	})
}

func TestUnlinkCloseInterleaving(t *testing.T) {
	h := testHost(t, "")
	dir := mustDir(t, h)
	inode := mustInode(t, h, "interleaved")
	entry := mustLink(t, h, inode, dir, "file")
	first, err := h.Open(entry)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Open(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Unlink(entry); err != nil {
		t.Fatal(err)
	}
	if got := mustInodeState(t, h, inode); got.Status != InodeOrphan || got.OpenCount != 2 {
		t.Fatalf("after unlink: %+v", got)
	}
	if err := h.Close(first); err != nil {
		t.Fatal(err)
	}
	if got := mustInodeState(t, h, inode); got.Status != InodeOrphan || got.OpenCount != 1 {
		t.Fatalf("after first close: %+v", got)
	}
	if err := h.Close(second); err != nil {
		t.Fatal(err)
	}
	if got := mustInodeState(t, h, inode); got.Status != InodeDeleting || got.OpenCount != 0 {
		t.Fatalf("after last close: %+v", got)
	}
	if data, err := h.ReadInode(inode); err != nil || string(data) != "interleaved" {
		t.Fatalf("data disappeared before Sweep: %q, %v", data, err)
	}
	if _, err := h.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.GetInode(inode); ok {
		t.Fatal("sweep did not release inode")
	}
}

func TestTornLogRollback(t *testing.T) {
	t.Run("partial_tail", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "partial.log")
		h := testHost(t, path)
		dir := mustDir(t, h)
		inode := mustInode(t, h, "payload")
		_ = mustLink(t, h, inode, dir, "file")
		if err := h.Shutdown(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 20 {
			t.Fatal("log unexpectedly short")
		}
		if err := os.WriteFile(path, data[:len(data)-17], 0o600); err != nil {
			t.Fatal(err)
		}

		h = testHost(t, path)
		defer h.Shutdown()
		report := h.RecoveryReport()
		if report.TruncatedBytes <= 0 {
			t.Fatalf("torn tail was not reported: %+v", report)
		}
		if len(report.RolledBack) != 1 {
			t.Fatalf("rolled back = %+v", report.RolledBack)
		}
		if _, err := h.Lookup(dir, "file"); !errors.Is(err, ErrEntryNotFound) {
			t.Fatalf("torn link entry exists: %v", err)
		}
		if got := mustInodeState(t, h, inode); got.LinkCount != 0 || got.Status != InodeOrphan {
			t.Fatalf("torn link changed inode: %+v", got)
		}
	})

	t.Run("missing_commit_is_fully_rolled_back", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing-commit.log")
		h := testHost(t, path)
		dir := mustDir(t, h)
		inode := mustInode(t, h, "payload")
		entry := mustLink(t, h, inode, dir, "file")
		if err := h.Unlink(entry); err != nil {
			t.Fatal(err)
		}
		if err := h.Shutdown(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		truncated := withoutLastRecord(data)
		if err := os.WriteFile(path, truncated, 0o600); err != nil {
			t.Fatal(err)
		}

		h = testHost(t, path)
		defer h.Shutdown()
		if _, err := h.Lookup(dir, "file"); err != nil {
			t.Fatalf("unlink was partially applied: %v", err)
		}
		if got := mustInodeState(t, h, inode); got.LinkCount != 1 || got.Status != InodeLive {
			t.Fatalf("unlink count was partially applied: %+v", got)
		}
		if len(h.RecoveryReport().RolledBack) != 1 {
			t.Fatalf("rollback report = %+v", h.RecoveryReport())
		}
	})
}

func withoutLastRecord(data []byte) []byte {
	data = bytes.TrimSuffix(data, []byte("\n"))
	idx := bytes.LastIndexByte(data, '\n')
	if idx < 0 {
		return nil
	}
	return data[:idx+1]
}

func TestDuplicateEntryIsQuarantinedNotCorrected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate.log")
	h := testHost(t, path)
	dir := mustDir(t, h)
	inode := mustInode(t, h, "keep")
	entry := mustLink(t, h, inode, dir, "name")

	duplicate := entry
	_, err := h.commit("corrupt_duplicate", []mutation{
		{Op: opPutEntry, Entry: &duplicate},
		{
			Op:              opSetLinkCount,
			InodeID:         inode.ID,
			InodeGeneration: inode.Generation,
			LinkCount:       2,
		},
	})
	if err != nil {
		t.Fatalf("inject duplicate: %v", err)
	}
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}

	h = testHost(t, path)
	defer h.Shutdown()
	got := mustInodeState(t, h, inode)
	if got.LinkCount != 2 {
		t.Fatalf("recovery corrected linkCount to %d instead of preserving 2", got.LinkCount)
	}
	if len(h.QuarantinedInodes()) == 0 {
		t.Fatal("mismatched inode was not quarantined")
	}
	if len(h.QuarantinedEntries()) == 0 {
		t.Fatal("duplicate entry was not quarantined")
	}
	reclaimed, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("quarantined inode was reclaimed: %+v", reclaimed)
	}
	if data, err := h.ReadInode(inode); err != nil || string(data) != "keep" {
		t.Fatalf("quarantined data changed: %q, %v", data, err)
	}
}

func TestInodeNumberReuseIgnoresStaleGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reuse.log")
	h := testHost(t, path)
	dir := mustDir(t, h)
	old, err := h.CreateInode(CreateInodeOptions{ID: 42, Data: []byte("old")})
	if err != nil {
		t.Fatal(err)
	}
	oldEntry := mustLink(t, h, old, dir, "old")
	if err := h.Unlink(oldEntry); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.GetInode(old); ok {
		t.Fatal("old generation was not released")
	}

	reused, err := h.CreateInode(CreateInodeOptions{ID: 42, Data: []byte("new")})
	if err != nil {
		t.Fatal(err)
	}
	if reused.Generation <= old.Generation {
		t.Fatalf("generation did not advance: old=%s new=%s", old, reused)
	}
	_ = mustLink(t, h, reused, dir, "new")
	stale := DirectoryEntry{
		DirectoryID:         dir.ID,
		DirectoryGeneration: dir.Generation,
		Name:                "stale",
		InodeID:             old.ID,
		InodeGeneration:     old.Generation,
	}
	if _, err := h.commit("inject_stale_reference", []mutation{{Op: opPutEntry, Entry: &stale}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}

	h = testHost(t, path)
	defer h.Shutdown()
	got := mustInodeState(t, h, reused)
	if got.LinkCount != 1 || got.Status != InodeLive {
		t.Fatalf("new generation was affected by stale entry: %+v", got)
	}
	if len(h.QuarantinedInodes()) != 0 {
		t.Fatalf("new generation was quarantined: %+v", h.QuarantinedInodes())
	}
	records := h.QuarantinedEntries()
	if len(records) != 1 || records[0].Reason != QuarantineStaleGeneration {
		t.Fatalf("stale entry quarantine = %+v", records)
	}
	if refs := h.ScanReferences(); matchingReferences(refs, reused) != 1 {
		t.Fatalf("stale generation was counted as a reference to the new inode: %+v", refs)
	}
}

func TestPolicyRevocation(t *testing.T) {
	t.Run("drain", func(t *testing.T) {
		h := testHost(t, "")
		dir := mustDir(t, h)
		inode := mustInode(t, h, "policy")
		entry := mustLink(t, h, inode, dir, "file")
		lease, err := h.Open(entry)
		if err != nil {
			t.Fatal(err)
		}
		gen, err := h.RevokePolicy(inode, RevokeOptions{Mode: RevokeDrain})
		if err != nil {
			t.Fatal(err)
		}
		if gen <= lease.PolicyGeneration {
			t.Fatalf("policy generation did not advance: %d -> %d", lease.PolicyGeneration, gen)
		}
		if _, err := h.Open(entry); !errors.Is(err, ErrPolicyDenied) {
			t.Fatalf("new Open after revocation = %v", err)
		}
		if err := h.Link(inode, dir, "second"); !errors.Is(err, ErrPolicyDenied) {
			t.Fatalf("new Link after revocation = %v", err)
		}
		if got := mustInodeState(t, h, inode); got.OpenCount != 1 {
			t.Fatalf("drain unexpectedly closed the existing lease: %+v", got)
		}
		if err := h.Close(lease); err != nil {
			t.Fatalf("drained lease close: %v", err)
		}
	})

	t.Run("grace", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0)
		h := testHost(t, "", func(opts *Options) {
			opts.Now = func() time.Time { return now }
		})
		dir := mustDir(t, h)
		inode := mustInode(t, h, "grace")
		entry := mustLink(t, h, inode, dir, "file")
		lease, err := h.Open(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.RevokePolicy(inode, RevokeOptions{Mode: RevokeGrace, GracePeriod: time.Minute}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Open(entry); !errors.Is(err, ErrPolicyDenied) {
			t.Fatalf("new Open during grace = %v", err)
		}
		now = now.Add(2 * time.Minute)
		ids, err := h.ExpireLeases(now)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != lease.ID {
			t.Fatalf("expired leases = %+v", ids)
		}
		if got := mustInodeState(t, h, inode); got.OpenCount != 0 || got.LinkCount != 1 {
			t.Fatalf("after grace expiry: %+v", got)
		}
	})
}

func TestAuthenticatedLogRejectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.log")
	h := testHost(t, path)
	dir := mustDir(t, h)
	inode := mustInode(t, h, "auth")
	_ = mustLink(t, h, inode, dir, "file")
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := bytes.Index(data, []byte("create_directory"))
	if idx < 0 {
		t.Fatal("could not find record to tamper")
	}
	data[idx+len("create_directory")-1] = 'x'
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHost(Options{LogPath: path, AuthKey: testKey}); !errors.Is(err, ErrLogAuthentication) {
		t.Fatalf("tampered log error = %v, want ErrLogAuthentication", err)
	}
}

func TestRecoveredHostAppendsWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append.log")
	h := testHost(t, path)
	dir := mustDir(t, h)
	first := mustInode(t, h, "first")
	_ = mustLink(t, h, first, dir, "first")
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}

	h = testHost(t, path)
	second := mustInode(t, h, "second")
	_ = mustLink(t, h, second, dir, "second")
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}

	h = testHost(t, path)
	defer h.Shutdown()
	for _, name := range []string{"first", "second"} {
		if _, err := h.Lookup(dir, name); err != nil {
			t.Fatalf("entry %q lost after append: %v", name, err)
		}
	}
	if got := mustInodeState(t, h, first); got.LinkCount != 1 {
		t.Fatalf("first inode = %+v", got)
	}
	if got := mustInodeState(t, h, second); got.LinkCount != 1 {
		t.Fatalf("second inode = %+v", got)
	}
}

func TestExchangeAcrossDirectories(t *testing.T) {
	h := testHost(t, "")
	dirA := mustDir(t, h)
	dirB := mustDir(t, h)
	inodeA := mustInode(t, h, "a")
	inodeB := mustInode(t, h, "b")
	a := mustLink(t, h, inodeA, dirA, "name")
	b := mustLink(t, h, inodeB, dirB, "name")
	if err := h.Exchange(a, b); err != nil {
		t.Fatal(err)
	}
	gotA, err := h.Lookup(dirA, "name")
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := h.Lookup(dirB, "name")
	if err != nil {
		t.Fatal(err)
	}
	if gotA.InodeKey() != inodeB || gotB.InodeKey() != inodeA {
		t.Fatalf("cross-directory exchange a=%+v b=%+v", gotA, gotB)
	}
	if got := mustInodeState(t, h, inodeA); got.LinkCount != 1 || got.Status != InodeLive {
		t.Fatalf("inode A after exchange: %+v", got)
	}
	if got := mustInodeState(t, h, inodeB); got.LinkCount != 1 || got.Status != InodeLive {
		t.Fatalf("inode B after exchange: %+v", got)
	}
}

func TestLinkPolicyAndDirectoryPermission(t *testing.T) {
	h := testHost(t, "")
	lockedDir, err := h.CreateDirectory(PermLookup)
	if err != nil {
		t.Fatal(err)
	}
	inodeNoLink, err := h.CreateInode(CreateInodeOptions{
		Data:   []byte("x"),
		Policy: Policy{Generation: 1, Flags: PolicyAllowOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Link(inodeNoLink, lockedDir, "file"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("source policy did not deny Link: %v", err)
	}

	inode := mustInode(t, h, "y")
	if err := h.Link(inode, lockedDir, "file"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("target directory did not deny Link: %v", err)
	}
}

func TestAutomaticReclaimAfterUnlinkAndRecovery(t *testing.T) {
	t.Run("unlink", func(t *testing.T) {
		h, err := NewHost(Options{AuthKey: testKey})
		if err != nil {
			t.Fatal(err)
		}
		dir := mustDir(t, h)
		inode := mustInode(t, h, "auto")
		entry := mustLink(t, h, inode, dir, "file")
		if err := h.Unlink(entry); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.GetInode(inode); ok {
			t.Fatal("auto reclaim left the inode behind")
		}
		if refs := h.ScanReferences(); len(refs) != 0 {
			t.Fatalf("auto reclaim left references: %+v", refs)
		}
	})

	t.Run("recovery", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auto-recovery.log")
		h := testHost(t, path)
		dir := mustDir(t, h)
		inode := mustInode(t, h, "auto")
		entry := mustLink(t, h, inode, dir, "file")
		if err := h.Unlink(entry); err != nil {
			t.Fatal(err)
		}
		if got := mustInodeState(t, h, inode); got.Status != InodeOrphan {
			t.Fatalf("explicit sweep test expected Orphan, got %+v", got)
		}
		if err := h.Shutdown(); err != nil {
			t.Fatal(err)
		}
		h, err := NewHost(Options{LogPath: path, AuthKey: testKey})
		if err != nil {
			t.Fatal(err)
		}
		defer h.Shutdown()
		if _, ok := h.GetInode(inode); ok {
			t.Fatal("recovery did not reclaim the unreferenced orphan")
		}
		if refs := h.ScanReferences(); len(refs) != 0 {
			t.Fatalf("recovery left references: %+v", refs)
		}
	})
}
