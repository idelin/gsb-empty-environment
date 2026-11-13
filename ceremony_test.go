package netsec

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Now()}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type testAdmin struct {
	credential Credential
	privateKey ed25519.PrivateKey
}

type fakeHSM struct {
	mu sync.Mutex

	receiptPrivateKey ed25519.PrivateKey
	importKeys        map[string][]byte
	usedKeys          map[string]bool
	results           map[string]ImportResult

	combineCalls int
	lastOrder    []string

	dropResponse bool
	readFailures int
	receiptSlot  string

	combineStarted chan struct{}
	combineRelease chan struct{}
}

func newFakeHSM() (*fakeHSM, ed25519.PublicKey) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return &fakeHSM{
		receiptPrivateKey: privateKey,
		importKeys:        make(map[string][]byte),
		usedKeys:          make(map[string]bool),
		results:           make(map[string]ImportResult),
	}, publicKey
}

func (h *fakeHSM) CreateImportKey(_ context.Context, ceremonyID string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.importKeys[ceremonyID]; exists {
		return nil, errors.New("duplicate import key request")
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	h.importKeys[ceremonyID] = append([]byte(nil), privateKey.Bytes()...)
	return append([]byte(nil), privateKey.PublicKey().Bytes()...), nil
}

func (h *fakeHSM) CombineAndImport(ctx context.Context, request ImportRequest) (ImportResult, error) {
	h.mu.Lock()
	h.combineCalls++
	ceremonyID := request.Binding.CeremonyID
	if h.usedKeys[ceremonyID] {
		h.mu.Unlock()
		return ImportResult{}, errors.New("one-time import key already consumed")
	}
	h.usedKeys[ceremonyID] = true
	privateKey := append([]byte(nil), h.importKeys[ceremonyID]...)
	h.lastOrder = make([]string, 0, len(request.WrappedShares))
	for _, share := range request.WrappedShares {
		h.lastOrder = append(h.lastOrder, share.PersonID)
	}
	started := h.combineStarted
	release := h.combineRelease
	h.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return ImportResult{}, ctx.Err()
		case <-release:
		}
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}

	var material [32]byte
	for _, wrapped := range request.WrappedShares {
		share, err := OpenWrappedShare(request.Binding, privateKey, wrapped)
		if err != nil {
			return ImportResult{}, err
		}
		for i := range material {
			material[i] ^= share[i]
		}
		zeroBytes(share[:])
	}
	commitment := sha256.Sum256(material[:])
	zeroBytes(material[:])

	keyHandle := "hsm-key-" + ceremonyID
	attributes := AttributesDigest(request.Binding)
	slot := request.Binding.Slot
	h.mu.Lock()
	if h.receiptSlot != "" {
		slot = h.receiptSlot
	}
	h.mu.Unlock()
	receipt := Receipt{
		CeremonyID:            request.Binding.CeremonyID,
		Slot:                  slot,
		Algorithm:             request.Binding.Algorithm,
		Purpose:               request.Binding.Purpose,
		KeyMaterialCommitment: commitment,
		WrappingKeyDigest:     request.Binding.WrappingKeyDigest,
		AttributesDigest:      attributes,
		KeyHandle:             keyHandle,
		IssuedAtUnixNano:      1_700_000_000_000_000_000,
	}
	receipt.Signature = ed25519.Sign(h.receiptPrivateKey, ReceiptSigningPayload(receipt))
	result := ImportResult{
		KeyHandle:             keyHandle,
		AttributesDigest:      attributes,
		KeyMaterialCommitment: commitment,
		Receipt:               receipt,
	}

	h.mu.Lock()
	h.results[ceremonyID] = result
	drop := h.dropResponse
	if drop {
		h.dropResponse = false
	}
	h.mu.Unlock()
	if drop {
		return ImportResult{}, errors.New("simulated response loss after HSM commit")
	}
	return result, nil
}

func (h *fakeHSM) ReadReceipt(_ context.Context, ceremonyID string) (ImportResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.readFailures > 0 {
		h.readFailures--
		return ImportResult{}, errors.New("simulated receipt read failure")
	}
	result, ok := h.results[ceremonyID]
	if !ok {
		return ImportResult{}, errors.New("receipt absent")
	}
	result.Receipt.Signature = append([]byte(nil), result.Receipt.Signature...)
	return result, nil
}

