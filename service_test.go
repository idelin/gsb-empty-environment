package netsec

import (
	"errors"
	"sync/atomic"
	"testing"
)

const (
	testServiceSID  = LocalSystemSID
	testAttackerSID = "S-1-5-21-1001"
)

var (
	testServiceIdentity                     = Creator{SID: testServiceSID, Integrity: IntegritySystem}
	testAttackerIdentity                    = Creator{SID: testAttackerSID, Integrity: IntegrityLow}
	testServiceSD        SecurityDescriptor = "D:(A;;GA;;;SY)"
	testAttackerSD       SecurityDescriptor = "D:(A;;GA;;;WD)"
)

func mustPolicy(t *testing.T, name Name, allowed []string, min IntegrityLevel, sd SecurityDescriptor) Policy {
	t.Helper()
	p, err := NewPolicy(name, allowed, min, sd)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newServiceWithPolicy(t *testing.T, ns *Namespace, identity Creator, policy Policy) *Service {
	t.Helper()
	s := NewService(ns, identity)
	if err := s.RegisterPolicy(policy); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAttackerPreoccupationIsNotExecutedOrOverwritten(t *testing.T) {
	ns := NewNamespace()
	scope := SessionScope("s1")
	name := MustName(scope, `svc\global-mutex`, ObjectTypeMutex)

	attacker, err := ns.CreateIfAbsent(CreateRequest{
		Name:               name,
		Creator:            testAttackerIdentity,
		SecurityDescriptor: testAttackerSD,
	})
	if err != nil {
		t.Fatal(err)
	}

	policy := mustPolicy(t, name, []string{testServiceSID}, IntegrityHigh, testServiceSD)
	service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)

	if _, err := service.OpenExisting(name); !errors.Is(err, ErrCreatorDenied) {
		t.Fatalf("OpenExisting error = %v, want creator denial", err)
	}
	if _, err := service.CreateNew(name); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateNew error = %v, want already-exists", err)
	}

	current, ok := ns.Lookup(scope, name.Text)
	if !ok {
		t.Fatal("attacker object was removed")
	}
	if current.ObjectID != attacker.ObjectID ||
		current.CreatorSID != testAttackerSID ||
		current.SecurityDescriptor != testAttackerSD {
		t.Fatalf("service overwrote attacker metadata: %#v", current)
	}

	var callbacks atomic.Int32
	spy := func(VerifiedHandle) error {
		callbacks.Add(1)
		return nil
	}
	if err := service.Lock(name, spy); !errors.Is(err, ErrNotBound) {
		t.Fatalf("Lock error = %v, want not-bound", err)
	}
	if err := service.Map(name, spy); !errors.Is(err, ErrTypeMismatch) && !errors.Is(err, ErrNotBound) {
		t.Fatalf("Map error = %v", err)
	}
	if callbacks.Load() != 0 {
		t.Fatalf("Lock/Map callback ran %d times against attacker object", callbacks.Load())
	}
}

func TestTypeConfusionDoesNotFallBackToCreate(t *testing.T) {
	ns := NewNamespace()
	scope := SessionScope("s1")
	expected := MustName(scope, `svc\shared-state`, ObjectTypeMutex)
	confused := MustName(scope, `svc\shared-state`, ObjectTypeSection)

	attacker, err := ns.CreateIfAbsent(CreateRequest{
		Name:               confused,
		Creator:            testAttackerIdentity,
		SecurityDescriptor: testAttackerSD,
	})
	if err != nil {
		t.Fatal(err)
	}

	policy := mustPolicy(t, expected, []string{testServiceSID}, IntegrityHigh, testServiceSD)
	service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)

	if _, err := service.OpenExisting(expected); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("OpenExisting error = %v, want type mismatch", err)
	}
	_, err = service.CreateNew(expected)
	if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("CreateNew error = %v, want occupied type mismatch", err)
	}
	current, ok := ns.Lookup(scope, expected.Text)
	if !ok || current.Type != ObjectTypeSection || current.ObjectID != attacker.ObjectID {
		t.Fatalf("type-confused object changed: %#v ok=%v", current, ok)
	}

	var callbacks atomic.Int32
	spy := func(VerifiedHandle) error {
		callbacks.Add(1)
		return nil
	}
	if err := service.Lock(expected, spy); !errors.Is(err, ErrNotBound) {
		t.Fatalf("Lock error = %v", err)
	}
	if callbacks.Load() != 0 {
		t.Fatalf("mutex callback ran against attacker section")
	}
}

