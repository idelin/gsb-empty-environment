package nvmf

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

var (
	hostA   = "nqn.2019-04.com.example:host-a"
	hostB   = "nqn.2019-04.com.example:host-b"
	hostC   = "nqn.2019-04.com.example:host-c"
	subNQN1 = "nqn.2019-04.com.example:subsystem-one"
	subNQN2 = "nqn.2019-04.com.example:subsystem-two"
)

func mustHostID(b byte) HostID {
	var id HostID
	id[15] = b
	return id
}

func newTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()
	sub1, err := NewSubsystem(subNQN1, hostA, hostB)
	if err != nil {
		t.Fatalf("NewSubsystem: %v", err)
	}
	sub2, err := NewSubsystem(subNQN2, hostA)
	if err != nil {
		t.Fatalf("NewSubsystem: %v", err)
	}
	return NewAuthorizer(sub1, sub2)
}

func TestSubsystemAllowlistRejectsMalformedEntries(t *testing.T) {
	if _, err := NewSubsystem(subNQN1, "not-an-nqn"); !errors.Is(err, ErrInvalidNQN) {
		t.Fatalf("NewSubsystem with bad host: got %v, want ErrInvalidNQN", err)
	}
	sub, err := NewSubsystem(subNQN1)
	if err != nil {
		t.Fatalf("NewSubsystem: %v", err)
	}
	if err := sub.AllowHost("nqn.2014-08.org.nvmexpress:uuid:bad"); !errors.Is(err, ErrMalformedUUID) {
		t.Fatalf("AllowHost bad uuid: got %v, want ErrMalformedUUID", err)
	}
	if got := sub.AllowedHosts(); len(got) != 0 {
		t.Fatalf("AllowedHosts = %v, want empty", got)
	}
}

func TestAllowlistIsExactByteMatch(t *testing.T) {
	sub, err := NewSubsystem(subNQN1, hostA)
	if err != nil {
		t.Fatalf("NewSubsystem: %v", err)
	}
	variants := []string{
		strings.ToUpper(hostA[:3]) + hostA[3:], // NQN.2019...
		strings.ToUpper(hostA),                 // all uppercase
		" " + hostA,                            // leading space
		hostA + " ",                            // trailing space
		"nqn.2019-04.COM.example:host-a",       // uppercase reverse domain
		"nqn.2019-04.com.example:host-A",       // uppercase suffix char
	}
	for _, variant := range variants {
		if sub.allows(variant) {
			t.Fatalf("allowlist matched non-exact variant %q for entry %q", variant, hostA)
		}
	}
	if !sub.allows(hostA) {
		t.Fatalf("exact allowlisted NQN %q not matched", hostA)
	}
}

func TestAdmitConnectHappyPath(t *testing.T) {
	auth := newTestAuthorizer(t)
	req := ConnectRequest{
		HostNQN:      hostA,
		HostID:       mustHostID(1),
		SubsystemNQN: subNQN1,
		ControllerID: 5,
		QueueID:      AdminQueueID,
	}
	if err := auth.AdmitConnect(req); err != nil {
		t.Fatalf("admin Connect: %v", err)
	}
	ioReq := req
	ioReq.QueueID = 1
	if err := auth.AdmitConnect(ioReq); err != nil {
		t.Fatalf("IO Connect on same controller: %v", err)
	}
}

func TestAdmitConnectRejectsUnknownSubsystemAndEmptyHostID(t *testing.T) {
	auth := newTestAuthorizer(t)
	req := ConnectRequest{
		HostNQN:      hostA,
		HostID:       mustHostID(1),
		SubsystemNQN: "nqn.2019-04.com.example:missing",
		ControllerID: 1,
		QueueID:      AdminQueueID,
	}
	if err := auth.AdmitConnect(req); !errors.Is(err, ErrUnknownSubsystem) {
		t.Fatalf("unknown subsystem: got %v, want ErrUnknownSubsystem", err)
	}
	req.SubsystemNQN = subNQN1
	req.HostID = HostID{}
	if err := auth.AdmitConnect(req); !errors.Is(err, ErrEmptyHostID) {
		t.Fatalf("zero HostID: got %v, want ErrEmptyHostID", err)
	}
}

