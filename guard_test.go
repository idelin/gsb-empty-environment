package netsec

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

const (
	bootstrapURI  = "coaps://bs.example.org:5684"
	managementURI = "coaps://mgmt.example.org:5684"
)

func newSeededStore() *Store {
	store := NewStore()

	// Instance 0: trusted bootstrap server account (PSK).
	store.SetResource(ObjectSecurity, 0, ResServerURI, bootstrapURI)
	store.SetResource(ObjectSecurity, 0, ResBootstrapServer, true)
	store.SetResource(ObjectSecurity, 0, ResSecurityMode, SecModePSK)
	store.SetResource(ObjectSecurity, 0, ResClientIdentity, []byte("client-psk-id"))
	store.SetResource(ObjectSecurity, 0, ResSecretKey, []byte("bs-psk-secret"))

	// Instance 1: management server account secured with PSK.
	store.SetResource(ObjectSecurity, 1, ResServerURI, managementURI)
	store.SetResource(ObjectSecurity, 1, ResBootstrapServer, false)
	store.SetResource(ObjectSecurity, 1, ResSecurityMode, SecModePSK)
	store.SetResource(ObjectSecurity, 1, ResClientIdentity, []byte("mgmt-id"))
	store.SetResource(ObjectSecurity, 1, ResSecretKey, []byte("mgmt-psk-secret"))

	// Server Object instance paired with management security instance 1.
	store.SetResource(ObjectServer, 1, 0, 1)
	return store
}

func trustedSessionInfo() SessionInfo {
	return SessionInfo{
		ServerURI: bootstrapURI,
		Mode:      SecModePSK,
		Identity:  []byte("client-psk-id"),
		ServerKey: []byte("bs-psk-secret"),
	}
}

