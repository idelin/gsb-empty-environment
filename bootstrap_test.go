package netsec

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
)

const (
	testAddr  Address        = "10.0.0.7"
	otherAddr Address        = "10.0.0.8"
	instanceA InstanceID     = "vm-aaaa"
	instanceB InstanceID     = "vm-bbbb"
	nonceA    BootNonce      = "nonce-alpha"
	nonceB    BootNonce      = "nonce-beta"
	kindTLS   CredentialKind = "tls"
	kindAPI   CredentialKind = "api-key"
)

func assertRuntimeHas(t *testing.T, installed map[string]Credential, want Credential) {
	t.Helper()
	got, ok := installed[want.bindingKey()]
	if !ok {
		t.Fatalf("runtime missing credential %q; have %v", want.bindingKey(), installed)
	}
	if string(got.Material) != string(want.Material) {
		t.Fatalf("material mismatch for %q: got %q want %q",
			want.bindingKey(), got.Material, want.Material)
	}
}

func TestOutOfOrderFileDelivery(t *testing.T) {
	runtime := newFakeRuntime()
	supervisor := NewSupervisor(runtime)

	if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
		t.Fatalf("BootInstance: %v", err)
	}

	tlsCred := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
	apiCred := newTestCredential(kindAPI, instanceA, testAddr, nonceA, 1)

	dir := t.TempDir()
	if _, err := WriteCredentialDir(dir, []CredentialEnvelope{
		toEnvelope(tlsCred),
		toEnvelope(apiCred),
	}); err != nil {
		t.Fatalf("WriteCredentialDir: %v", err)
	}

	outcomes, err := supervisor.DeliverCredentialDir(dir)
	if err != nil {
		t.Fatalf("DeliverCredentialDir: %v", err)
	}

	var installedCount int
	for _, outcome := range outcomes {
		if outcome.Decision != DecisionInstalled {
			t.Fatalf("file %s: decision = %s, want installed (%v)",
				outcome.Path, outcome.Decision, outcome.Err)
		}
		installedCount++
	}
	if installedCount != 2 {
		t.Fatalf("installed %d files, want 2", installedCount)
	}

	installedSet := runtime.snapshot()
	if len(installedSet) != 2 {
		t.Fatalf("runtime holds %d credentials, want 2: %v", len(installedSet), installedSet)
	}
	assertRuntimeHas(t, installedSet, tlsCred)
	assertRuntimeHas(t, installedSet, apiCred)

	redelivery, err := supervisor.DeliverCredentialDir(dir)
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	for _, outcome := range redelivery {
		if outcome.Decision != DecisionAlreadyActive {
			t.Fatalf("redelivered %s: decision = %s, want already_active",
				outcome.Path, outcome.Decision)
		}
		if !errors.Is(outcome.Err, ErrCredentialAlreadyActive) {
			t.Fatalf("redelivered %s: err = %v, want ErrCredentialAlreadyActive",
				outcome.Path, outcome.Err)
		}
	}
	if len(runtime.snapshot()) != 2 {
		t.Fatalf("runtime changed after redelivery: %v", runtime.snapshot())
	}

	active, ok := supervisor.ActiveWindow(testAddr)
	if !ok {
		t.Fatal("active window disappeared")
	}
	if active.Installed != 2 || active.Generation != 1 || active.Nonce != nonceA {
		t.Fatalf("active window = %+v, want generation 1 / nonceA / 2 installed", active)
	}
}

