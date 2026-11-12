package netsec

import (
	"bytes"
	"crypto/x509"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

func testCertificate(raw string) *x509.Certificate {
	return &x509.Certificate{Raw: []byte(raw)}
}

func testMAC(last byte) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x11, 0x22, 0x33, 0x44, last}
}

func testEnrollment(raw, wtpID, serial string, macLast byte) Enrollment {
	return Enrollment{
		Fingerprint:  FingerprintOfDER([]byte(raw)),
		WTPID:        wtpID,
		SerialNumber: serial,
		MAC:          testMAC(macLast),
	}
}

func testJoin(raw, wtpID, serial string, macLast byte, session SessionID, connection ConnectionID, source net.Addr) JoinRequest {
	return JoinRequest{
		Certificate: testCertificate(raw),
		WTPID:       wtpID,
		SessionID:   session,
		Connection:  connection,
		Source:      source,
		Board: WTPBoardData{
			SerialNumber: serial,
			MAC:          testMAC(macLast),
		},
	}
}

func testUDPAddr(ip string, port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
}

func mustBind(t *testing.T, binder *Binder, request JoinRequest) *Binding {
	t.Helper()
	binding, err := binder.BindJoin(request)
	if err != nil {
		t.Fatalf("BindJoin() error = %v", err)
	}
	return binding
}

func TestBindJoinBindsCertificateBoardIdentityAndConnection(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	request := testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247))

	binding := mustBind(t, binder, request)
	if binding == nil {
		t.Fatal("BindJoin() returned nil binding")
	}
	wantKey := BindingKey{
		Fingerprint: FingerprintOfDER([]byte("cert-a")),
		WTPID:       "wtp-a",
		SessionID:   7,
		Connection:  "dtls-a",
	}
	if got := binding.Key(); got != wantKey {
		t.Fatalf("Key() = %+v, want %+v", got, wantKey)
	}
	if binding.Fingerprint() != wantKey.Fingerprint || binding.WTPID() != wantKey.WTPID ||
		binding.SessionID() != wantKey.SessionID || binding.Connection() != wantKey.Connection {
		t.Fatalf("binding accessors do not match key %+v", wantKey)
	}
	if got := binder.Bindings(); len(got) != 1 || got[0] != binding {
		t.Fatalf("Bindings() = %v, want only the new binding", got)
	}
	if got, ok := binder.Binding(wantKey); !ok || got != binding {
		t.Fatalf("Binding(key) = %v, %v; want the new binding", got, ok)
	}
	if got, ok := binder.BindingByFingerprint(wantKey.Fingerprint); !ok || got != binding {
		t.Fatalf("BindingByFingerprint() = %v, %v; want the new binding", got, ok)
	}
	if got, ok := binder.BindingByWTP("wtp-a"); !ok || got != binding {
		t.Fatalf("BindingByWTP() = %v, %v; want the new binding", got, ok)
	}
	if got, ok := binder.BindingBySession(7); !ok || got != binding {
		t.Fatalf("BindingBySession() = %v, %v; want the new binding", got, ok)
	}
	if got, ok := binder.BindingByConnection("dtls-a"); !ok || got != binding {
		t.Fatalf("BindingByConnection() = %v, %v; want the new binding", got, ok)
	}
}