func (h *fakeHSM) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.combineCalls
}

func (h *fakeHSM) order() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lastOrder...)
}

type fixture struct {
	registry *Registry
	rootKey  ed25519.PrivateKey
	clock    *testClock
	hsm      *fakeHSM
	host     *Host
}

func newFixture(t *testing.T, policy Policy) *fixture {
	t.Helper()
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(rootPublic, policy)
	if err != nil {
		t.Fatal(err)
	}
	hsm, receiptPublic := newFakeHSM()
	clock := newTestClock()
	host, err := NewHost(registry, hsm, receiptPublic, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	return &fixture{
		registry: registry,
		rootKey:  rootPrivate,
		clock:    clock,
		hsm:      hsm,
		host:     host,
	}
}

func (f *fixture) addAdmin(t *testing.T, credentialID, personID string, role Role) *testAdmin {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{
		CredentialID: credentialID,
		PersonID:     personID,
		PublicKey:    publicKey,
		Role:         role,
	}
	signed, err := SignCredential(f.rootKey, credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Register(signed); err != nil {
		t.Fatalf("register %s: %v", credentialID, err)
	}
	return &testAdmin{credential: credential, privateKey: privateKey}
}

func (f *fixture) begin(t *testing.T, commitment [32]byte) *Ceremony {
	t.Helper()
	ceremony, err := f.host.BeginCeremony(context.Background(), CeremonySpec{
		Slot:                  "slot-7",
		Algorithm:             "AES-256-GCM",
		Purpose:               "root-key",
		KeyMaterialCommitment: commitment,
		Validity:              10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ceremony
}

func approve(t *testing.T, ceremony *Ceremony, admin *testAdmin) {
	t.Helper()
	approval, err := SignApproval(
		ceremony.Binding(),
		admin.credential.CredentialID,
		admin.credential.PersonID,
		admin.privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ceremony.Approve(context.Background(), approval); err != nil {
		t.Fatalf("approve %s: %v", admin.credential.CredentialID, err)
	}
}

func submitShare(t *testing.T, ceremony *Ceremony, admin *testAdmin, share [32]byte) {
	t.Helper()
	envelope, err := SealShare(
		ceremony.Binding(),
		ceremony.WrappingPublicKey(),
		admin.credential.CredentialID,
		admin.credential.PersonID,
		admin.privateKey,
		share,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ceremony.SubmitShare(context.Background(), *envelope); err != nil {
		t.Fatalf("submit share for %s: %v", admin.credential.CredentialID, err)
	}
}

func sharesByPerson(t *testing.T, admins ...*testAdmin) map[string][32]byte {
	t.Helper()
	shares := make(map[string][32]byte)
	for _, admin := range admins {
		if _, exists := shares[admin.credential.PersonID]; exists {
			continue
		}
		share, err := GenerateShare()
		if err != nil {
			t.Fatal(err)
		}
		shares[admin.credential.PersonID] = share
	}
	return shares
}

func commitmentForShares(t *testing.T, shares map[string][32]byte) [32]byte {
	t.Helper()
	people := make([]string, 0, len(shares))
	for personID := range shares {
		people = append(people, personID)
	}
	sort.Strings(people)
	var material [32]byte
	for _, personID := range people {
		for i := range material {
			material[i] ^= shares[personID][i]
		}
	}
	commitment := sha256.Sum256(material[:])
	return commitment
}

func prepareFullCeremony(t *testing.T, admins ...*testAdmin) (*fixture, *Ceremony, []*testAdmin) {
	t.Helper()
	policy := Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	}
	f := newFixture(t, policy)
	registered := make([]*testAdmin, 0, len(admins))
	for _, template := range admins {
		registered = append(registered, f.addAdmin(t, template.credential.CredentialID, template.credential.PersonID, template.credential.Role))
	}
	shares := sharesByPerson(t, registered...)
	ceremony := f.begin(t, commitmentForShares(t, shares))
	for _, admin := range registered {
		approve(t, ceremony, admin)
	}
	for i := len(registered) - 1; i >= 0; i-- {
		admin := registered[i]
		submitShare(t, ceremony, admin, shares[admin.credential.PersonID])
	}
	return f, ceremony, registered
}

func TestAliasCredentialsForSamePersonGetOneVote(t *testing.T) {
	f := newFixture(t, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	})
	first := f.addAdmin(t, "alice-primary", "alice", RoleSecurityOfficer)
	alias := f.addAdmin(t, "alice-alias", "alice", RoleKeyCustodian)
	ceremony := f.begin(t, [32]byte{})

	approve(t, ceremony, first)
	approval, err := SignApproval(
		ceremony.Binding(),
		alias.credential.CredentialID,
		alias.credential.PersonID,
		alias.privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ceremony.Approve(context.Background(), approval); !errors.Is(err, ErrDuplicatePerson) {
		t.Fatalf("alias approval error = %v, want ErrDuplicatePerson", err)
	}
	if count := ceremony.Status().ApprovalCount; count != 1 {
		t.Fatalf("approval count = %d, want 1", count)
	}
	if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("complete error = %v, want ErrNotReady", err)
	}
	if f.hsm.calls() != 0 {
		t.Fatalf("HSM was called with only one person")
	}
}

func TestApprovalCannotBeReplayedIntoNewCeremony(t *testing.T) {
	f := newFixture(t, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	})
	admin := f.addAdmin(t, "alice-primary", "alice", RoleSecurityOfficer)
	oldCeremony := f.begin(t, [32]byte{1})
	newCeremony := f.begin(t, [32]byte{2})

	oldApproval, err := SignApproval(
		oldCeremony.Binding(),
		admin.credential.CredentialID,
		admin.credential.PersonID,
		admin.privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := newCeremony.Approve(context.Background(), oldApproval); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("replayed approval error = %v, want ErrInvalidSignature", err)
	}
}

func TestShareCannotBeTransplantedToAnotherCeremony(t *testing.T) {
	f := newFixture(t, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	})
	admin := f.addAdmin(t, "alice-primary", "alice", RoleSecurityOfficer)
	first := f.begin(t, [32]byte{1})
	second := f.begin(t, [32]byte{2})
	share, err := GenerateShare()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := SealShare(
		first.Binding(),
		first.WrappingPublicKey(),
		admin.credential.CredentialID,
		admin.credential.PersonID,
		admin.privateKey,
		share,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.SubmitShare(context.Background(), *envelope); !errors.Is(err, ErrEnvelopeMismatch) {
		t.Fatalf("transplanted share error = %v, want ErrEnvelopeMismatch", err)
	}

	f.hsm.mu.Lock()
	firstImportPrivateKey := append([]byte(nil), f.hsm.importKeys[first.Binding().CeremonyID]...)
	f.hsm.mu.Unlock()
	if _, err := OpenWrappedShare(second.Binding(), firstImportPrivateKey, envelope.wrapped()); !errors.Is(err, ErrEnvelopeMismatch) {
		t.Fatalf("re-bound ciphertext error = %v, want ErrEnvelopeMismatch", err)
	}
}

func TestRequiredRoleMustParticipate(t *testing.T) {
	f := newFixture(t, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
		RequiredRoles:  []Role{RoleSecurityOfficer, RoleKeyCustodian},
	})
	alice := f.addAdmin(t, "alice-so", "alice", RoleSecurityOfficer)
	bob := f.addAdmin(t, "bob-so", "bob", RoleSecurityOfficer)
	ceremony := f.begin(t, [32]byte{})
	approve(t, ceremony, alice)
	approve(t, ceremony, bob)
	shares := sharesByPerson(t, alice, bob)
	submitShare(t, ceremony, alice, shares["alice"])
	submitShare(t, ceremony, bob, shares["bob"])

	if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrMissingRequiredRole) {
		t.Fatalf("complete error = %v, want ErrMissingRequiredRole", err)
	}
	if f.hsm.calls() != 0 {
		t.Fatalf("HSM was called without the required KeyCustodian role")
	}
}

func TestUnapprovedShareCannotInflateShareThreshold(t *testing.T) {
	f := newFixture(t, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 3,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	})
	alice := f.addAdmin(t, "alice-kc", "alice", RoleKeyCustodian)
	bob := f.addAdmin(t, "bob-so", "bob", RoleSecurityOfficer)
	charlie := f.addAdmin(t, "charlie-so", "charlie", RoleSecurityOfficer)
	ceremony := f.begin(t, [32]byte{})
	approve(t, ceremony, alice)
	approve(t, ceremony, bob)
	shares := sharesByPerson(t, alice, bob, charlie)
	submitShare(t, ceremony, alice, shares["alice"])
	submitShare(t, ceremony, bob, shares["bob"])
	submitShare(t, ceremony, charlie, shares["charlie"])

	if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("complete error = %v, want ErrNotReady", err)
	}
	if f.hsm.calls() != 0 {
		t.Fatal("HSM was called with fewer than the policy share threshold")
	}
}