func TestAddressReuseRejectsOldIdentity(t *testing.T) {
	runtime := newFakeRuntime()
	supervisor := NewSupervisor(runtime)

	if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
		t.Fatalf("boot instance A: %v", err)
	}

	oldTLS := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
	oldAPI := newTestCredential(kindAPI, instanceA, testAddr, nonceA, 1)
	if decision, err := supervisor.HandleCredential(oldTLS); err != nil || decision != DecisionInstalled {
		t.Fatalf("install old tls: decision=%s err=%v", decision, err)
	}
	if decision, err := supervisor.HandleCredential(oldAPI); err != nil || decision != DecisionInstalled {
		t.Fatalf("install old api: decision=%s err=%v", decision, err)
	}

	// Same address is handed to a freshly booted instance: the old window is
	// revoked and the runtime is purged before the new window opens.
	if err := supervisor.BootInstance(instanceB, testAddr, nonceB, 2); err != nil {
		t.Fatalf("boot instance B: %v", err)
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatalf("runtime not purged on reuse: %v", runtime.snapshot())
	}
	if got := runtime.purgeCalls; len(got) != 2 || got[0] != testAddr || got[1] != testAddr {
		t.Fatalf("purge calls = %v, want two purges of %s (one per boot)", got, testAddr)
	}

	active, ok := supervisor.ActiveWindow(testAddr)
	if !ok || active.InstanceID != instanceB || active.Generation != 2 || active.Nonce != nonceB {
		t.Fatalf("active window after reuse = %+v ok=%v", active, ok)
	}

	stagedBefore := runtime.staged

	// Every variant of a late file from the retired instance must be refused.
	lateCases := []struct {
		name     string
		cred     Credential
		decision Decision
		wantErr  error
	}{
		{"old generation", oldTLS, DecisionStaleGeneration, ErrStaleGeneration},
		{"old generation other kind", oldAPI, DecisionStaleGeneration, ErrStaleGeneration},
		{"forged nonce on new generation", newTestCredential(kindTLS, instanceB, testAddr, nonceA, 2), DecisionStaleNonce, ErrStaleNonce},
		{"forged instance on new generation", newTestCredential(kindTLS, instanceA, testAddr, nonceB, 2), DecisionStaleInstance, ErrStaleInstance},
		{"future generation", newTestCredential(kindTLS, instanceB, testAddr, nonceB, 3), DecisionStaleGeneration, ErrStaleGeneration},
		{"unknown address", newTestCredential(kindTLS, instanceA, otherAddr, nonceA, 1), DecisionNoWindow, ErrNoBootstrapWindow},
	}

	for _, tc := range lateCases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := supervisor.HandleCredential(tc.cred)
			if decision != tc.decision {
				t.Fatalf("decision = %s, want %s", decision, tc.decision)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// The sink boundary must never have been crossed by a stale credential.
	if runtime.staged != stagedBefore {
		t.Fatalf("staging count changed from %d to %d: stale credential reached runtime",
			stagedBefore, runtime.staged)
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatalf("old credential entered new runtime: %v", runtime.snapshot())
	}

	// A genuine credential for the new instance installs cleanly.
	newTLS := newTestCredential(kindTLS, instanceB, testAddr, nonceB, 2)
	if decision, err := supervisor.HandleCredential(newTLS); err != nil || decision != DecisionInstalled {
		t.Fatalf("install new tls: decision=%s err=%v", decision, err)
	}
	installed := runtime.snapshot()
	if len(installed) != 1 {
		t.Fatalf("runtime = %v, want exactly the new credential", installed)
	}
	assertRuntimeHas(t, installed, newTLS)

	// Replaying the retired file after the new credential is active is still refused.
	decision, err := supervisor.HandleCredential(oldTLS)
	if decision != DecisionStaleGeneration || !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("replay old tls: decision=%s err=%v", decision, err)
	}
	if len(runtime.snapshot()) != 1 {
		t.Fatalf("replay mutated runtime: %v", runtime.snapshot())
	}
}