func TestBindJoinRejectsIdentitySpoofing(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)

	tests := []struct {
		name    string
		modify  func(*JoinRequest)
		wantErr error
	}{
		{
			name: "certificate used with another board serial",
			modify: func(request *JoinRequest) {
				request.Board.SerialNumber = "SN-B"
			},
			wantErr: ErrBoardMismatch,
		},
		{
			name: "certificate used with another board MAC",
			modify: func(request *JoinRequest) {
				request.Board.MAC = testMAC(0x02)
			},
			wantErr: ErrBoardMismatch,
		},
		{
			name: "certificate used with another WTP identity",
			modify: func(request *JoinRequest) {
				request.WTPID = "wtp-b"
			},
			wantErr: ErrIdentityMismatch,
		},
		{
			name: "unrelated certificate",
			modify: func(request *JoinRequest) {
				request.Certificate = testCertificate("cert-x")
			},
			wantErr: ErrUnknownCertificate,
		},
		{
			name: "fingerprint inconsistent with certificate",
			modify: func(request *JoinRequest) {
				request.Fingerprint = FingerprintOfDER([]byte("cert-x"))
			},
			wantErr: ErrInvalidJoin,
		},
		{
			name: "certificate and DER body inconsistent",
			modify: func(request *JoinRequest) {
				request.CertificateDER = []byte("cert-x")
			},
			wantErr: ErrInvalidJoin,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binder := NewBinder(enrollment)
			request := testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247))
			test.modify(&request)
			if _, err := binder.BindJoin(request); !errors.Is(err, test.wantErr) {
				t.Fatalf("BindJoin() error = %v, want %v", err, test.wantErr)
			}
			if got := binder.Bindings(); len(got) != 0 {
				t.Fatalf("Bindings() = %v after rejected join, want none", got)
			}
		})
	}
}

func TestBindJoinValidatesRequiredFields(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)

	tests := []struct {
		name   string
		modify func(*JoinRequest)
	}{
		{name: "missing certificate identity", modify: func(request *JoinRequest) {
			request.Certificate = nil
		}},
		{name: "missing WTP identity", modify: func(request *JoinRequest) {
			request.WTPID = " \t"
		}},
		{name: "missing connection ID", modify: func(request *JoinRequest) {
			request.Connection = ""
		}},
		{name: "missing serial number", modify: func(request *JoinRequest) {
			request.Board.SerialNumber = ""
		}},
		{name: "invalid MAC length", modify: func(request *JoinRequest) {
			request.Board.MAC = net.HardwareAddr{0x02, 0x11}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binder := NewBinder(enrollment)
			request := testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", nil)
			test.modify(&request)
			if _, err := binder.BindJoin(request); !errors.Is(err, ErrInvalidJoin) {
				t.Fatalf("BindJoin() error = %v, want %v", err, ErrInvalidJoin)
			}
		})
	}
}

func TestBindJoinRejectsDuplicateAndConflictingJoins(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	request := testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247))
	first := mustBind(t, binder, request)

	if _, err := binder.BindJoin(request); !errors.Is(err, ErrDuplicateJoin) {
		t.Fatalf("duplicate BindJoin() error = %v, want %v", err, ErrDuplicateJoin)
	}

	secondConnection := request
	secondConnection.Connection = "dtls-b"
	if _, err := binder.BindJoin(secondConnection); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("certificate on a second connection error = %v, want %v", err, ErrBindingConflict)
	}

	secondSession := request
	secondSession.Connection = "dtls-c"
	secondSession.SessionID = 8
	if _, err := binder.BindJoin(secondSession); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("certificate with a second session error = %v, want %v", err, ErrBindingConflict)
	}

	spoofedWTP := request
	spoofedWTP.WTPID = "wtp-b"
	if _, err := binder.BindJoin(spoofedWTP); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("spoofed WTP identity error = %v, want %v", err, ErrIdentityMismatch)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, ok := binder.Binding(first.Key()); ok {
		t.Fatal("binding is still registered after Close")
	}
	rejoined := mustBind(t, binder, request)
	if rejoined.Key() != first.Key() {
		t.Fatalf("rejoined key = %+v, want %+v", rejoined.Key(), first.Key())
	}
}