func TestConcurrentCompleteCallsHSMExactlyOnce(t *testing.T) {
	policy := Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	}
	f := newFixture(t, policy)
	zoe := f.addAdmin(t, "zoe-so", "zoe", RoleSecurityOfficer)
	alice := f.addAdmin(t, "alice-kc", "alice", RoleKeyCustodian)
	shares := sharesByPerson(t, zoe, alice)
	ceremony := f.begin(t, commitmentForShares(t, shares))
	approve(t, ceremony, zoe)
	approve(t, ceremony, alice)
	submitShare(t, ceremony, alice, shares["alice"])
	submitShare(t, ceremony, zoe, shares["zoe"])

	started := make(chan struct{}, 10)
	release := make(chan struct{})
	f.hsm.mu.Lock()
	f.hsm.combineStarted = started
	f.hsm.combineRelease = release
	f.hsm.mu.Unlock()

	const callers = 24
	results := make(chan *ImportResult, callers)
	failures := make(chan error, callers)
	var wg sync.WaitGroup
	startBarrier := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBarrier
			result, err := ceremony.Complete(context.Background())
			if err != nil {
				failures <- err
				return
			}
			results <- result
		}()
	}
	close(startBarrier)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("HSM import did not start")
	}
	close(release)
	wg.Wait()
	close(results)
	close(failures)

	for err := range failures {
		t.Fatalf("concurrent complete: %v", err)
	}
	var first *ImportResult
	count := 0
	for result := range results {
		count++
		if result == nil {
			t.Fatal("nil import result")
		}
		if first == nil {
			first = result
		} else if result.KeyHandle != first.KeyHandle ||
			!bytes.Equal(result.Receipt.Signature, first.Receipt.Signature) {
			t.Fatal("concurrent callers observed different HSM results")
		}
		if result.KeyHandle != "hsm-key-"+ceremony.Binding().CeremonyID {
			t.Fatalf("key handle = %q", result.KeyHandle)
		}
	}
	if count != callers {
		t.Fatalf("successful callers = %d, want %d", count, callers)
	}
	if f.hsm.calls() != 1 {
		t.Fatalf("HSM CombineAndImport calls = %d, want 1", f.hsm.calls())
	}
	order := f.hsm.order()
	if len(order) != 2 || order[0] != "alice" || order[1] != "zoe" {
		t.Fatalf("share order = %v, want [alice zoe]", order)
	}
}

