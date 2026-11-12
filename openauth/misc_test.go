package openauth

import (
	"context"
	"errors"
	"testing"
)

func TestAllSquashWithExemptions(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	mapper := NewRootSquashMapper(SquashConfig{
		AllSquash: true,
		UIDExempt: []uint32{1500},
		GIDExempt: []uint32{2500},
	})
	authz := New(fs, mapper)

	// An exempt uid keeps its identity and can own the created file.
	res, err := authz.Open(ctx, OpenRequest{
		Cred:   AuthSysCreds{UID: 1500, GID: 1500},
		Parent: fs.rootNode(), Name: "exempt",
		Create: true, How: CreateUnchecked,
		ShareAccess: ShareAccessRead, ClientID: "c", OpenOwner: "o",
	})
	if err != nil {
		t.Fatalf("exempt create failed: %v", err)
	}
	if got := fs.get("exempt").uid; got != 1500 {
		t.Fatalf("expected exempt uid 1500, got %d", got)
	}
	if res.State.Identity.UID != 1500 {
		t.Fatalf("expected unmapped identity, got %+v", res.State.Identity)
	}

	// A non-exempt caller is squashed to nobody.
	id := mapper.Map(AuthSysCreds{UID: 3000, GID: 3000})
	if id.UID != 65534 || id.Privileged {
		t.Fatalf("expected squashed non-privileged identity, got %+v", id)
	}

	// GID exemption also prevents squashing under all_squash.
	idGid := mapper.Map(AuthSysCreds{UID: 3000, GID: 2500})
	if idGid.UID != 3000 {
		t.Fatalf("expected gid-exempt passthrough, got %+v", idGid)
	}
}

func TestInvalidOpenFlags(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	authz := New(fs, nil)

	_, err := authz.Open(ctx, OpenRequest{
		Parent: fs.rootNode(), Name: "x",
		ShareAccess: ShareAccess(99), ClientID: "c", OpenOwner: "o",
	})
	wantStatus(t, err, NFS4ERR_INVAL)

	_, err = authz.Open(ctx, OpenRequest{
		Parent: fs.rootNode(), Name: "x",
		ShareAccess: ShareAccessRead, ShareDeny: ShareDeny(1234),
		ClientID: "c", OpenOwner: "o",
	})
	wantStatus(t, err, NFS4ERR_INVAL)

	_, err = authz.Open(ctx, OpenRequest{
		Parent: fs.rootNode(), Name: "x",
		Create: true, How: CreateHow(7),
		ShareAccess: ShareAccessRead, ClientID: "c", OpenOwner: "o",
	})
	wantStatus(t, err, NFS4ERR_NOTSUPP)

	if err := authz.Close(424242); !errors.Is(err, NewNFSError(NFS4ERR_BAD_STATEID, "")) {
		t.Fatalf("expected BAD_STATEID on unknown close, got %v", err)
	}
}

func TestErrorHelpers(t *testing.T) {
	if StatusOf(nil) != NFS4_OK {
		t.Fatalf("nil error must map to NFS4_OK")
	}
	if StatusOf(errors.New("boom")) != NFS4ERR_SERVERFAULT {
		t.Fatalf("generic error must map to SERVERFAULT")
	}
	err := NewNFSError(NFS4ERR_EXIST, "already there")
	if StatusOf(err) != NFS4ERR_EXIST {
		t.Fatalf("unexpected status")
	}
	if !errors.Is(err, NewNFSError(NFS4ERR_EXIST, "")) {
		t.Fatalf("errors.Is should match by status code")
	}
	if errors.Is(err, NewNFSError(NFS4ERR_PERM, "")) {
		t.Fatalf("different status codes must not match")
	}
	if err.Error() == "" {
		t.Fatalf("expected a non-empty error message")
	}
}

func TestUnixAccessModes(t *testing.T) {
	// Owner rwx, group r-x, other r--.
	node := &memNode{uid: 10, gid: 20, mode: 0o754}

	owner := Identity{UID: 10, GID: 20}
	if got := UnixAccess(owner, node); got != AccessRead|AccessWrite|AccessExecute {
		t.Fatalf("owner rights = %v", got)
	}

	member := Identity{UID: 30, GID: 20}
	if got := UnixAccess(member, node); got != AccessRead|AccessExecute {
		t.Fatalf("group rights = %v", got)
	}

	other := Identity{UID: 30, GID: 30}
	if got := UnixAccess(other, node); got != AccessRead {
		t.Fatalf("other rights = %v", got)
	}

	root := Identity{UID: 0, Privileged: true}
	if got := UnixAccess(root, node); got&(AccessRead|AccessWrite) != AccessRead|AccessWrite {
		t.Fatalf("root must always read/write, got %v", got)
	}

	noExec := &memNode{uid: 10, gid: 20, mode: 0o600}
	if got := UnixAccess(root, noExec); got&AccessExecute != 0 {
		t.Fatalf("root execute requires an execute bit, got %v", got)
	}
}