func TestBindJoinEnforcesGlobalWTPAndSessionUniqueness(t *testing.T) {
	t.Run("WTP identity cannot be bound to two certificates", func(t *testing.T) {
		binder := NewBinder(
			testEnrollment("cert-a", "shared-wtp", "SN-A", 0x01),
			testEnrollment("cert-b", "shared-wtp", "SN-B", 0x02),
		)
		mustBind(t, binder, testJoin("cert-a", "shared-wtp", "SN-A", 0x01, 1, "dtls-a", nil))
		if _, err := binder.BindJoin(testJoin("cert-b", "shared-wtp", "SN-B", 0x02, 2, "dtls-b", nil)); !errors.Is(err, ErrBindingConflict) {
			t.Fatalf("second certificate for WTP error = %v, want %v", err, ErrBindingConflict)
		}
	})

	t.Run("session ID cannot be reused by another endpoint", func(t *testing.T) {
		binder := NewBinder(
			testEnrollment("cert-a", "wtp-a", "SN-A", 0x01),
			testEnrollment("cert-b", "wtp-b", "SN-B", 0x02),
		)
		mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 42, "dtls-a", nil))
		if _, err := binder.BindJoin(testJoin("cert-b", "wtp-b", "SN-B", 0x02, 42, "dtls-b", nil)); !errors.Is(err, ErrSessionConflict) {
			t.Fatalf("shared session error = %v, want %v", err, ErrSessionConflict)
		}
	})

	t.Run("DTLS connection cannot carry two endpoints", func(t *testing.T) {
		binder := NewBinder(
			testEnrollment("cert-a", "wtp-a", "SN-A", 0x01),
			testEnrollment("cert-b", "wtp-b", "SN-B", 0x02),
		)
		mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 1, "dtls-shared", nil))
		if _, err := binder.BindJoin(testJoin("cert-b", "wtp-b", "SN-B", 0x02, 2, "dtls-shared", nil)); !errors.Is(err, ErrBindingConflict) {
			t.Fatalf("second endpoint on a DTLS connection error = %v, want %v", err, ErrBindingConflict)
		}
	})
}

func TestConcurrentDuplicateJoinsCreateOneBinding(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	request := testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247))

	const attempts = 16
	var successes int32
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := binder.BindJoin(request); err == nil {
				atomic.AddInt32(&successes, 1)
				return
			} else if !errors.Is(err, ErrDuplicateJoin) && !errors.Is(err, ErrBindingConflict) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("unexpected concurrent BindJoin() error = %v", err)
	}
	if got := atomic.LoadInt32(&successes); got != 1 {
		t.Fatalf("successful joins = %d, want 1", got)
	}
	if got := binder.Bindings(); len(got) != 1 {
		t.Fatalf("active bindings = %d, want 1", len(got))
	}
}

func TestControlRecordsRequireAuthenticationAndReplayWindow(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	binding := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247)))
	source := testUDPAddr("10.0.0.1", 5247)

	message, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, 10, []byte("ten")))
	if err != nil {
		t.Fatalf("Process(seq 10) error = %v", err)
	}
	if string(message.Payload) != "ten" || message.Sequence != 10 {
		t.Fatalf("Process(seq 10) = %+v, want payload ten and sequence 10", message)
	}
	for _, sequence := range []uint64{12, 11} {
		if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, sequence, []byte("ok"))); err != nil {
			t.Fatalf("Process(out-of-order seq %d) error = %v", sequence, err)
		}
	}
	for _, sequence := range []uint64{10, 11, 12} {
		if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, sequence, []byte("replay"))); !errors.Is(err, ErrReplay) {
			t.Fatalf("Process(replayed seq %d) error = %v, want %v", sequence, err, ErrReplay)
		}
	}

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, 100, []byte("new"))); err != nil {
		t.Fatalf("Process(seq 100) error = %v", err)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, 10, []byte("too old"))); !errors.Is(err, ErrReplay) {
		t.Fatalf("Process(seq 10 outside window) error = %v, want %v", err, ErrReplay)
	}

	unauthenticated := Record{
		Connection: "dtls-a",
		Source:     source,
		Epoch:      0,
		Sequence:   101,
		Payload:    []byte("unauthenticated"),
	}
	if _, err := binding.Process(unauthenticated); !errors.Is(err, ErrUnauthenticatedRecord) {
		t.Fatalf("Process(unauthenticated) error = %v, want %v", err, ErrUnauthenticatedRecord)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, 101, []byte("authenticated"))); err != nil {
		t.Fatalf("authenticated seq 101 was consumed by the rejected record: %v", err)
	}

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-b", source, 0, 102, []byte("wrong connection"))); !errors.Is(err, ErrConnectionMismatch) {
		t.Fatalf("Process(wrong connection) error = %v, want %v", err, ErrConnectionMismatch)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", source, 0, 102, []byte("right connection"))); err != nil {
		t.Fatalf("correct connection seq 102 error = %v", err)
	}
}