func TestLostImportResponseIsRecoveredByCeremonyID(t *testing.T) {
	f, ceremony, _ := prepareFullCeremony(
		t,
		&testAdmin{credential: Credential{CredentialID: "alice-kc", PersonID: "alice", Role: RoleKeyCustodian}},
		&testAdmin{credential: Credential{CredentialID: "bob-so", PersonID: "bob", Role: RoleSecurityOfficer}},
	)
	f.hsm.mu.Lock()
	f.hsm.dropResponse = true
	f.hsm.readFailures = 1
	f.hsm.mu.Unlock()

	result, err := ceremony.Complete(context.Background())
	if result != nil || !errors.Is(err, ErrImportUncertain) {
		t.Fatalf("complete = (%v, %v), want ErrImportUncertain", result, err)
	}
	if status := ceremony.Status(); status.State != StateFailed {
		t.Fatalf("state after lost response = %s, want failed", status.State)
	}

	recovered, err := ceremony.ReadReceipt(context.Background())
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if recovered.KeyHandle == "" {
		t.Fatal("recovered receipt has no key handle")
	}
	if status := ceremony.Status(); status.State != StateCompleted {
		t.Fatalf("state after receipt read = %s, want completed", status.State)
	}
	again, err := ceremony.Complete(context.Background())
	if err != nil {
		t.Fatalf("complete after recovery: %v", err)
	}
	if again.KeyHandle != recovered.KeyHandle ||
		!bytes.Equal(again.Receipt.Signature, recovered.Receipt.Signature) {
		t.Fatal("ceremony returned a different receipt after recovery")
	}
	if f.hsm.calls() != 1 {
		t.Fatalf("HSM CombineAndImport calls = %d, want 1", f.hsm.calls())
	}
}