func TestInstallFailureLeavesNoCredential(t *testing.T) {
	t.Run("commit failure rolls back partial install", func(t *testing.T) {
		runtime := newFakeRuntime()
		runtime.failCommit = 1
		supervisor := NewSupervisor(runtime)

		if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
			t.Fatalf("boot: %v", err)
		}

		cred := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
		decision, err := supervisor.HandleCredential(cred)
		if decision != DecisionInstallFailed || !errors.Is(err, ErrInstallFailed) {
			t.Fatalf("decision=%s err=%v", decision, err)
		}
		if len(runtime.snapshot()) != 0 {
			t.Fatalf("failed commit left credential in runtime: %v", runtime.snapshot())
		}
		if len(runtime.partialSnapshot()) != 0 {
			t.Fatalf("abort did not remove partial entry: %v", runtime.partialSnapshot())
		}

		// The window stays open and accepts a retry once the fault clears.
		if decision, err := supervisor.HandleCredential(cred); err != nil || decision != DecisionInstalled {
			t.Fatalf("retry: decision=%s err=%v", decision, err)
		}
		assertRuntimeHas(t, runtime.snapshot(), cred)
	})

	t.Run("stage failure never touches runtime", func(t *testing.T) {
		runtime := newFakeRuntime()
		runtime.failStage = 1
		supervisor := NewSupervisor(runtime)

		if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
			t.Fatalf("boot: %v", err)
		}

		cred := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
		decision, err := supervisor.HandleCredential(cred)
		if decision != DecisionRejected || !errors.Is(err, errFakeStage) {
			t.Fatalf("decision=%s err=%v", decision, err)
		}
		if len(runtime.snapshot()) != 0 || len(runtime.partialSnapshot()) != 0 {
			t.Fatalf("stage failure left state behind: installed=%v partial=%v",
				runtime.snapshot(), runtime.partialSnapshot())
		}
		if decision, err := supervisor.HandleCredential(cred); err != nil || decision != DecisionInstalled {
			t.Fatalf("retry after stage fault: decision=%s err=%v", decision, err)
		}
	})

	t.Run("purge failure blocks new window", func(t *testing.T) {
		runtime := newFakeRuntime()
		supervisor := NewSupervisor(runtime)

		if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
			t.Fatalf("first boot: %v", err)
		}
		oldCred := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
		if _, err := supervisor.HandleCredential(oldCred); err != nil {
			t.Fatalf("install old credential: %v", err)
		}

		runtime.failPurge = 1
		err := supervisor.BootInstance(instanceB, testAddr, nonceB, 2)
		if !errors.Is(err, errFakePurge) {
			t.Fatalf("boot with purge fault: err=%v", err)
		}

		// The old window must remain intact rather than mix a failed new boot
		// into a half-purged runtime.
		active, ok := supervisor.ActiveWindow(testAddr)
		if !ok || active.InstanceID != instanceA || active.Generation != 1 {
			t.Fatalf("window mutated after failed boot: %+v ok=%v", active, ok)
		}
		decision, err := supervisor.HandleCredential(oldCred)
		if decision != DecisionAlreadyActive || !errors.Is(err, ErrCredentialAlreadyActive) {
			t.Fatalf("old credential redelivery after failed boot: decision=%s err=%v", decision, err)
		}
		extra := newTestCredential(kindAPI, instanceA, testAddr, nonceA, 1)
		if decision, err := supervisor.HandleCredential(extra); err != nil || decision != DecisionInstalled {
			t.Fatalf("old window stopped accepting matching files: decision=%s err=%v", decision, err)
		}
	})
}

func TestGenerationMonotonicityAndOneTimeUse(t *testing.T) {
	runtime := newFakeRuntime()
	supervisor := NewSupervisor(runtime)

	if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
		t.Fatalf("boot gen 1: %v", err)
	}
	if err := supervisor.BootInstance(instanceB, testAddr, nonceB, 2); err != nil {
		t.Fatalf("boot gen 2: %v", err)
	}

	// Reusing the active generation is treated as a reverted generation.
	err := supervisor.BootInstance(instanceB, testAddr, "nonce-reset", 2)
	if !errors.Is(err, ErrGenerationReverted) {
		t.Fatalf("reuse active gen 2: err=%v, want ErrGenerationReverted", err)
	}

	// Reusing a now-retired generation is refused as one-time use, even with
	// a fresh nonce.
	err = supervisor.BootInstance(instanceA, testAddr, "nonce-reset", 1)
	if !errors.Is(err, ErrGenerationReused) {
		t.Fatalf("reuse retired gen 1: err=%v, want ErrGenerationReused", err)
	}

	// Zero generation is invalid regardless of address history.
	err = supervisor.BootInstance(instanceB, otherAddr, "nonce-0", 0)
	if !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("generation zero: err=%v, want ErrInvalidGeneration", err)
	}

	// Different addresses keep independent generation sequences.
	if err := supervisor.BootInstance(instanceA, otherAddr, nonceA, 1); err != nil {
		t.Fatalf("independent address gen 1: %v", err)
	}
	// Skipping ahead is allowed on the second address; reverting afterward is not.
	if err := supervisor.BootInstance(instanceA, otherAddr, "nonce-4", 4); err != nil {
		t.Fatalf("boot gen 4 on other address: %v", err)
	}
	if err := supervisor.BootInstance(instanceA, otherAddr, "nonce-3", 3); !errors.Is(err, ErrGenerationReverted) {
		t.Fatalf("revert to gen 3: err=%v, want ErrGenerationReverted", err)
	}
	// Reusing the still-active generation on that address is also a revert.
	if err := supervisor.BootInstance(instanceA, otherAddr, "nonce-lower", 4); !errors.Is(err, ErrGenerationReverted) {
		t.Fatalf("reuse active gen 4: err=%v, want ErrGenerationReverted", err)
	}

	// A failed boot must not consume the generation.
	freshAddr := Address("10.0.0.9")
	runtime.failPurge = 1
	if err := supervisor.BootInstance(instanceB, freshAddr, nonceB, 2); !errors.Is(err, errFakePurge) {
		t.Fatalf("failing boot gen 2: %v", err)
	}
	runtime.failPurge = 0
	if err := supervisor.BootInstance(instanceB, freshAddr, nonceB, 2); err != nil {
		t.Fatalf("retry gen 2 after failed boot: %v", err)
	}
}