func TestAddressMigrationRequiresFreshAuthenticatedRecord(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	oldAddress := testUDPAddr("10.0.0.1", 5247)
	binding := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", oldAddress))
	newAddress := testUDPAddr("10.0.0.2", 5247)
	thirdAddress := testUDPAddr("10.0.0.3", 5247)
	fourthAddress := testUDPAddr("10.0.0.4", 5247)

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 0, 1, []byte("initial"))); err != nil {
		t.Fatalf("initial record error = %v", err)
	}

	unauthenticated := Record{
		Connection: "dtls-a",
		Source:     newAddress,
		Epoch:      0,
		Sequence:   2,
		Payload:    []byte("spoofed"),
	}
	if _, err := binding.Process(unauthenticated); !errors.Is(err, ErrUnauthenticatedRecord) {
		t.Fatalf("unauthenticated migration error = %v, want %v", err, ErrUnauthenticatedRecord)
	}
	assertSameAddress(t, binding.Source(), oldAddress)

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", newAddress, 0, 1, []byte("replayed"))); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed migration error = %v, want %v", err, ErrReplay)
	}
	assertSameAddress(t, binding.Source(), oldAddress)

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", newAddress, 0, 2, []byte("migrate"))); err != nil {
		t.Fatalf("authenticated migration error = %v", err)
	}
	assertSameAddress(t, binding.Source(), newAddress)

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 0, 3, []byte("old address"))); !errors.Is(err, ErrStaleAddress) {
		t.Fatalf("old address record error = %v, want %v", err, ErrStaleAddress)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 0, 2, []byte("old address replay"))); !errors.Is(err, ErrStaleAddress) {
		t.Fatalf("old address replayed record error = %v, want %v", err, ErrStaleAddress)
	}
	assertSameAddress(t, binding.Source(), newAddress)

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", newAddress, 0, 3, []byte("new address"))); err != nil {
		t.Fatalf("new address record error = %v", err)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", thirdAddress, 0, 4, []byte("migrate again"))); err != nil {
		t.Fatalf("second migration error = %v", err)
	}
	assertSameAddress(t, binding.Source(), thirdAddress)

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", newAddress, 0, 5, []byte("retired second address"))); !errors.Is(err, ErrStaleAddress) {
		t.Fatalf("retired second address error = %v, want %v", err, ErrStaleAddress)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 0, 6, []byte("retired first address"))); !errors.Is(err, ErrStaleAddress) {
		t.Fatalf("retired first address error = %v, want %v", err, ErrStaleAddress)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", thirdAddress, 0, 5, []byte("current address"))); err != nil {
		t.Fatalf("current address record error = %v", err)
	}

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", fourthAddress, 0, 5, []byte("replayed migration"))); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed migration to fourth address error = %v, want %v", err, ErrReplay)
	}
	assertSameAddress(t, binding.Source(), thirdAddress)
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", fourthAddress, 0, 6, []byte("migrate to fourth"))); err != nil {
		t.Fatalf("migration to fourth address error = %v", err)
	}
	assertSameAddress(t, binding.Source(), fourthAddress)
}

