package openauth

import (
	"context"
	"errors"
	"testing"
)

func uptr(v uint32) *uint32 { return &v }
func gptr(v uint32) *uint32 { return &v }
func mptr(v uint32) *uint32 { return &v }
func sptr(v uint64) *uint64 { return &v }

func cred(uid uint32) AuthSysCreds {
	return AuthSysCreds{UID: uid, GID: uid, Groups: []uint32{uid}}
}

func wantStatus(t *testing.T, err error, want NFS4Status) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error status %d, got success", want)
	}
	var nerr *NFSError
	if !errors.As(err, &nerr) {
		t.Fatalf("expected *NFSError, got %T: %v", err, err)
	}
	if nerr.Status != want {
		t.Fatalf("expected status %d, got %d (%v)", want, nerr.Status, err)
	}
}

func TestCreateOwnerMustEqualMappedCaller(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	// uid 1000 tries to create a file owned by uid 0: refused, no file left.
	_, err := authz.Open(ctx, OpenRequest{
		Cred:        cred(1000),
		Parent:      fs.rootNode(),
		Name:        "evil",
		Create:      true,
		How:         CreateUnchecked,
		Attrs:       CreateAttrs{Owner: uptr(0)},
		ShareAccess: ShareAccessRead,
		ShareDeny:   ShareDenyNone,
		ClientID:    "c1",
		OpenOwner:   "o1",
	})
	wantStatus(t, err, NFS4ERR_PERM)
	if fs.exists("evil") {
		t.Fatalf("rejected owner-tamper create left a file behind")
	}

	// The same caller may create a file owned by its own (mapped) uid.
	res, err := authz.Open(ctx, OpenRequest{
		Cred:        cred(1000),
		Parent:      fs.rootNode(),
		Name:        "ok",
		Create:      true,
		How:         CreateUnchecked,
		Attrs:       CreateAttrs{Owner: uptr(1000), Mode: mptr(0o640), Size: sptr(42)},
		ShareAccess: ShareAccessRead,
		ShareDeny:   ShareDenyNone,
		ClientID:    "c1",
		OpenOwner:   "o2",
	})
	if err != nil {
		t.Fatalf("legitimate create failed: %v", err)
	}
	node := fs.get("ok")
	if node.uid != 1000 || node.mode != 0o640 || node.size != 42 {
		t.Fatalf("unexpected initial attrs: uid=%d mode=%o size=%d", node.uid, node.mode, node.size)
	}
	if !res.CreatedFile || res.State == nil {
		t.Fatalf("expected created file with granted state, got %+v", res)
	}
}

func TestCreateGroupConfinement(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	_, err := authz.Open(ctx, OpenRequest{
		Cred:        cred(1000),
		Parent:      fs.rootNode(),
		Name:        "g",
		Create:      true,
		How:         CreateUnchecked,
		Attrs:       CreateAttrs{Group: gptr(7777)},
		ShareAccess: ShareAccessRead,
		ClientID:    "c1",
		OpenOwner:   "o1",
	})
	wantStatus(t, err, NFS4ERR_PERM)
	if fs.exists("g") {
		t.Fatalf("rejected group-tamper create left a file behind")
	}

	// A supplementary group the caller belongs to is allowed.
	c := cred(1000)
	c.Groups = append(c.Groups, 2000)
	_, err = authz.Open(ctx, OpenRequest{
		Cred:        c,
		Parent:      fs.rootNode(),
		Name:        "g2",
		Create:      true,
		How:         CreateUnchecked,
		Attrs:       CreateAttrs{Group: gptr(2000)},
		ShareAccess: ShareAccessRead,
		ClientID:    "c1",
		OpenOwner:   "o2",
	})
	if err != nil {
		t.Fatalf("create with membership group failed: %v", err)
	}
	if fs.get("g2").gid != 2000 {
		t.Fatalf("expected gid 2000, got %d", fs.get("g2").gid)
	}
}