func TestMalformedAndLateFiles(t *testing.T) {
	runtime := newFakeRuntime()
	supervisor := NewSupervisor(runtime)

	if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
		t.Fatalf("boot: %v", err)
	}

	dir := t.TempDir()

	// Malformed JSON and invalid envelopes must be rejected without staging.
	badJSON := dir + "/cred-bad-json.json"
	if err := os.WriteFile(badJSON, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write bad json: %v", err)
	}
	emptyEnvelope := CredentialEnvelope{Kind: "tls"}
	if _, err := WriteCredentialDir(dir, []CredentialEnvelope{emptyEnvelope}); err != nil {
		t.Fatalf("write empty envelope: %v", err)
	}

	// A valid file is delivered, then its window is replaced before redelivery.
	cred := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
	if _, err := WriteCredentialDir(dir, []CredentialEnvelope{toEnvelope(cred)}); err != nil {
		t.Fatalf("write valid envelope: %v", err)
	} else {
		if outcomes, err := supervisor.DeliverCredentialDir(dir); err != nil {
			t.Fatalf("first delivery: %v", err)
		} else {
			var valid, rejected int
			for _, outcome := range outcomes {
				switch outcome.Decision {
				case DecisionInstalled:
					valid++
				case DecisionRejected:
					rejected++
				default:
					t.Fatalf("unexpected decision %s for %s", outcome.Decision, outcome.Path)
				}
			}
			if valid != 1 || rejected != 2 {
				t.Fatalf("first delivery: valid=%d rejected=%d", valid, rejected)
			}
		}
	}

	// New instance takes over the address; the old file is now late.
	if err := supervisor.BootInstance(instanceB, testAddr, nonceB, 2); err != nil {
		t.Fatalf("reboot: %v", err)
	}
	outcomes, err := supervisor.DeliverCredentialDir(dir)
	if err != nil {
		t.Fatalf("late redelivery: %v", err)
	}
	for _, outcome := range outcomes {
		if outcome.Decision == DecisionInstalled || outcome.Decision == DecisionAlreadyActive {
			t.Fatalf("late file %s accepted with decision %s", outcome.Path, outcome.Decision)
		}
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatalf("runtime should be empty after reuse: %v", runtime.snapshot())
	}
}

func TestConcurrentDeliveryAndReboot(t *testing.T) {
	runtime := newFakeRuntime()
	supervisor := NewSupervisor(runtime)

	if err := supervisor.BootInstance(instanceA, testAddr, nonceA, 1); err != nil {
		t.Fatalf("boot: %v", err)
	}

	var wg sync.WaitGroup

	// Phase 1: hammer the open window with concurrent matching files.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kind := CredentialKind("kind-" + strconv.Itoa(i))
			cred := newTestCredential(kind, instanceA, testAddr, nonceA, 1)
			decision, err := supervisor.HandleCredential(cred)
			if err != nil || decision != DecisionInstalled {
				t.Errorf("concurrent install %d: decision=%s err=%v", i, decision, err)
			}
		}(i)
	}
	wg.Wait()

	if len(runtime.snapshot()) != 20 {
		t.Fatalf("phase 1 installed %d credentials, want 20", len(runtime.snapshot()))
	}

	// Phase 2: a new instance takes over the address.
	if err := supervisor.BootInstance(instanceB, testAddr, nonceB, 2); err != nil {
		t.Fatalf("reboot: %v", err)
	}

	// A burst of late files from the retired instance must never be accepted.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kind := CredentialKind("stale-" + strconv.Itoa(i))
			cred := newTestCredential(kind, instanceA, testAddr, nonceA, 1)
			decision, _ := supervisor.HandleCredential(cred)
			if decision == DecisionInstalled || decision == DecisionAlreadyActive {
				t.Errorf("stale file %d accepted with decision %s", i, decision)
			}
		}(i)
	}

	wg.Wait()

	// Final invariant: every installed credential belongs to the open window.
	for _, cred := range runtime.snapshot() {
		if cred.Generation != 2 || cred.Nonce != nonceB || cred.InstanceID != instanceB {
			t.Fatalf("stale credential survived concurrent reboot: %+v", cred)
		}
	}
	active, ok := supervisor.ActiveWindow(testAddr)
	if !ok || active.Generation != 2 {
		t.Fatalf("active window after concurrent run: %+v ok=%v", active, ok)
	}
	if active.Installed != len(runtime.snapshot()) {
		t.Fatalf("window installed count %d != runtime entries %d",
			active.Installed, len(runtime.snapshot()))
	}

	// After the reboot settles, gen-2 credentials install and gen-1 stays refused.
	newCred := newTestCredential(kindTLS, instanceB, testAddr, nonceB, 2)
	if decision, err := supervisor.HandleCredential(newCred); err != nil || decision != DecisionInstalled {
		t.Fatalf("post-reboot install: decision=%s err=%v", decision, err)
	}
	lateOld := newTestCredential(kindAPI, instanceA, testAddr, nonceA, 1)
	decision, err := supervisor.HandleCredential(lateOld)
	if decision != DecisionStaleGeneration || !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("late old credential: decision=%s err=%v", decision, err)
	}
}