func trustedSetup(opts ...Option) (*Store, *Guard, SessionInfo) {
	store := newSeededStore()
	options := append([]Option{}, opts...)
	guard := NewGuard(store, TrustAnchorsFromStore(store), options...)
	return store, guard, trustedSessionInfo()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

// A fake bootstrap server must never be able to write, delete or finish,
// regardless of how it tries to impersonate the trusted server.
func TestFakeServerCannotMutateStore(t *testing.T) {
	fakePeers := []SessionInfo{
		{ // Correct URI and identity but an attacker-controlled PSK.
			ServerURI: bootstrapURI,
			Mode:      SecModePSK,
			Identity:  []byte("client-psk-id"),
			ServerKey: []byte("attacker-psk"),
		},
		{ // Correct key material but spoofed host.
			ServerURI: "coaps://evil.example.org:5684",
			Mode:      SecModePSK,
			Identity:  []byte("client-psk-id"),
			ServerKey: []byte("bs-psk-secret"),
		},
		{ // NoSec connection with no authenticated key at all.
			ServerURI: bootstrapURI,
			Mode:      SecModeNoSec,
		},
	}

	for _, fake := range fakePeers {
		store, guard, _ := trustedSetup()
		before := store.snapshot()

		session := guard.BeginSession(fake)
		if session.Trusted() {
			t.Fatalf("fake peer must not be trusted: %+v", fake)
		}

		wantErr(t, session.Write("/0/2", ResourceSet{
			ResServerURI:       "coaps://attacker.example.org",
			ResBootstrapServer: false,
			ResSecurityMode:    SecModePSK,
		}), ErrUntrustedServer)

		wantErr(t, session.Write("/0/1/0", "coaps://attacker.example.org"), ErrUntrustedServer)
		wantErr(t, session.Delete("/"), ErrUntrustedServer)
		wantErr(t, session.Delete("/0/0"), ErrUntrustedServer)
		wantErr(t, session.Finish(), ErrUntrustedServer)

		if !reflect.DeepEqual(store.data, before) {
			t.Fatalf("fake peer %+v mutated the store", fake)
		}
		if !session.Active() {
			t.Fatalf("rejected Finish must not close the session for %+v", fake)
		}
	}
}

// Root-level delete must wipe management state but keep every bootstrap
// account so the client can still reach its real bootstrap server.
func TestRootDeletePreservesBootstrapAccounts(t *testing.T) {
	store, guard, info := trustedSetup()
	session := guard.BeginSession(info)
	if !session.Trusted() {
		t.Fatal("legitimate bootstrap server must be trusted")
	}

	// Directly deleting a bootstrap account is always refused.
	wantErr(t, session.Delete("/0/0"), ErrDeleteBootstrapAccount)
	if _, ok := store.GetResource(ObjectSecurity, 0, ResServerURI); !ok {
		t.Fatal("bootstrap account must survive an explicit delete attempt")
	}

	must(t, session.Delete("/"))

	if !store.IsBootstrapAccount(0) {
		t.Fatal("bootstrap account missing after root delete")
	}
	if key, _ := store.GetResource(ObjectSecurity, 0, ResSecretKey); !bytes.Equal(key.([]byte), []byte("bs-psk-secret")) {
		t.Fatal("bootstrap account key material changed during root delete")
	}
	if slices.Contains(store.Instances(ObjectSecurity), 1) {
		t.Fatal("management security instance must be removed by root delete")
	}
	if slices.Contains(store.Objects(), ObjectServer) {
		t.Fatal("Server Object must be removed by root delete")
	}
	if store.HasSecureManagementAccount() {
		t.Fatal("no management account should remain immediately after root delete")
	}

	// Re-provision a secured management server; Bootstrap-Finish then commits.
	must(t, session.Write("/0/2", ResourceSet{
		ResServerURI:       "coaps://mgmt2.example.org:5684",
		ResBootstrapServer: false,
		ResSecurityMode:    SecModeCertificate,
		ResServerPublicKey: []byte("server-cert"),
	}))
	must(t, session.Finish())

	if session.Active() {
		t.Fatal("finished session must be inactive")
	}
	got := store.Instances(ObjectSecurity)
	if !slices.Equal(got, []int{0, 2}) {
		t.Fatalf("security instances after finish = %v, want [0 2]", got)
	}
}

// Bootstrap-Finish with only NoSec management accounts must reject the
// session and roll every mutation back to the pre-session snapshot.
func TestNoSecOnlyManagementServerRollsBack(t *testing.T) {
	store, guard, info := trustedSetup()
	before := store.snapshot()
	session := guard.BeginSession(info)

	must(t, session.Delete("/0/1"))
	must(t, session.Write("/0/2", ResourceSet{
		ResServerURI:       "coap://nosec-mgmt.example.org:5683",
		ResBootstrapServer: false,
		ResSecurityMode:    SecModeNoSec,
	}))

	wantErr(t, session.Finish(), ErrNoSecureManagementServer)

	if !reflect.DeepEqual(store.data, before) {
		t.Fatalf("store was not rolled back after NoSec-only finish")
	}
	if slices.Contains(store.Instances(ObjectSecurity), 2) {
		t.Fatal("NoSec instance must disappear after rollback")
	}
	if !slices.Contains(store.Instances(ObjectSecurity), 1) {
		t.Fatal("original secured management instance must be restored")
	}

	wantErr(t, session.Finish(), ErrSessionClosed)
}

// Once the session lifetime elapses, all writes, deletes and finish are
// refused even for a fully authenticated bootstrap server.
func TestTimeoutRejectsMutations(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }

	store, guard, info := trustedSetup(WithTimeout(time.Minute), WithClock(clock))
	session := guard.BeginSession(info)

	must(t, session.Write("/0/1/0", "coaps://mgmt.example.org:5684"))

	now = now.Add(2 * time.Minute)

	wantErr(t, session.Write("/0/2", ResourceSet{
		ResSecurityMode: SecModePSK,
	}), ErrSessionTimeout)
	wantErr(t, session.Delete("/"), ErrSessionTimeout)
	wantErr(t, session.Delete("/0/1"), ErrSessionTimeout)
	wantErr(t, session.Finish(), ErrSessionTimeout)

	if slices.Contains(store.Instances(ObjectSecurity), 2) {
		t.Fatal("timed-out write must not create an instance")
	}
	if _, ok := store.GetResource(ObjectSecurity, 1, ResServerURI); !ok {
		t.Fatal("pre-timeout state must remain intact after rejected finish")
	}
}