func TestRootSquashAndPrivilegedChown(t *testing.T) {
	ctx := context.Background()

	// Default root squash: uid 0 maps to nobody 65534 and loses chown.
	fs := newMemFS()
	squashed := New(fs, NewRootSquashMapper(SquashConfig{}))
	_, err := squashed.Open(ctx, OpenRequest{
		Cred:        cred(0),
		Parent:      fs.rootNode(),
		Name:        "a",
		Create:      true,
		How:         CreateUnchecked,
		Attrs:       CreateAttrs{Owner: uptr(0)},
		ShareAccess: ShareAccessRead,
		ClientID:    "c1",
		OpenOwner:   "o1",
	})
	wantStatus(t, err, NFS4ERR_PERM)

	res, err := squashed.Open(ctx, OpenRequest{
		Cred:        cred(0),
		Parent:      fs.rootNode(),
		Name:        "b",
		Create:      true,
		How:         CreateUnchecked,
		ShareAccess: ShareAccessRead,
		ClientID:    "c1",
		OpenOwner:   "o2",
	})
	if err != nil {
		t.Fatalf("squashed root create failed: %v", err)
	}
	if got := fs.get("b").uid; got != 65534 {
		t.Fatalf("expected squashed owner 65534, got %d", got)
	}
	if res.State.Identity.UID != 65534 || res.State.Identity.Privileged {
		t.Fatalf("expected non-privileged nobody identity, got %+v", res.State.Identity)
	}

	// With root squashing disabled, uid 0 stays privileged and may chown.
	fs2 := newMemFS()
	priv := New(fs2, NewRootSquashMapper(SquashConfig{NoRootSquash: true}))
	res2, err := priv.Open(ctx, OpenRequest{
		Cred:        cred(0),
		Parent:      fs2.rootNode(),
		Name:        "p",
		Create:      true,
		How:         CreateUnchecked,
		Attrs:       CreateAttrs{Owner: uptr(1000), Group: gptr(1000)},
		ShareAccess: ShareAccessRead,
		ClientID:    "c1",
		OpenOwner:   "o1",
	})
	if err != nil {
		t.Fatalf("privileged create with foreign owner failed: %v", err)
	}
	if got := fs2.get("p").uid; got != 1000 {
		t.Fatalf("expected privileged chown to uid 1000, got %d", got)
	}
	if !res2.State.Identity.Privileged {
		t.Fatalf("expected privileged identity")
	}
}

func TestGuardedCreateExistingReturnsExist(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	_, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "f",
		Create: true, How: CreateUnchecked,
		Attrs:       CreateAttrs{Mode: mptr(0o666)},
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	if err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "f",
		Create: true, How: CreateGuarded,
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o2",
	})
	wantStatus(t, err, NFS4ERR_EXIST)
}

func TestExclusiveVerifierRetrySemantics(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	verifierA := Verifier{1, 2, 3, 4, 5, 6, 7, 8}
	verifierB := Verifier{8, 7, 6, 5, 4, 3, 2, 1}

	first, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "excl",
		Create: true, How: CreateExclusive, Verifier: verifierA,
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	if err != nil {
		t.Fatalf("exclusive create failed: %v", err)
	}
	if !first.CreatedFile {
		t.Fatalf("first exclusive OPEN must create the file")
	}

	// Retry with the same verifier is idempotent success, no new file.
	retry, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "excl",
		Create: true, How: CreateExclusive, Verifier: verifierA,
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	if err != nil {
		t.Fatalf("matching verifier retry failed: %v", err)
	}
	if retry.CreatedFile || !retry.Retry {
		t.Fatalf("expected retry=true with no new file, got created=%v retry=%v",
			retry.CreatedFile, retry.Retry)
	}

	// A different verifier on the existing file is NFS4ERR_EXIST.
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "excl",
		Create: true, How: CreateExclusive, Verifier: verifierB,
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o2",
	})
	wantStatus(t, err, NFS4ERR_EXIST)

	// EXCLUSIVE4_1 rejects owner/mode/size attributes on the wire.
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "excl2",
		Create: true, How: CreateExclusive, Verifier: verifierA,
		Attrs:       CreateAttrs{Mode: mptr(0o644)},
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o3",
	})
	wantStatus(t, err, NFS4ERR_INVAL)
	if fs.exists("excl2") {
		t.Fatalf("invalid exclusive create left a file behind")
	}
}