func TestStaleEpochCannotMigrateAddress(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	oldAddress := testUDPAddr("10.0.0.1", 5247)
	newAddress := testUDPAddr("10.0.0.2", 5247)
	binding := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", oldAddress))

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 0, 100, []byte("epoch zero"))); err != nil {
		t.Fatalf("epoch zero record error = %v", err)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 1, 0, []byte("epoch one"))); err != nil {
		t.Fatalf("new epoch record error = %v", err)
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", newAddress, 0, 101, []byte("stale epoch"))); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale epoch migration error = %v, want %v", err, ErrStaleEpoch)
	}
	assertSameAddress(t, binding.Source(), oldAddress)
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", oldAddress, 1, 0, []byte("same epoch replay"))); !errors.Is(err, ErrReplay) {
		t.Fatalf("same epoch replay error = %v, want %v", err, ErrReplay)
	}
}

func TestFirstAuthenticatedRecordEstablishesAddressWhenJoinHasNone(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	binding := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", nil))
	if binding.Source() != nil {
		t.Fatalf("initial Source() = %v, want nil", binding.Source())
	}

	address := testUDPAddr("10.0.0.1", 5247)
	unauthenticated := Record{Connection: "dtls-a", Source: address, Sequence: 1}
	if _, err := binding.Process(unauthenticated); !errors.Is(err, ErrUnauthenticatedRecord) {
		t.Fatalf("unauthenticated first record error = %v, want %v", err, ErrUnauthenticatedRecord)
	}
	if binding.Source() != nil {
		t.Fatalf("Source() = %v after unauthenticated record, want nil", binding.Source())
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", address, 0, 1, nil)); err != nil {
		t.Fatalf("first authenticated record error = %v", err)
	}
	assertSameAddress(t, binding.Source(), address)
}

func TestEquivalentIPRepresentationsDoNotMigrate(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	first := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5247}
	binding := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", first))

	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", first, 0, 1, nil)); err != nil {
		t.Fatalf("first record error = %v", err)
	}
	equivalent := &net.UDPAddr{IP: net.IP{10, 0, 0, 1}, Port: 5247}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", equivalent, 0, 2, nil)); err != nil {
		t.Fatalf("equivalent IPv4 representation error = %v", err)
	}
	// If the two representations had been treated as a migration, the original
	// address would now be retired and this record would fail.
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", first, 0, 3, nil)); err != nil {
		t.Fatalf("original representation after equivalent record error = %v", err)
	}
}

func TestClosedBindingRejectsRecords(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binder := NewBinder(enrollment)
	binding := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247)))
	if err := binding.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if !binding.Closed() {
		t.Fatal("Closed() = false after Close")
	}
	if _, err := binding.Process(NewAuthenticatedRecord("dtls-a", testUDPAddr("10.0.0.1", 5247), 0, 1, nil)); !errors.Is(err, ErrBindingClosed) {
		t.Fatalf("Process() after Close error = %v, want %v", err, ErrBindingClosed)
	}
}