// A complete trusted session commits validated changes and rejects further
// mutations after close.
func TestTrustedSessionCommitsAndCloses(t *testing.T) {
	store, guard, info := trustedSetup()
	session := guard.BeginSession(info)

	must(t, session.Write("/0/2", ResourceSet{
		ResServerURI:       "coaps://mgmt2.example.org:5684",
		ResBootstrapServer: false,
		ResSecurityMode:    SecModeRawPK,
		ResServerPublicKey: []byte("raw-pub-key"),
	}))
	must(t, session.Delete("/1/1"))
	must(t, session.Finish())

	if slices.Contains(store.Objects(), ObjectServer) {
		t.Fatal("committed Server Object deletion did not persist")
	}
	if !slices.Contains(store.Instances(ObjectSecurity), 2) {
		t.Fatal("committed new management instance did not persist")
	}
	wantErr(t, session.Write("/0/3", ResourceSet{}), ErrSessionClosed)
	wantErr(t, session.Delete("/0/2"), ErrSessionClosed)
}

// Snapshot values are deep copies: external mutation of returned byte slices
// and Discard must restore the exact pre-session state, including key bytes.
func TestSnapshotAndValueDeepCopy(t *testing.T) {
	store, guard, info := trustedSetup()
	session := guard.BeginSession(info)

	raw, ok := store.GetResource(ObjectSecurity, 0, ResSecretKey)
	if !ok {
		t.Fatal("bootstrap PSK missing")
	}
	raw.([]byte)[0] = 'X'
	again, _ := store.GetResource(ObjectSecurity, 0, ResSecretKey)
	if again.([]byte)[0] == 'X' {
		t.Fatal("GetResource must return a defensive copy of key material")
	}

	store.SetResource(ObjectSecurity, 1, ResSecretKey, []byte("tampered"))
	session.Discard()

	restored, _ := store.GetResource(ObjectSecurity, 1, ResSecretKey)
	if !bytes.Equal(restored.([]byte), []byte("mgmt-psk-secret")) {
		t.Fatalf("Discard did not restore snapshot, got %q", restored)
	}
	wantErr(t, session.Finish(), ErrSessionClosed)
}

func TestWritePayloadAndPathValidation(t *testing.T) {
	_, guard, info := trustedSetup()
	session := guard.BeginSession(info)

	wantErr(t, session.Write("/", ResourceSet{}), ErrInvalidPath)
	wantErr(t, session.Write("/0/1", ObjectWrite{}), ErrInvalidPayload)
	wantErr(t, session.Write("/0", ResourceSet{}), ErrInvalidPayload)
	wantErr(t, session.Write("/nope/1", nil), ErrInvalidPath)
	wantErr(t, session.Write("/0/1/2/3", nil), ErrInvalidPath)

	must(t, session.Write("/0/3", ResourceSet{
		ResServerURI:       "coaps://mgmt3.example.org:5684",
		ResBootstrapServer: false,
		ResSecurityMode:    SecModePSK,
	}))
	must(t, session.Write("/0/3/0", "coaps://renamed.example.org:5684"))
	uri, _ := guard.store.GetResource(ObjectSecurity, 3, ResServerURI)
	if uri != "coaps://renamed.example.org:5684" {
		t.Fatalf("resource write not applied, got %v", uri)
	}

	must(t, session.Write("/0", ObjectWrite{
		4: {
			ResServerURI:       "coaps://mgmt4.example.org:5684",
			ResBootstrapServer: false,
			ResSecurityMode:    SecModeCertificate,
		},
	}))
	if !slices.Contains(guard.store.Instances(ObjectSecurity), 4) {
		t.Fatal("object-level write did not create instance 4")
	}
}

func TestTrustAnchorsFromStoreSkipsNoSec(t *testing.T) {
	store := NewStore()
	store.SetResource(ObjectSecurity, 0, ResBootstrapServer, true)
	store.SetResource(ObjectSecurity, 0, ResSecurityMode, SecModeNoSec)
	store.SetResource(ObjectSecurity, 0, ResServerURI, bootstrapURI)

	if anchors := TrustAnchorsFromStore(store); len(anchors) != 0 {
		t.Fatalf("NoSec bootstrap account must not become a trust anchor, got %+v", anchors)
	}
}