func TestImpersonationRejectedAndRebindAfterRelease(t *testing.T) {
	auth := newTestAuthorizer(t)
	first := ConnectRequest{
		HostNQN:      hostA,
		HostID:       mustHostID(1),
		SubsystemNQN: subNQN1,
		ControllerID: 7,
		QueueID:      AdminQueueID,
	}
	if err := auth.AdmitConnect(first); err != nil {
		t.Fatalf("first Connect: %v", err)
	}

	// Attacker presents the allowlisted NQN but a different HostID.
	imposter := first
	imposter.HostID = mustHostID(2)
	if err := auth.AdmitConnect(imposter); !errors.Is(err, ErrHostIDMismatch) {
		t.Fatalf("imposter Connect: got %v, want ErrHostIDMismatch", err)
	}

	// The genuine owner may open more connections with the same HostID.
	second := first
	second.QueueID = 3
	if err := auth.AdmitConnect(second); err != nil {
		t.Fatalf("same-HostID second Connect: %v", err)
	}

	// No binding state should have been created for the imposter.
	auth.mu.Lock()
	if got := auth.bindings[hostA].refCount; got != 2 {
		t.Fatalf("binding refCount = %d, want 2", got)
	}
	auth.mu.Unlock()

	// Releasing every genuine connection frees the NQN for a fresh binding.
	auth.ReleaseConnection(second)
	auth.ReleaseConnection(first)
	if err := auth.AdmitConnect(imposter); err != nil {
		t.Fatalf("rebind after release: %v", err)
	}
}

func TestOverlongNQNRejectedBeforeAllowlist(t *testing.T) {
	auth := newTestAuthorizer(t)
	longNQN := "nqn.2020-01.com.example:" + strings.Repeat("a", 200) // 224 bytes
	req := ConnectRequest{
		HostNQN:      longNQN,
		HostID:       mustHostID(9),
		SubsystemNQN: subNQN1,
		ControllerID: 1,
		QueueID:      AdminQueueID,
	}
	if err := auth.AdmitConnect(req); !errors.Is(err, ErrNQNTooLong) {
		t.Fatalf("overlong NQN Connect: got %v, want ErrNQNTooLong", err)
	}
	auth.mu.Lock()
	if len(auth.bindings) != 0 {
		t.Fatalf("rejected Connect created binding state: %v", auth.bindings)
	}
	auth.mu.Unlock()
}

func TestNonUTF8AndCaseVariantNQNCannotBypassAllowlist(t *testing.T) {
	auth := newTestAuthorizer(t)
	base := ConnectRequest{
		HostID:       mustHostID(4),
		SubsystemNQN: subNQN1,
		ControllerID: 9,
		QueueID:      AdminQueueID,
	}

	base.HostNQN = "nqn.2019-04.COM.example:host-a"
	if err := auth.AdmitConnect(base); !errors.Is(err, ErrInvalidNQN) {
		t.Fatalf("uppercase domain Connect: got %v, want ErrInvalidNQN", err)
	}

	base.HostNQN = "NQN.2019-04.com.example:host-a"
	if err := auth.AdmitConnect(base); !errors.Is(err, ErrInvalidNQN) {
		t.Fatalf("uppercase prefix Connect: got %v, want ErrInvalidNQN", err)
	}

	// Syntactically valid but differing bytes must not match the allowlist.
	base.HostNQN = "nqn.2019-04.com.example:host-A"
	if err := auth.AdmitConnect(base); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("case-variant suffix Connect: got %v, want ErrHostNotAllowed", err)
	}

	base.HostNQN = "nqn.2019-04.com.example:\xff\xfe"
	if err := auth.AdmitConnect(base); !errors.Is(err, ErrNQNNotUTF8) {
		t.Fatalf("non-UTF8 Connect: got %v, want ErrNQNNotUTF8", err)
	}

	// Upper-case UUID prefix is a distinct NQN byte string and therefore
	// cannot match the lower-case allowlist entry of the same logical host.
	base.HostNQN = "nqn.2014-08.org.nvmexpress:UUID:123e4567-e89b-12d3-a456-426614174000"
	if err := auth.AdmitConnect(base); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("uppercase UUID-prefix Connect: got %v, want ErrHostNotAllowed", err)
	}
}

func TestDiscoveryLogFiltersByHost(t *testing.T) {
	auth := newTestAuthorizer(t)

	logA, err := auth.DiscoveryLog(hostA)
	if err != nil {
		t.Fatalf("DiscoveryLog: %v", err)
	}
	if len(logA) != 2 || logA[0].SubsystemNQN != subNQN1 || logA[1].SubsystemNQN != subNQN2 {
		t.Fatalf("hostA discovery log = %+v, want both subsystems sorted", logA)
	}

	logB, err := auth.DiscoveryLog(hostB)
	if err != nil {
		t.Fatalf("DiscoveryLog: %v", err)
	}
	if len(logB) != 1 || logB[0].SubsystemNQN != subNQN1 {
		t.Fatalf("hostB discovery log = %+v, want only %s", logB, subNQN1)
	}

	logC, err := auth.DiscoveryLog(hostC)
	if err != nil {
		t.Fatalf("DiscoveryLog: %v", err)
	}
	if len(logC) != 0 {
		t.Fatalf("unallowlisted host discovery log = %+v, want empty", logC)
	}

	if _, err := auth.DiscoveryLog("NQN.2019-04.com.example:host-a"); !errors.Is(err, ErrInvalidNQN) {
		t.Fatalf("malformed discovery request: got %v, want ErrInvalidNQN", err)
	}
}