func TestLostReceiptCannotBypassLaterTermination(t *testing.T) {
	templates := []*testAdmin{
		{credential: Credential{CredentialID: "alice-kc", PersonID: "alice", Role: RoleKeyCustodian}},
		{credential: Credential{CredentialID: "bob-so", PersonID: "bob", Role: RoleSecurityOfficer}},
	}
	loseResponse := func(t *testing.T) (*fixture, *Ceremony, []*testAdmin) {
		t.Helper()
		f, ceremony, admins := prepareFullCeremony(t, templates...)
		f.hsm.mu.Lock()
		f.hsm.dropResponse = true
		f.hsm.readFailures = 1
		f.hsm.mu.Unlock()
		if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrImportUncertain) {
			t.Fatalf("complete error = %v, want ErrImportUncertain", err)
		}
		return f, ceremony, admins
	}

	t.Run("revocation", func(t *testing.T) {
		f, ceremony, admins := loseResponse(t)
		if err := f.registry.RevokeCredential(admins[0].credential.CredentialID); err != nil {
			t.Fatal(err)
		}
		if _, err := ceremony.ReadReceipt(context.Background()); !errors.Is(err, ErrCredentialRevoked) {
			t.Fatalf("read receipt error = %v, want ErrCredentialRevoked", err)
		}
	})

	t.Run("policy change", func(t *testing.T) {
		f, ceremony, _ := loseResponse(t)
		if err := f.registry.SetPolicy(Policy{
			Version:        2,
			Threshold:      2,
			ShareThreshold: 2,
			AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ceremony.ReadReceipt(context.Background()); !errors.Is(err, ErrPolicyChanged) {
			t.Fatalf("read receipt error = %v, want ErrPolicyChanged", err)
		}
	})

	t.Run("expiry", func(t *testing.T) {
		f, ceremony, _ := loseResponse(t)
		f.clock.Advance(11 * time.Minute)
		if _, err := ceremony.ReadReceipt(context.Background()); !errors.Is(err, ErrCeremonyExpired) {
			t.Fatalf("read receipt error = %v, want ErrCeremonyExpired", err)
		}
	})
}

func TestMismatchedReceiptSlotNeverCompletes(t *testing.T) {
	f, ceremony, _ := prepareFullCeremony(
		t,
		&testAdmin{credential: Credential{CredentialID: "alice-kc", PersonID: "alice", Role: RoleKeyCustodian}},
		&testAdmin{credential: Credential{CredentialID: "bob-so", PersonID: "bob", Role: RoleSecurityOfficer}},
	)
	f.hsm.mu.Lock()
	f.hsm.receiptSlot = "slot-attacker"
	f.hsm.mu.Unlock()

	if _, err := ceremony.Complete(context.Background()); err == nil {
		t.Fatal("ceremony completed with a receipt for the wrong slot")
	}
	if status := ceremony.Status(); status.State != StateFailed {
		t.Fatalf("state = %s, want failed", status.State)
	}
}

func TestPolicyChangeDuringHSMImportPreventsCompletion(t *testing.T) {
	f, ceremony, _ := prepareFullCeremony(
		t,
		&testAdmin{credential: Credential{CredentialID: "alice-kc", PersonID: "alice", Role: RoleKeyCustodian}},
		&testAdmin{credential: Credential{CredentialID: "bob-so", PersonID: "bob", Role: RoleSecurityOfficer}},
	)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	f.hsm.mu.Lock()
	f.hsm.combineStarted = started
	f.hsm.combineRelease = release
	f.hsm.mu.Unlock()

	result := make(chan error, 1)
	go func() {
		_, err := ceremony.Complete(context.Background())
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("HSM import did not start")
	}
	if err := f.registry.SetPolicy(Policy{
		Version:        2,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrPolicyChanged) {
		t.Fatalf("complete error = %v, want ErrPolicyChanged", err)
	}
	if f.hsm.calls() != 1 {
		t.Fatalf("HSM calls = %d, want 1", f.hsm.calls())
	}
	if status := ceremony.Status(); status.State != StateFailed {
		t.Fatalf("state = %s, want failed", status.State)
	}
	if len(ceremony.shares) != 0 {
		t.Fatal("wrapped shares were not destroyed")
	}
}

func TestCeremonyExpiryRevocationAndPolicyChangeDestroyShares(t *testing.T) {
	templates := []*testAdmin{
		{credential: Credential{CredentialID: "alice-kc", PersonID: "alice", Role: RoleKeyCustodian}},
		{credential: Credential{CredentialID: "bob-so", PersonID: "bob", Role: RoleSecurityOfficer}},
	}

	t.Run("expiry", func(t *testing.T) {
		f, ceremony, _ := prepareFullCeremony(t, templates...)
		f.clock.Advance(11 * time.Minute)
		if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrCeremonyExpired) {
			t.Fatalf("complete error = %v, want ErrCeremonyExpired", err)
		}
		if len(ceremony.shares) != 0 {
			t.Fatal("wrapped shares were not destroyed on expiry")
		}
	})

	t.Run("revocation", func(t *testing.T) {
		f, ceremony, admins := prepareFullCeremony(t, templates...)
		if err := f.registry.RevokeCredential(admins[0].credential.CredentialID); err != nil {
			t.Fatal(err)
		}
		if status := ceremony.Status(); status.State != StateFailed {
			t.Fatalf("state after revocation = %s, want failed", status.State)
		}
		if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrCredentialRevoked) {
			t.Fatalf("complete error = %v, want ErrCredentialRevoked", err)
		}
		if len(ceremony.shares) != 0 {
			t.Fatal("wrapped shares were not destroyed on revocation")
		}
	})

	t.Run("policy change", func(t *testing.T) {
		f, ceremony, _ := prepareFullCeremony(t, templates...)
		if err := f.registry.SetPolicy(Policy{
			Version:        2,
			Threshold:      2,
			ShareThreshold: 2,
			AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
		}); err != nil {
			t.Fatal(err)
		}
		if status := ceremony.Status(); status.State != StateFailed {
			t.Fatalf("state after policy change = %s, want failed", status.State)
		}
		if _, err := ceremony.Complete(context.Background()); !errors.Is(err, ErrPolicyChanged) {
			t.Fatalf("complete error = %v, want ErrPolicyChanged", err)
		}
		if len(ceremony.shares) != 0 {
			t.Fatal("wrapped shares were not destroyed on policy change")
		}
	})
}