func TestShareDenyConflict(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	// World readable/writable so the access gate does not interfere.
	first, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "shared",
		Create: true, How: CreateUnchecked, Attrs: CreateAttrs{Mode: mptr(0o666)},
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyNone,
		ClientID: "c1", OpenOwner: "reader",
	})
	if err != nil {
		t.Fatalf("seed open failed: %v", err)
	}

	// Another open-owner denying READ conflicts with held READ access.
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(2000), Parent: fs.rootNode(), Name: "shared",
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyRead,
		ClientID: "c2", OpenOwner: "blocker",
	})
	wantStatus(t, err, NFS4ERR_SHARE_DENIED)

	// Releasing the reservation allows the conflicting open to proceed.
	if err := authz.Close(first.State.ID); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	blocker, err := authz.Open(ctx, OpenRequest{
		Cred: cred(2000), Parent: fs.rootNode(), Name: "shared",
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyRead,
		ClientID: "c2", OpenOwner: "blocker",
	})
	if err != nil {
		t.Fatalf("open after close failed: %v", err)
	}

	// Held deny-READ now blocks a new reader (reverse direction).
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(3000), Parent: fs.rootNode(), Name: "shared",
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyNone,
		ClientID: "c3", OpenOwner: "late-reader",
	})
	wantStatus(t, err, NFS4ERR_SHARE_DENIED)

	// Reopening the same open-owner replaces rather than conflicts.
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(2000), Parent: fs.rootNode(), Name: "shared",
		ShareAccess: ShareAccessWrite, ShareDeny: ShareDenyWrite,
		ClientID: "c2", OpenOwner: "blocker",
	})
	if err != nil {
		t.Fatalf("same open-owner reopen should replace reservation: %v", err)
	}
	states := authz.ActiveStates()
	if len(states) != 1 {
		t.Fatalf("expected one remaining reservation, got %d", len(states))
	}
	if states[0].Deny != ShareDenyWrite {
		t.Fatalf("expected replaced deny-write reservation, got %d", states[0].Deny)
	}
	_ = blocker
}

func TestShareRequiresReadOrWritePermission(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	// root creates a 0600 file owned by uid 1000.
	rootAuth := New(fs, NewRootSquashMapper(SquashConfig{NoRootSquash: true}))
	_, err := rootAuth.Open(ctx, OpenRequest{
		Cred: cred(0), Parent: fs.rootNode(), Name: "secret",
		Create: true, How: CreateUnchecked,
		Attrs:       CreateAttrs{Owner: uptr(1000), Group: gptr(1000), Mode: mptr(0o600)},
		ShareAccess: ShareAccessRead, ClientID: "c0", OpenOwner: "o0",
	})
	if err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	// uid 2000 has neither read nor write on a 0600 file: share denied at
	// the access gate before any reservation is installed.
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(2000), Parent: fs.rootNode(), Name: "secret",
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyNone,
		ClientID: "c2", OpenOwner: "o1",
	})
	wantStatus(t, err, NFS4ERR_ACCES)

	// The owner uid 1000 holds read permission and can take share state.
	if _, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "secret",
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyNone,
		ClientID: "c1", OpenOwner: "owner",
	}); err != nil {
		t.Fatalf("owner open should succeed: %v", err)
	}

	// WRITE access requires write permission: a 0400 file cannot be opened
	// for write share access even by its owner.
	_, err = rootAuth.Open(ctx, OpenRequest{
		Cred: cred(0), Parent: fs.rootNode(), Name: "readonly",
		Create: true, How: CreateUnchecked,
		Attrs:       CreateAttrs{Owner: uptr(1000), Group: gptr(1000), Mode: mptr(0o400)},
		ShareAccess: ShareAccessRead, ClientID: "c0", OpenOwner: "ro",
	})
	if err != nil {
		t.Fatalf("seed readonly create failed: %v", err)
	}
	_, err = authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "readonly",
		ShareAccess: ShareAccessWrite, ShareDeny: ShareDenyNone,
		ClientID: "c1", OpenOwner: "w",
	})
	wantStatus(t, err, NFS4ERR_ACCES)
}