func TestCaseCollisionPolicyAndCreate(t *testing.T) {
	ns := NewNamespace()
	scope := SessionScope("s1")
	expected := MustName(scope, `Svc\State`, ObjectTypeMutex)
	attackerName := MustName(scope, `SVC\state`, ObjectTypeMutex)

	if _, err := ns.CreateIfAbsent(CreateRequest{
		Name:               attackerName,
		Creator:            testAttackerIdentity,
		SecurityDescriptor: testAttackerSD,
	}); err != nil {
		t.Fatal(err)
	}

	policy := mustPolicy(t, expected, []string{testServiceSID}, IntegrityHigh, testServiceSD)
	service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)
	if _, err := service.OpenExisting(expected); !errors.Is(err, ErrCreatorDenied) {
		t.Fatalf("OpenExisting error = %v", err)
	}
	if _, err := service.CreateNew(expected); !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, ErrCaseCollision) {
		t.Fatalf("CreateNew error = %v, want case collision", err)
	}

	// Policies themselves must not register two case-folded spellings.
	other := mustPolicy(t, MustName(scope, `SVC\STATE`, ObjectTypeSection), []string{testServiceSID}, IntegrityHigh, testServiceSD)
	if err := service.RegisterPolicy(other); !errors.Is(err, ErrPolicyExists) || !errors.Is(err, ErrCaseCollision) {
		t.Fatalf("duplicate policy error = %v", err)
	}
}