func TestControllerIDCannotBeReusedByAnotherHost(t *testing.T) {
	auth := newTestAuthorizer(t)
	const cid uint16 = 11

	adminA := ConnectRequest{
		HostNQN:      hostA,
		HostID:       mustHostID(1),
		SubsystemNQN: subNQN1,
		ControllerID: cid,
		QueueID:      AdminQueueID,
	}
	if err := auth.AdmitConnect(adminA); err != nil {
		t.Fatalf("hostA admin Connect: %v", err)
	}

	// hostB is allowlisted on the same subsystem but cannot attach an IO queue
	// to hostA's controller ID.
	ioB := ConnectRequest{
		HostNQN:      hostB,
		HostID:       mustHostID(2),
		SubsystemNQN: subNQN1,
		ControllerID: cid,
		QueueID:      2,
	}
	if err := auth.AdmitConnect(ioB); !errors.Is(err, ErrControllerInUse) {
		t.Fatalf("cross-host IO reuse: got %v, want ErrControllerInUse", err)
	}

	// Neither can hostB claim the controller ID with its own admin queue.
	adminB := ioB
	adminB.QueueID = AdminQueueID
	if err := auth.AdmitConnect(adminB); !errors.Is(err, ErrControllerInUse) {
		t.Fatalf("cross-host admin reuse: got %v, want ErrControllerInUse", err)
	}

	// An IO queue for a controller ID nobody established is rejected too.
	orphan := ioB
	orphan.ControllerID = 999
	if err := auth.AdmitConnect(orphan); !errors.Is(err, ErrNoAdminQueue) {
		t.Fatalf("orphan IO queue: got %v, want ErrNoAdminQueue", err)
	}

	// The owner keeps full use of the controller.
	ioA := adminA
	ioA.QueueID = 4
	if err := auth.AdmitConnect(ioA); err != nil {
		t.Fatalf("owner IO Connect: %v", err)
	}

	// After the admin queue is torn down the controller ID can be reassigned.
	auth.ReleaseConnection(adminA)
	if err := auth.AdmitConnect(adminB); err != nil {
		t.Fatalf("reuse after admin release: %v", err)
	}
}

func TestControllerOwnershipIsScopedPerSubsystem(t *testing.T) {
	auth := newTestAuthorizer(t)
	const cid uint16 = 3
	a1 := ConnectRequest{
		HostNQN:      hostA,
		HostID:       mustHostID(1),
		SubsystemNQN: subNQN1,
		ControllerID: cid,
		QueueID:      AdminQueueID,
	}
	a2 := a1
	a2.SubsystemNQN = subNQN2
	if err := auth.AdmitConnect(a1); err != nil {
		t.Fatalf("admin on sub1: %v", err)
	}
	if err := auth.AdmitConnect(a2); err != nil {
		t.Fatalf("same controller ID on separate subsystem: %v", err)
	}
}

func TestAdmitConnectConcurrent(t *testing.T) {
	auth := newTestAuthorizer(t)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := ConnectRequest{
				HostNQN:      hostA,
				HostID:       mustHostID(1),
				SubsystemNQN: subNQN1,
				ControllerID: uint16(20 + i),
				QueueID:      AdminQueueID,
			}
			if err := auth.AdmitConnect(req); err != nil {
				t.Errorf("concurrent Connect %d: %v", i, err)
				return
			}
			auth.ReleaseConnection(req)
		}(i)
	}
	wg.Wait()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if len(auth.bindings) != 0 {
		t.Fatalf("bindings leaked after release: %v", auth.bindings)
	}
}

func TestAddSubsystemAndReleaseController(t *testing.T) {
	auth := NewAuthorizer()
	sub, err := NewSubsystem(subNQN1, hostA)
	if err != nil {
		t.Fatalf("NewSubsystem: %v", err)
	}
	auth.AddSubsystem(sub)

	req := ConnectRequest{
		HostNQN:      hostA,
		HostID:       mustHostID(1),
		SubsystemNQN: subNQN1,
		ControllerID: 8,
		QueueID:      AdminQueueID,
	}
	if err := auth.AdmitConnect(req); err != nil {
		t.Fatalf("Connect after AddSubsystem: %v", err)
	}

	auth.ReleaseController(subNQN1, req.ControllerID)
	io := req
	io.QueueID = 1
	if err := auth.AdmitConnect(io); !errors.Is(err, ErrNoAdminQueue) {
		t.Fatalf("IO Connect after ReleaseController: got %v, want ErrNoAdminQueue", err)
	}

	// Releasing an unknown controller ID is a no-op, not an error.
	auth.ReleaseController("nqn.unknown:sub", 42)
}

func TestNewSubsystemRejectsEmptyNQN(t *testing.T) {
	if _, err := NewSubsystem(""); !errors.Is(err, ErrEmptyNQN) {
		t.Fatalf("NewSubsystem(\"\"): got %v, want ErrEmptyNQN", err)
	}
}