func TestBootValidation(t *testing.T) {
	supervisor := NewSupervisor(newFakeRuntime())

	cases := []struct {
		name  string
		id    InstanceID
		addr  Address
		nonce BootNonce
		gen   Generation
		want  error
	}{
		{"empty instance", "", testAddr, nonceA, 1, ErrEmptyInstanceID},
		{"empty address", instanceA, "", nonceA, 1, ErrEmptyAddress},
		{"empty nonce", instanceA, testAddr, "", 1, ErrEmptyNonce},
		{"zero generation", instanceA, testAddr, nonceA, 0, ErrInvalidGeneration},
		{"negative generation", instanceA, testAddr, nonceA, -3, ErrInvalidGeneration},
		{"whitespace nonce", instanceA, testAddr, "   ", 1, ErrEmptyNonce},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := supervisor.BootInstance(tc.id, tc.addr, tc.nonce, tc.gen)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	if _, ok := supervisor.ActiveWindow(testAddr); ok {
		t.Fatal("invalid boot must not open a window")
	}
}

func TestCredentialValidation(t *testing.T) {
	supervisor := NewSupervisor(newFakeRuntime())

	base := newTestCredential(kindTLS, instanceA, testAddr, nonceA, 1)
	cases := []struct {
		name    string
		mutate  func(*Credential)
		wantErr error
	}{
		{"empty kind", func(c *Credential) { c.Kind = "" }, ErrEmptyCredentialKind},
		{"empty instance", func(c *Credential) { c.InstanceID = "" }, ErrEmptyInstanceID},
		{"empty address", func(c *Credential) { c.Address = "" }, ErrEmptyAddress},
		{"empty nonce", func(c *Credential) { c.Nonce = "" }, ErrEmptyNonce},
		{"zero generation", func(c *Credential) { c.Generation = 0 }, ErrInvalidGeneration},
		{"empty material", func(c *Credential) { c.Material = nil }, ErrEmptyMaterial},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cred := base
			tc.mutate(&cred)
			decision, err := supervisor.HandleCredential(cred)
			if decision != DecisionRejected || !errors.Is(err, tc.wantErr) {
				t.Fatalf("decision=%s err=%v, want rejected / %v", decision, err, tc.wantErr)
			}
		})
	}
}

func TestDecisionAndWindowStateLabels(t *testing.T) {
	if got := WindowOpen.String(); got != "open" {
		t.Fatalf("WindowOpen = %q", got)
	}
	if got := WindowRevoked.String(); got != "revoked" {
		t.Fatalf("WindowRevoked = %q", got)
	}
	decisions := []Decision{
		DecisionInstalled, DecisionAlreadyActive, DecisionStaleGeneration,
		DecisionStaleNonce, DecisionStaleInstance, DecisionRevokedWindow,
		DecisionNoWindow, DecisionRejected, DecisionInstallFailed,
	}
	seen := make(map[string]struct{}, len(decisions))
	for _, decision := range decisions {
		label := decision.String()
		if label == "" || label == "unknown" {
			t.Fatalf("decision %d has no stable label", decision)
		}
		seen[label] = struct{}{}
	}
	if len(seen) != len(decisions) {
		t.Fatalf("decision labels are not unique: %v", seen)
	}
}

func TestNewSupervisorRequiresSink(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewSupervisor(nil) should panic")
		}
	}()
	NewSupervisor(nil)
}

func TestNewBootNonce(t *testing.T) {
	seen := make(map[BootNonce]struct{}, 64)
	for i := 0; i < 64; i++ {
		nonce, err := NewBootNonce()
		if err != nil {
			t.Fatalf("NewBootNonce: %v", err)
		}
		if len(nonce) != 32 {
			t.Fatalf("nonce %q length = %d, want 32 hex chars", nonce, len(nonce))
		}
		if _, dup := seen[nonce]; dup {
			t.Fatalf("duplicate nonce generated: %s", nonce)
		}
		seen[nonce] = struct{}{}
	}
}
