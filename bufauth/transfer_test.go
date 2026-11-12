package bufauth

import (
	"errors"
	"sync"
	"syscall"
	"testing"
)

// pipeFD returns the write end of a fresh pipe as a stand-in for a graphics
// buffer file descriptor. The returned cleanup closes both ends.
func pipeFD(t *testing.T) (int, func()) {
	t.Helper()
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	cleanup := func() {
		_ = syscall.Close(fds[0])
		_ = syscall.Close(fds[1])
	}
	return fds[1], cleanup
}

func fdValid(fd int) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0)
	return errno == 0
}

func TestTransferClaimHappyPath(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	fd, cleanup := pipeFD(t)
	defer cleanup()

	transfer, err := auth.BeginTransfer(root, "buf0", fd)
	if err != nil {
		t.Fatalf("BeginTransfer: %v", err)
	}
	if transfer.State() != StatePending || !fdValid(transfer.FD()) {
		t.Fatalf("transfer not pending with valid fd: %+v", transfer)
	}

	claimed, err := auth.Claim(transfer.Token())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.State() != StateClaimed || claimed.GrantID() != root.ID() {
		t.Fatalf("claimed transfer mismatch: %+v", claimed)
	}
	defer syscall.Close(claimed.FD())

	// Tokens are single use.
	if _, err := auth.Claim(transfer.Token()); !errors.Is(err, ErrTransferGone) {
		t.Fatalf("double claim err = %v, want ErrTransferGone", err)
	}
	// The ledger dup differs from the caller's original fd.
	if claimed.FD() == fd {
		t.Fatal("authority did not dup the transferred descriptor")
	}
}

func TestTransferRequiresReadAndReexport(t *testing.T) {
	auth := NewAuthority()
	writeOnly, err := auth.Issue("buf0", PermWrite|PermReexport)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	noReexport, err := auth.Issue("buf0", PermRead|PermWrite)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	fd, cleanup := pipeFD(t)
	defer cleanup()

	if _, err := auth.BeginTransfer(writeOnly, "buf0", fd); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("write-only transfer err = %v, want ErrPermEscalation", err)
	}
	if _, err := auth.BeginTransfer(noReexport, "buf0", fd); !errors.Is(err, ErrReexportDenied) {
		t.Fatalf("no-reexport transfer err = %v, want ErrReexportDenied", err)
	}
	if _, err := auth.BeginTransfer(noReexport, "other-buffer", fd); !errors.Is(err, ErrReexportDenied) {
		t.Fatalf("reexport checked before buffer identity, got %v", err)
	}

	root, _ := auth.Issue("buf0", fullPerms())
	if _, err := auth.BeginTransfer(root, "wrong-buffer", fd); !errors.Is(err, ErrBufferMismatch) {
		t.Fatalf("buffer mismatch err = %v, want ErrBufferMismatch", err)
	}
	if _, err := auth.BeginTransfer(root, "buf0", -1); !errors.Is(err, ErrBadFD) {
		t.Fatalf("bad fd err = %v, want ErrBadFD", err)
	}
}

func TestRevokeReclaimsPendingTransfer(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	fd, cleanup := pipeFD(t)
	defer cleanup()

	transfer, err := auth.BeginTransfer(root, "buf0", fd)
	if err != nil {
		t.Fatalf("BeginTransfer: %v", err)
	}
	ledgerFD := transfer.FD()

	report, err := auth.Revoke(root)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(report.ReclaimedTokens) != 1 || report.ReclaimedTokens[0] != transfer.Token() {
		t.Fatalf("reclaimed tokens = %v", report.ReclaimedTokens)
	}
	if len(report.ClosedFDs) != 1 || report.ClosedFDs[0] != ledgerFD {
		t.Fatalf("closed fds = %v", report.ClosedFDs)
	}
	if fdValid(ledgerFD) {
		t.Fatal("ledger-owned fd was not closed on reclaim")
	}
	if transfer.State() != StateReclaimed {
		t.Fatalf("state = %v, want reclaimed", transfer.State())
	}
	if _, ok := auth.PendingTransfer(transfer.Token()); ok {
		t.Fatal("reclaimed token still present in ledger")
	}
	if _, err := auth.Claim(transfer.Token()); !errors.Is(err, ErrTransferGone) {
		t.Fatalf("claim reclaimed err = %v, want ErrTransferGone", err)
	}
	// The caller's original descriptor remains owned by the caller.
	if !fdValid(fd) {
		t.Fatal("revocation closed the caller-owned descriptor")
	}
}