func TestDeleteRecreateStaleHandleAndGeneration(t *testing.T) {
	ns := NewNamespace()
	scope := SessionScope("s1")
	name := MustName(scope, `svc\gate`, ObjectTypeMutex)
	policy := mustPolicy(t, name, []string{testServiceSID}, IntegrityHigh, testServiceSD)
	service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)

	first, err := service.CreateNew(name)
	if err != nil {
		t.Fatal(err)
	}
	oldHandle, err := ns.OpenExisting(name)
	if err != nil {
		t.Fatal(err)
	}
	var firstCallbacks atomic.Int32
	if err := first.Lock(func(h VerifiedHandle) error {
		firstCallbacks.Add(1)
		if h.ObjectID != oldHandle.ObjectID || h.Generation != oldHandle.Generation {
			t.Fatalf("callback got wrong handle: %#v", h)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if firstCallbacks.Load() != 1 {
		t.Fatalf("first callback count = %d", firstCallbacks.Load())
	}

	if err := ns.Delete(name); err != nil {
		t.Fatal(err)
	}
	if err := first.Lock(func(VerifiedHandle) error {
		t.Fatal("callback ran after delete")
		return nil
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("use after delete = %v", err)
	}

	recreated, err := ns.CreateIfAbsent(CreateRequest{
		Name:               name,
		Creator:            testServiceIdentity,
		SecurityDescriptor: testServiceSD,
		Nonce:              [16]byte{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recreated.Generation <= oldHandle.Generation {
		t.Fatalf("generation did not advance: old=%d new=%d", oldHandle.Generation, recreated.Generation)
	}
	if recreated.ObjectID == oldHandle.ObjectID {
		t.Fatal("recreated object reused object ID")
	}

	var staleCallbacks atomic.Int32
	err = first.Lock(func(VerifiedHandle) error {
		staleCallbacks.Add(1)
		return nil
	})
	if !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("stale use = %v, want stale handle", err)
	}
	if staleCallbacks.Load() != 0 {
		t.Fatal("host callback ran for stale generation")
	}

	second, err := service.OpenExisting(name)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation() != recreated.Generation || second.ObjectID() != recreated.ObjectID {
		t.Fatalf("new binding does not match recreated object")
	}
	if err := second.Lock(func(VerifiedHandle) error { return nil }); err != nil {
		t.Fatalf("new binding use = %v", err)
	}

	// A stale DeleteIf must not remove the replacement object.
	if err := ns.DeleteIf(oldHandle); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("DeleteIf stale error = %v", err)
	}
	if current, ok := ns.Lookup(scope, name.Text); !ok || current.ObjectID != recreated.ObjectID {
		t.Fatalf("replacement was removed by stale handle: %#v ok=%v", current, ok)
	}
}

func TestUseRevalidatesTypeAndDescriptorAfterRecreation(t *testing.T) {
	tests := []struct {
		name     string
		recreate Name
		sd       SecurityDescriptor
		want     error
	}{
		{
			name:     "type changed",
			recreate: MustName(SessionScope("s1"), `svc\gate`, ObjectTypeSection),
			sd:       testAttackerSD,
			want:     ErrTypeMismatch,
		},
		{
			name:     "descriptor changed",
			recreate: MustName(SessionScope("s1"), `svc\gate`, ObjectTypeMutex),
			sd:       testAttackerSD,
			want:     ErrDescriptorMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns := NewNamespace()
			name := MustName(SessionScope("s1"), `svc\gate`, ObjectTypeMutex)
			policy := mustPolicy(t, name, []string{testServiceSID}, IntegrityHigh, testServiceSD)
			service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)
			bound, err := service.CreateNew(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := ns.Delete(name); err != nil {
				t.Fatal(err)
			}
			if _, err := ns.CreateIfAbsent(CreateRequest{
				Name: tc.recreate, Creator: testServiceIdentity, SecurityDescriptor: tc.sd,
			}); err != nil {
				t.Fatal(err)
			}

			var callbacks atomic.Int32
			if err := bound.Use(func(VerifiedHandle) error {
				callbacks.Add(1)
				return nil
			}); !errors.Is(err, tc.want) {
				t.Fatalf("Use error = %v, want %v", err, tc.want)
			}
			if callbacks.Load() != 0 {
				t.Fatal("callback ran for a replacement that failed policy revalidation")
			}
			if _, err := service.OpenExisting(name); !errors.Is(err, tc.want) {
				t.Fatalf("OpenExisting = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestGlobalLowPrivilegeAlwaysDenied(t *testing.T) {
	ns := NewNamespace()
	name := MustName(GlobalScope(), `svc\shared-state`, ObjectTypeSection)

	// The policy is deliberately permissive: the attacker's SID is allowed and
	// the minimum is Low. The Global rule must still reject the creator.
	policy := mustPolicy(t, name, []string{testServiceSID, testAttackerSID}, IntegrityLow, testAttackerSD)
	service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)

	if _, err := ns.CreateIfAbsent(CreateRequest{
		Name:               name,
		Creator:            Creator{SID: testAttackerSID, Integrity: IntegrityLow},
		SecurityDescriptor: testAttackerSD,
	}); err != nil {
		t.Fatal(err)
	}
	var callbacks atomic.Int32
	if _, err := service.OpenExisting(name); !errors.Is(err, ErrLowPrivilegeGlobal) {
		t.Fatalf("OpenExisting low Global = %v", err)
	}
	if err := service.Map(name, func(VerifiedHandle) error {
		callbacks.Add(1)
		return nil
	}); !errors.Is(err, ErrNotBound) {
		t.Fatalf("Map = %v", err)
	}
	if callbacks.Load() != 0 {
		t.Fatal("Map callback ran for low-privilege Global object")
	}

	// Even a medium-integrity creator is too low for a cross-session object.
	for _, level := range []IntegrityLevel{IntegrityUntrusted, IntegrityLow, IntegrityMedium} {
		lowService := newServiceWithPolicy(t, ns, Creator{SID: testServiceSID, Integrity: level},
			mustPolicy(t, MustName(GlobalScope(), `svc\other-`+level.String(), ObjectTypeMutex),
				[]string{testServiceSID}, IntegrityLow, testServiceSD))
		n := MustName(GlobalScope(), `svc\other-`+level.String(), ObjectTypeMutex)
		if _, err := lowService.CreateNew(n); !errors.Is(err, ErrLowPrivilegeGlobal) {
			t.Fatalf("%s CreateNew error = %v", level, err)
		}
		if _, ok := ns.Lookup(n.Scope, n.Text); ok {
			t.Fatalf("%s creator created a Global object", level)
		}
	}
}

func TestPolicyVerificationEachItem(t *testing.T) {
	ns := NewNamespace()
	scope := SessionScope("s1")
	expected := MustName(scope, `svc\checked`, ObjectTypeMutex)
	policy := mustPolicy(t, expected, []string{testServiceSID}, IntegrityHigh, testServiceSD)
	service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)

	tests := []struct {
		name    string
		request CreateRequest
		want    error
	}{
		{
			name: "type",
			request: CreateRequest{
				Name:    MustName(scope, expected.Text, ObjectTypeSection),
				Creator: testServiceIdentity, SecurityDescriptor: testServiceSD,
			},
			want: ErrTypeMismatch,
		},
		{
			name: "sid",
			request: CreateRequest{
				Name: expected, Creator: Creator{SID: testAttackerSID, Integrity: IntegritySystem},
				SecurityDescriptor: testServiceSD,
			},
			want: ErrCreatorDenied,
		},
		{
			name: "integrity",
			request: CreateRequest{
				Name: expected, Creator: Creator{SID: testServiceSID, Integrity: IntegrityMedium},
				SecurityDescriptor: testServiceSD,
			},
			want: ErrIntegrityTooLow,
		},
		{
			name: "descriptor",
			request: CreateRequest{
				Name: expected, Creator: testServiceIdentity,
				SecurityDescriptor: testAttackerSD,
			},
			want: ErrDescriptorMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns := NewNamespace()
			if _, err := ns.CreateIfAbsent(tc.request); err != nil {
				t.Fatal(err)
			}
			service := newServiceWithPolicy(t, ns, testServiceIdentity, policy)
			var callbacks atomic.Int32
			if _, err := service.OpenExisting(expected); !errors.Is(err, tc.want) {
				t.Fatalf("OpenExisting error = %v, want %v", err, tc.want)
			}
			if err := service.Use(expected, func(VerifiedHandle) error {
				callbacks.Add(1)
				return nil
			}); !errors.Is(err, ErrNotBound) {
				t.Fatalf("Use error = %v", err)
			}
			if callbacks.Load() != 0 {
				t.Fatal("callback ran for non-compliant object")
			}
		})
	}

	// A sizeable object with the right SID and descriptor but wrong integrity
	// is also rejected.
	if _, err := ns.CreateIfAbsent(CreateRequest{
		Name:               expected,
		Creator:            Creator{SID: testServiceSID, Integrity: IntegrityMedium},
		SecurityDescriptor: testServiceSD,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenExisting(expected); !errors.Is(err, ErrIntegrityTooLow) {
		t.Fatalf("wrong-integrity OpenExisting = %v", err)
	}
}

func TestSessionScopesAreIndependent(t *testing.T) {
	ns := NewNamespace()
	nameA := MustName(SessionScope("s1"), `svc\same`, ObjectTypeSection)
	nameB := MustName(SessionScope("s2"), `svc\same`, ObjectTypeSection)
	policyA := mustPolicy(t, nameA, []string{"sid-a"}, IntegrityHigh, "sd-a")
	policyB := mustPolicy(t, nameB, []string{"sid-b"}, IntegrityHigh, "sd-b")
	serviceA := newServiceWithPolicy(t, ns, Creator{SID: "sid-a", Integrity: IntegrityHigh}, policyA)
	serviceB := newServiceWithPolicy(t, ns, Creator{SID: "sid-b", Integrity: IntegrityHigh}, policyB)

	a, err := serviceA.CreateNew(nameA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := serviceB.CreateNew(nameB)
	if err != nil {
		t.Fatalf("same text in another session should be independent: %v", err)
	}
	if a.ObjectID() == b.ObjectID() {
		t.Fatal("session objects share object ID")
	}
	if _, err := serviceB.OpenExisting(nameA); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("cross-session policy lookup = %v", err)
	}
	if err := a.Map(func(h VerifiedHandle) error {
		if h.Name.Scope != nameA.Scope {
			t.Fatalf("wrong scope in callback: %#v", h.Name.Scope)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Map(func(VerifiedHandle) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestNonceIsServiceGeneratedAndSaved(t *testing.T) {
	ns := NewNamespace()
	names := []Name{
		MustName(SessionScope("s1"), `svc\one`, ObjectTypeMutex),
		MustName(SessionScope("s1"), `svc\two`, ObjectTypeMutex),
	}
	for _, name := range names {
		service := newServiceWithPolicy(t, ns, testServiceIdentity,
			mustPolicy(t, name, []string{testServiceSID}, IntegrityHigh, testServiceSD))
		bound, err := service.CreateNew(name)
		if err != nil {
			t.Fatal(err)
		}
		if bound.CreationNonce() == ([16]byte{}) {
			t.Fatal("zero creation nonce")
		}
		current, ok := ns.Lookup(name.Scope, name.Text)
		if !ok || current.CreationNonce != bound.CreationNonce() {
			t.Fatal("namespace did not commit service nonce")
		}
	}
}