func TestFailedOpenRollsBackCreatedFile(t *testing.T) {
	ctx := context.Background()

	// Inject a verifier-store failure after a successful exclusive create:
	// the freshly created file must be removed again.
	fs := newMemFS()
	flaky := &flakyBackend{Backend: fs, failVerifier: true}
	authz := New(flaky, nil)

	_, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "orphan",
		Create: true, How: CreateExclusive, Verifier: Verifier{9, 9},
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	wantStatus(t, err, NFS4ERR_SERVERFAULT)
	if fs.exists("orphan") {
		t.Fatalf("failed OPEN left the created file on disk")
	}
	if len(authz.ActiveStates()) != 0 {
		t.Fatalf("failed OPEN must not leave share state behind")
	}

	// A share-conflict hit on a newly created UNCHECKED file also rolls back.
	fs2 := newMemFS()
	holderAuth := New(fs2, nil)
	holderRes, err := holderAuth.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs2.rootNode(), Name: "f",
		Create: true, How: CreateUnchecked, Attrs: CreateAttrs{Mode: mptr(0o666)},
		ShareAccess: ShareAccessRead, ShareDeny: ShareDenyNone,
		ClientID: "c1", OpenOwner: "holder",
	})
	if err != nil {
		t.Fatalf("seed open failed: %v", err)
	}

	// The aliasing backend reports the newly created name as the holder's
	// node, so the deny-READ request collides with the held reservation
	// after the create has already materialized on disk.
	alias := &aliasCreateBackend{memFS: fs2, AliasNode: holderRes.Node}
	authz2 := New(alias, nil)
	// Migrate the holder's reservation into the authorizer under test.
	authz2.mu.Lock()
	authz2.states[holderRes.State.ID] = holderRes.State
	authz2.seq.Store(holderRes.State.ID)
	authz2.mu.Unlock()
	_, err = authz2.Open(ctx, OpenRequest{
		Cred: cred(2000), Parent: fs2.rootNode(), Name: "newconflict",
		Create: true, How: CreateUnchecked, Attrs: CreateAttrs{Mode: mptr(0o666)},
		ShareAccess: ShareAccessNone, ShareDeny: ShareDenyRead,
		ClientID: "c2", OpenOwner: "new",
	})
	wantStatus(t, err, NFS4ERR_SHARE_DENIED)
	if fs2.exists("newconflict") {
		t.Fatalf("share-conflicted create left a file behind")
	}

	// When even the rollback remove fails, the caller gets SERVERFAULT.
	fs3 := newMemFS()
	badRollback := &flakyBackend{Backend: fs3, failVerifier: true, failRemove: true}
	authz3 := New(badRollback, nil)
	_, err = authz3.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs3.rootNode(), Name: "stuck",
		Create: true, How: CreateExclusive, Verifier: Verifier{1},
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	wantStatus(t, err, NFS4ERR_SERVERFAULT)
}

func TestOpenExistingAndNoEnt(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	// Opening a missing file without CREATE is NFS4ERR_NOENT.
	_, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "missing",
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	wantStatus(t, err, NFS4ERR_NOENT)

	// UNCHECKED create then a plain open of the same name succeeds without
	// creating a second object.
	first, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "f",
		Create: true, How: CreateUnchecked, Attrs: CreateAttrs{Mode: mptr(0o666)},
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o1",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	second, err := authz.Open(ctx, OpenRequest{
		Cred: cred(1000), Parent: fs.rootNode(), Name: "f",
		ShareAccess: ShareAccessRead, ClientID: "c1", OpenOwner: "o2",
	})
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if second.CreatedFile || first.Node.ID() != second.Node.ID() {
		t.Fatalf("expected the same existing node, got %+v vs %+v", first.Node, second.Node)
	}
}