func TestInFlightTransferRejectedAfterRevoke(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	bufferFD, cleanup := pipeFD(t)
	defer cleanup()

	transfer, err := auth.BeginTransfer(root, "buf0", bufferFD)
	if err != nil {
		t.Fatalf("BeginTransfer: %v", err)
	}

	socks, err := SocketPair()
	if err != nil {
		t.Fatalf("SocketPair: %v", err)
	}
	defer syscall.Close(socks[0])
	defer syscall.Close(socks[1])

	// The SCM_RIGHTS message is now queued in the kernel socket buffer,
	// i.e. in flight and outside the authority's direct fd control.
	if err := SendFD(socks[0], transfer.Token(), transfer.FD()); err != nil {
		t.Fatalf("SendFD: %v", err)
	}

	// Revocation cascades and marks the ledger entry reclaimed while the
	// message is still unread.
	report, err := auth.Revoke(root)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(report.ReclaimedTokens) != 1 {
		t.Fatalf("reclaimed tokens = %v", report.ReclaimedTokens)
	}

	// The receiver can still extract the kernel-held descriptor and token...
	token, receivedFD, err := ReceiveFD(socks[1])
	if err != nil {
		t.Fatalf("ReceiveFD: %v", err)
	}
	defer syscall.Close(receivedFD)
	if token != transfer.Token() {
		t.Fatalf("token = %q, want %q", token, transfer.Token())
	}
	if !fdValid(receivedFD) {
		t.Fatal("kernel did not deliver the in-flight descriptor")
	}

	// ...but the revoked token must not authorize its use.
	_, claimErr := auth.Claim(token)
	if !errors.Is(claimErr, ErrGrantRevoked) && !errors.Is(claimErr, ErrTransferGone) {
		t.Fatalf("Claim in-flight after revoke err = %v, want revocation error", claimErr)
	}
}

func TestMultipleReceiversIsolation(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Receiver A: read-only, may deliver but cannot continue the chain.
	recvA, err := auth.Forward(root, PermRead|PermReexport)
	if err != nil {
		t.Fatalf("Forward A: %v", err)
	}
	// Receiver B: read/write, may reexport but cannot map.
	recvB, err := auth.Forward(root, PermRead|PermWrite|PermReexport)
	if err != nil {
		t.Fatalf("Forward B: %v", err)
	}

	if err := auth.Check(recvA, PermRead); err != nil {
		t.Fatalf("A read: %v", err)
	}
	if err := auth.Check(recvB, PermWrite); err != nil {
		t.Fatalf("B write: %v", err)
	}
	if err := auth.Check(recvA, PermWrite); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("A write err = %v, want ErrPermEscalation", err)
	}
	if err := auth.Check(recvB, PermMap); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("B map err = %v, want ErrPermEscalation", err)
	}
	// A cannot derive any writable branch.
	if _, err := auth.Forward(recvA, PermRead|PermWrite); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("A gain write err = %v, want ErrPermEscalation", err)
	}

	// Each receiver gets its own ledger entry and duped fd.
	bufferFD, cleanup := pipeFD(t)
	defer cleanup()
	trA, err := auth.BeginTransfer(recvA, "buf0", bufferFD)
	if err != nil {
		t.Fatalf("transfer A: %v", err)
	}
	trB, err := auth.BeginTransfer(recvB, "buf0", bufferFD)
	if err != nil {
		t.Fatalf("transfer B: %v", err)
	}
	if trA.Token() == trB.Token() || trA.FD() == trB.FD() {
		t.Fatal("receivers share token or ledger fd")
	}

	// Revoking receiver A's branch never touches receiver B.
	if _, err := auth.Revoke(recvA); err != nil {
		t.Fatalf("Revoke A: %v", err)
	}
	if recvA.Live() {
		t.Fatal("receiver A grant still live after its own revocation")
	}
	if !recvB.Live() {
		t.Fatal("receiver isolation broken by revocation")
	}
	if trA.State() != StateReclaimed {
		t.Fatalf("A transfer = %v, want reclaimed", trA.State())
	}
	if _, err := auth.Claim(trB.Token()); err != nil {
		t.Fatalf("B claim after A revoke: %v", err)
	}
	if _, err := auth.Claim(trA.Token()); !errors.Is(err, ErrTransferGone) {
		t.Fatalf("A claim after revoke err = %v, want ErrTransferGone", err)
	}
}

func TestConcurrentForwardAndRevoke(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			grant, forwardErr := auth.Forward(root, PermRead|PermReexport)
			if forwardErr != nil {
				if !errors.Is(forwardErr, ErrGrantRevoked) {
					t.Errorf("Forward: %v", forwardErr)
				}
				return
			}
			// Every accepted grant must either still be live or be a
			// documented descendant of the revoked root, never a dangling
			// grant with widened rights.
			if grant.Perms() != PermRead|PermReexport {
				t.Errorf("grant perms = %v", grant.Perms())
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = auth.Revoke(root)
	}()
	wg.Wait()

	// After full revocation no new derivation or transfer is possible.
	if _, err := auth.Forward(root, PermRead); !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("post-revoke Forward err = %v, want ErrGrantRevoked", err)
	}
	fd, cleanup := pipeFD(t)
	defer cleanup()
	if _, err := auth.BeginTransfer(root, "buf0", fd); !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("post-revoke BeginTransfer err = %v, want ErrGrantRevoked", err)
	}
}