func TestEnrollmentLookupAndReEnrollment(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	lookup := func(fingerprint Fingerprint) (Enrollment, bool) {
		if fingerprint != enrollment.Fingerprint {
			return Enrollment{}, false
		}
		return enrollment, true
	}
	binder := NewBinderWithLookup(lookup, DefaultOptions())
	request := testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", nil)
	binding := mustBind(t, binder, request)

	if err := binder.Enroll(enrollment); err != nil {
		t.Fatalf("idempotent Enroll() while active error = %v", err)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := binder.Enroll(enrollment); err != nil {
		t.Fatalf("idempotent Enroll() error = %v", err)
	}
	changed := enrollment
	changed.SerialNumber = "SN-B"
	if err := binder.Enroll(changed); !errors.Is(err, ErrEnrollmentConflict) {
		t.Fatalf("conflicting Enroll() error = %v, want %v", err, ErrEnrollmentConflict)
	}
}

func TestProcessPayloadAlias(t *testing.T) {
	enrollment := testEnrollment("cert-a", "wtp-a", "SN-A", 0x01)
	binding := mustBind(t, NewBinder(enrollment), testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", testUDPAddr("10.0.0.1", 5247)))
	payload, err := binding.ProcessPayload(NewAuthenticatedRecord("dtls-a", testUDPAddr("10.0.0.1", 5247), 0, 1, []byte{0x01, 0x02}))
	if err != nil {
		t.Fatalf("ProcessPayload() error = %v", err)
	}
	if !bytes.Equal(payload, []byte{0x01, 0x02}) {
		t.Fatalf("ProcessPayload() = %v, want [1 2]", payload)
	}
}

func TestRevokeAndCloseAll(t *testing.T) {
	binder := NewBinder(
		testEnrollment("cert-a", "wtp-a", "SN-A", 0x01),
		testEnrollment("cert-b", "wtp-b", "SN-B", 0x02),
	)
	bindingA := mustBind(t, binder, testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", nil))
	bindingB := mustBind(t, binder, testJoin("cert-b", "wtp-b", "SN-B", 0x02, 8, "dtls-b", nil))

	binder.Revoke(bindingA.Key().Fingerprint)
	if !bindingA.Closed() {
		t.Fatal("revoked binding is still open")
	}
	if bindingB.Closed() {
		t.Fatal("unrelated binding was closed by Revoke")
	}
	if _, ok := binder.Binding(bindingA.Key()); ok {
		t.Fatal("revoked binding is still registered")
	}
	if _, err := binder.BindJoin(testJoin("cert-a", "wtp-a", "SN-A", 0x01, 7, "dtls-a", nil)); !errors.Is(err, ErrUnknownCertificate) {
		t.Fatalf("join after Revoke error = %v, want %v", err, ErrUnknownCertificate)
	}

	binder.CloseAll()
	if !bindingB.Closed() {
		t.Fatal("CloseAll did not close the remaining binding")
	}
	if got := binder.Bindings(); len(got) != 0 {
		t.Fatalf("Bindings() = %v after CloseAll, want none", got)
	}
}

type stringAddr struct {
	network string
	value   string
}

func (a stringAddr) Network() string { return a.network }
func (a stringAddr) String() string  { return a.value }

func TestAddressKeySeparatesTransportsAndRejectsInvalidPorts(t *testing.T) {
	udp := testUDPAddr("10.0.0.1", 5247)
	tcp := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5247}
	udpKey, err := addressKey(udp)
	if err != nil {
		t.Fatalf("addressKey(udp) error = %v", err)
	}
	tcpKey, err := addressKey(tcp)
	if err != nil {
		t.Fatalf("addressKey(tcp) error = %v", err)
	}
	if udpKey == tcpKey {
		t.Fatalf("UDP and TCP addresses produced the same key %q", udpKey)
	}

	custom := stringAddr{network: "custom", value: "10.0.0.1:5247"}
	customKey, err := addressKey(custom)
	if err != nil {
		t.Fatalf("addressKey(custom) error = %v", err)
	}
	if customKey == udpKey {
		t.Fatalf("custom and UDP addresses produced the same key %q", customKey)
	}

	if _, err := addressKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: -1}); err == nil {
		t.Fatal("addressKey(negative UDP port) error = nil, want error")
	}
	if _, err := addressKey(&net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 65536}); err == nil {
		t.Fatal("addressKey(out-of-range TCP port) error = nil, want error")
	}
	if _, err := addressKey(nil); err == nil {
		t.Fatal("addressKey(nil) error = nil, want error")
	}
}

func assertSameAddress(t *testing.T, got, want net.Addr) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("address = %v, want %v", got, want)
		}
		return
	}
	gotKey, err := addressKey(got)
	if err != nil {
		t.Fatalf("addressKey(got) error = %v", err)
	}
	wantKey, err := addressKey(want)
	if err != nil {
		t.Fatalf("addressKey(want) error = %v", err)
	}
	if gotKey != wantKey {
		t.Fatalf("address = %v, want %v", got, want)
	}
}