func TestHRRootRejectsTamperedOrReboundRegistration(t *testing.T) {
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	adminPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(rootPublic, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{CredentialID: "alice", PersonID: "alice", PublicKey: adminPublic, Role: RoleSecurityOfficer}
	signed, err := SignCredential(rootPrivate, credential)
	if err != nil {
		t.Fatal(err)
	}
	tampered := signed
	tampered.PersonID = "mallory"
	if err := registry.Register(tampered); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("tampered registration error = %v, want ErrInvalidRegistration", err)
	}
	if err := registry.Register(signed); err != nil {
		t.Fatal(err)
	}
	rebound, err := SignCredential(rootPrivate, Credential{
		CredentialID: "alice",
		PersonID:     "mallory",
		PublicKey:    adminPublic,
		Role:         RoleSecurityOfficer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(rebound); !errors.Is(err, ErrPersonIDImmutable) {
		t.Fatalf("rebound credential error = %v, want ErrPersonIDImmutable", err)
	}
}

func TestPolicyRequiresAtLeastTwoApprovalsAndShares(t *testing.T) {
	rootPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(rootPublic, Policy{
		Version:        1,
		Threshold:      1,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer},
	}); !errors.Is(err, ErrWeakPolicy) {
		t.Fatalf("one-approval policy error = %v, want ErrWeakPolicy", err)
	}
	if _, err := NewRegistry(rootPublic, Policy{
		Version:        1,
		Threshold:      2,
		ShareThreshold: 1,
		AllowedRoles:   []Role{RoleSecurityOfficer},
	}); !errors.Is(err, ErrWeakPolicy) {
		t.Fatalf("one-share policy error = %v, want ErrWeakPolicy", err)
	}
}
