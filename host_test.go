package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDerivedKeysSeparateDirections(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 1, []byte("root-direction"))

	sendContext := Context{
		Tenant:    []byte("tenant-a"),
		Purpose:   PurposeAuthentication,
		Version:   3,
		Direction: DirectionSend,
		Epoch:     11,
	}
	receiveContext := sendContext
	receiveContext.Direction = DirectionReceive

	sendFirst := deriveProbe(t, host, root, sendContext)
	sendSecond := deriveProbe(t, host, root, sendContext)
	receive := deriveProbe(t, host, root, receiveContext)

	if !bytes.Equal(sendFirst, sendSecond) {
		t.Fatal("the same context produced different keys")
	}
	if bytes.Equal(sendFirst, receive) {
		t.Fatal("send and receive contexts produced the same key")
	}
}

func TestTenantIsolationAcrossCache(t *testing.T) {
	counter := &eventCounter{}
	host := NewHost(WithObserver(counter.observe))
	root := mustInstallRoot(t, host, 1, []byte("root-tenant"))

	contextA := probeContext("tenant-a")
	contextB := probeContext("tenant-b")

	keyA1 := deriveProbe(t, host, root, contextA)
	keyB := deriveProbe(t, host, root, contextB)
	keyA2 := deriveProbe(t, host, root, contextA)

	if bytes.Equal(keyA1, keyB) {
		t.Fatal("different tenants shared a derived key")
	}
	if !bytes.Equal(keyA1, keyA2) {
		t.Fatal("cached derivation for tenant A changed")
	}
	if got := counter.count(CryptoExpand); got != 2 {
		t.Fatalf("Expand calls = %d, want 2 (one per tenant)", got)
	}
	if got := counter.count(CryptoCacheHit); got != 1 {
		t.Fatalf("cache hits = %d, want 1", got)
	}
}

func TestEveryContextFieldSeparatesKeys(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 1, []byte("root-context"))

	base := probeContext("tenant")
	variants := []Context{base}

	tenantVariant := base
	tenantVariant.Tenant = []byte("tenanT")
	variants = append(variants, tenantVariant)

	purposeAuth := base
	purposeAuth.Purpose = PurposeAuthentication
	variants = append(variants, purposeAuth)

	purposeTicket := base
	purposeTicket.Purpose = PurposeTicket
	variants = append(variants, purposeTicket)

	versionVariant := base
	versionVariant.Version++
	variants = append(variants, versionVariant)

	directionVariant := base
	directionVariant.Direction = DirectionReceive
	variants = append(variants, directionVariant)

	epochVariant := base
	epochVariant.Epoch++
	variants = append(variants, epochVariant)

	seen := make(map[string]string)
	for _, context := range variants {
		probe := deriveProbe(t, host, root, context)
		label := context.Purpose.String() + "/" + context.Direction.String()
		if previous, duplicate := seen[string(probe)]; duplicate {
			t.Fatalf("contexts %s and %s produced the same key", previous, label)
		}
		seen[string(probe)] = label
	}
}

func TestCacheHitReturnsIndependentOneShotHandles(t *testing.T) {
	counter := &eventCounter{}
	host := NewHost(WithObserver(counter.observe))
	root := mustInstallRoot(t, host, 1, []byte("root-cache"))
	context := probeContext("tenant")

	first, err := host.Derive(root, context, 32)
	if err != nil {
		t.Fatalf("Derive first: %v", err)
	}
	second, err := host.Derive(root, context, 32)
	if err != nil {
		t.Fatalf("Derive second: %v", err)
	}
	if got := counter.count(CryptoCacheHit); got != 1 {
		t.Fatalf("cache hits = %d, want 1", got)
	}

	firstMAC := useProbe(t, first)
	secondMAC := useProbe(t, second)
	if !bytes.Equal(firstMAC, secondMAC) {
		t.Fatal("cache hit returned different key material")
	}
	if err := first.Use(func(*Key) error { return nil }); !errors.Is(err, ErrHandleUsed) {
		t.Fatalf("reusing first handle: got %v, want ErrHandleUsed", err)
	}
	if err := second.Use(func(*Key) error { return nil }); !errors.Is(err, ErrHandleUsed) {
		t.Fatalf("reusing second handle: got %v, want ErrHandleUsed", err)
	}
}

func TestDerivedCacheKeyContainsGenerationAndFullContext(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 41, []byte("root-cache-key"))
	context := probeContext("tenant")
	encoded, err := context.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if _, err := host.Derive(root, context, 32); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(host.cache) != 1 {
		t.Fatalf("cache entries = %d, want 1", len(host.cache))
	}
	for key := range host.cache {
		if key.rootID != root.id {
			t.Errorf("cache root ID = %d, want %d", key.rootID, root.id)
		}
		if key.generation != 41 {
			t.Errorf("cache generation = %d, want 41", key.generation)
		}
		if key.context != string(encoded) {
			t.Errorf("cache context does not contain the complete canonical context")
		}
		if key.length != 32 {
			t.Errorf("cache length = %d, want 32", key.length)
		}
	}
}

func TestRootRotationChangesKeysAndRevokesOldRoot(t *testing.T) {
	host := NewHost()
	oldRoot := mustInstallRoot(t, host, 1, []byte("root-one"))
	context := probeContext("tenant")
	oldKey := deriveProbe(t, host, oldRoot, context)

	newRoot, err := host.RotateRoot(oldRoot, 2, []byte("root-two"))
	if err != nil {
		t.Fatalf("RotateRoot: %v", err)
	}
	if host.RootActive(oldRoot) {
		t.Fatal("old root remained active after rotation")
	}
	if !host.RootActive(newRoot) {
		t.Fatal("new root is not active")
	}
	if _, err := host.Derive(oldRoot, context, 32); !errors.Is(err, ErrRootRevoked) {
		t.Fatalf("derive from old root: got %v, want ErrRootRevoked", err)
	}
	if len(host.cache) != 0 {
		t.Fatalf("rotation retained %d old cache entries", len(host.cache))
	}

	newKey := deriveProbe(t, host, newRoot, context)
	newKeyAgain := deriveProbe(t, host, newRoot, context)
	if bytes.Equal(oldKey, newKey) {
		t.Fatal("root rotation reused key material")
	}
	if !bytes.Equal(newKey, newKeyAgain) {
		t.Fatal("new root cache is unstable")
	}
	if _, err := host.RotateRoot(newRoot, 2, []byte("root-three")); !errors.Is(err, ErrGenerationReused) {
		t.Fatalf("same-generation rotation: got %v, want ErrGenerationReused", err)
	}
	if _, err := host.InstallRoot(2, []byte("root-duplicate")); !errors.Is(err, ErrDuplicateGeneration) {
		t.Fatalf("duplicate generation: got %v, want ErrDuplicateGeneration", err)
	}
}

func TestRootGenerationBindsDerivation(t *testing.T) {
	host := NewHost()
	rootKey := []byte("same-root-material")
	firstRoot := mustInstallRoot(t, host, 1, rootKey)
	secondRoot := mustInstallRoot(t, host, 2, rootKey)
	context := probeContext("tenant")

	firstKey := deriveProbe(t, host, firstRoot, context)
	secondKey := deriveProbe(t, host, secondRoot, context)
	if bytes.Equal(firstKey, secondKey) {
		t.Fatal("root generation did not separate otherwise identical derivations")
	}
}

func TestRevocationLifecycle(t *testing.T) {
	counter := &eventCounter{}
	host := NewHost(WithObserver(counter.observe))
	root := mustInstallRoot(t, host, 1, []byte("root-revoke"))
	context := probeContext("tenant")

	issued, err := host.Derive(root, context, 32)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	if err := host.RevokeRoot(root); err != nil {
		t.Fatalf("RevokeRoot: %v", err)
	}
	if host.RootActive(root) {
		t.Fatal("root remained active after revocation")
	}
	if err := host.RevokeRoot(root); err != nil {
		t.Fatalf("idempotent RevokeRoot: %v", err)
	}

	expandsBefore := counter.count(CryptoExpand)
	if _, err := host.Derive(root, context, 32); !errors.Is(err, ErrRootRevoked) {
		t.Fatalf("post-revocation derive: got %v, want ErrRootRevoked", err)
	}
	if got := counter.count(CryptoExpand); got != expandsBefore {
		t.Fatalf("revoked root triggered %d extra Expand calls", got-expandsBefore)
	}

	if got := useProbe(t, issued); len(got) == 0 {
		t.Fatal("issued handle failed after root revocation")
	}
	if err := issued.Use(func(*Key) error { return nil }); !errors.Is(err, ErrHandleUsed) {
		t.Fatalf("reuse after revocation: got %v, want ErrHandleUsed", err)
	}
	if got := counter.count(CryptoUse); got != 1 {
		t.Fatalf("use events = %d, want 1", got)
	}

	host.Close()
	if host.RootActive(root) {
		t.Fatal("Close left root active")
	}
}

func TestUseZeroizesMaterialAndCopiesContext(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 1, []byte("root-zeroize"))
	context := probeContext("tenant")

	handle, err := host.Derive(root, context, 32)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	context.Tenant[0] ^= 0xff
	if got := handle.Context().Tenant; !bytes.Equal(got, []byte("tenant")) {
		t.Fatalf("handle context aliased caller tenant: %q", got)
	}

	var scoped *Key
	if err := handle.Use(func(key *Key) error {
		scoped = key
		if len(key.HMAC([]byte("probe"))) != SHA256Size {
			t.Fatal("HMAC did not produce a SHA-256 digest")
		}
		return nil
	}); err != nil {
		t.Fatalf("Use: %v", err)
	}

	if scoped == nil {
		t.Fatal("operation did not receive a key view")
	}
	for i, value := range scoped.material {
		if value != 0 {
			t.Fatalf("key material byte %d was not zeroized", i)
		}
	}
	if !handle.Used() {
		t.Fatal("handle was not marked used")
	}
}

func TestConcurrentUseConsumesHandleOnce(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 1, []byte("root-one-shot"))
	handle, err := host.Derive(root, probeContext("tenant"), 32)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	const workers = 64
	start := make(chan struct{})
	results := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- handle.Use(func(*Key) error {
				time.Sleep(time.Microsecond)
				return nil
			})
		}()
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrHandleUsed):
		default:
			t.Errorf("unexpected Use error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful uses = %d, want 1", successes)
	}
}

func TestRevocationRace(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 1, []byte("root-race"))
	context := probeContext("tenant")

	type deriveResult struct {
		handle *DerivedHandle
		err    error
	}

	const preWorkers = 64
	preResults := make(chan deriveResult, preWorkers)
	start := make(chan struct{})
	var preGroup sync.WaitGroup
	for range preWorkers {
		preGroup.Add(1)
		go func() {
			defer preGroup.Done()
			<-start
			handle, err := host.Derive(root, context, 32)
			preResults <- deriveResult{handle: handle, err: err}
		}()
	}
	close(start)

	collected := make([]deriveResult, 0, preWorkers)
	haveSuccess := false
	for len(collected) < preWorkers {
		result := <-preResults
		collected = append(collected, result)
		if result.err == nil {
			haveSuccess = true
			break
		}
	}
	if !haveSuccess {
		t.Fatal("no derivation completed before revocation")
	}

	if err := host.RevokeRoot(root); err != nil {
		t.Fatalf("RevokeRoot: %v", err)
	}

	const postWorkers = 32
	postErrors := make(chan error, postWorkers)
	var postGroup sync.WaitGroup
	for range postWorkers {
		postGroup.Add(1)
		go func() {
			defer postGroup.Done()
			_, err := host.Derive(root, context, 32)
			postErrors <- err
		}()
	}
	postGroup.Wait()
	close(postErrors)
	for err := range postErrors {
		if !errors.Is(err, ErrRootRevoked) {
			t.Errorf("post-revocation derive: got %v, want ErrRootRevoked", err)
		}
	}

	preGroup.Wait()
	close(preResults)
	for result := range preResults {
		collected = append(collected, result)
	}

	successes := 0
	for _, result := range collected {
		if result.err != nil {
			if !errors.Is(result.err, ErrRootRevoked) {
				t.Errorf("pre-revocation derive failed with %v", result.err)
			}
			continue
		}
		successes++
		if result.handle == nil {
			t.Fatal("successful derive returned a nil handle")
		}
		if got := useProbe(t, result.handle); len(got) == 0 {
			t.Fatal("issued handle failed after racing revocation")
		}
		if err := result.handle.Use(func(*Key) error { return nil }); !errors.Is(err, ErrHandleUsed) {
			t.Errorf("second use: got %v, want ErrHandleUsed", err)
		}
	}
	if successes == 0 {
		t.Fatal("no successful derivations")
	}
}

func TestRotationRace(t *testing.T) {
	host := NewHost()
	oldRoot := mustInstallRoot(t, host, 1, []byte("root-rotate-race-old"))
	context := probeContext("tenant")

	type deriveResult struct {
		handle *DerivedHandle
		err    error
	}

	const workers = 48
	results := make(chan deriveResult, workers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			handle, err := host.Derive(oldRoot, context, 32)
			results <- deriveResult{handle: handle, err: err}
		}()
	}
	close(start)

	collected := make([]deriveResult, 0, workers)
	haveSuccess := false
	for len(collected) < workers {
		result := <-results
		collected = append(collected, result)
		if result.err == nil {
			haveSuccess = true
			break
		}
	}
	if !haveSuccess {
		t.Fatal("no derivation completed before rotation")
	}

	newRoot, err := host.RotateRoot(oldRoot, 2, []byte("root-rotate-race-new"))
	if err != nil {
		t.Fatalf("RotateRoot: %v", err)
	}
	if host.RootActive(oldRoot) {
		t.Fatal("old root remained active after rotation")
	}

	const postWorkers = 24
	postErrors := make(chan error, postWorkers)
	var postGroup sync.WaitGroup
	for range postWorkers {
		postGroup.Add(1)
		go func() {
			defer postGroup.Done()
			_, err := host.Derive(oldRoot, context, 32)
			postErrors <- err
		}()
	}
	postGroup.Wait()
	close(postErrors)
	for err := range postErrors {
		if !errors.Is(err, ErrRootRevoked) {
			t.Errorf("post-rotation derive with old root: got %v, want ErrRootRevoked", err)
		}
	}
	if _, err := host.Derive(newRoot, context, 32); err != nil {
		t.Fatalf("derive with new root: %v", err)
	}

	group.Wait()
	close(results)
	for result := range results {
		collected = append(collected, result)
	}

	successes := 0
	for _, result := range collected {
		if result.err != nil {
			if !errors.Is(result.err, ErrRootRevoked) {
				t.Errorf("pre-rotation derive failed with %v", result.err)
			}
			continue
		}
		successes++
		if got := useProbe(t, result.handle); len(got) == 0 {
			t.Fatal("issued handle failed after racing rotation")
		}
		if err := result.handle.Use(func(*Key) error { return nil }); !errors.Is(err, ErrHandleUsed) {
			t.Errorf("second use: got %v, want ErrHandleUsed", err)
		}
	}
	if successes == 0 {
		t.Fatal("no successful derivations")
	}
}

func TestInvalidInputsTriggerNoCryptoOperations(t *testing.T) {
	counter := &eventCounter{}
	host := NewHost(WithObserver(counter.observe))
	root := mustInstallRoot(t, host, 1, []byte("root-invalid"))

	otherHost := NewHost()
	otherRoot := mustInstallRoot(t, otherHost, 1, []byte("other-root"))

	valid := probeContext("tenant")
	invalidContexts := []struct {
		name    string
		context Context
		want    error
	}{
		{
			name:    "empty tenant",
			context: Context{Purpose: PurposeEncryption, Direction: DirectionSend},
			want:    ErrEmptyTenant,
		},
		{
			name: "overlong tenant",
			context: Context{
				Tenant:    make([]byte, MaxTenantBytes+1),
				Purpose:   PurposeEncryption,
				Direction: DirectionSend,
			},
			want: ErrTenantTooLong,
		},
		{
			name: "unknown purpose",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   200,
				Direction: DirectionSend,
			},
			want: ErrUnknownPurpose,
		},
		{
			name: "unknown direction",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   PurposeEncryption,
				Direction: 200,
			},
			want: ErrUnknownDirection,
		},
	}
	for _, test := range invalidContexts {
		t.Run(test.name, func(t *testing.T) {
			if _, err := host.Derive(root, test.context, 32); !errors.Is(err, test.want) {
				t.Fatalf("Derive: got %v, want %v", err, test.want)
			}
		})
	}

	for _, length := range []int{0, -1, MaxExpandLength + 1} {
		if _, err := host.Derive(root, valid, length); !errors.Is(err, ErrInvalidKeyLength) {
			t.Errorf("Derive length %d: got %v, want ErrInvalidKeyLength", length, err)
		}
	}
	if _, err := host.Derive(RootHandle{}, valid, 32); !errors.Is(err, ErrInvalidRoot) {
		t.Errorf("zero root: got %v, want ErrInvalidRoot", err)
	}
	if _, err := host.Derive(otherRoot, valid, 32); !errors.Is(err, ErrInvalidRoot) {
		t.Errorf("foreign root: got %v, want ErrInvalidRoot", err)
	}

	for _, operation := range []CryptoOperation{CryptoExtract, CryptoExpand, CryptoCacheHit, CryptoUse} {
		if got := counter.count(operation); got != 0 {
			t.Fatalf("%s events = %d before any valid operation", operation, got)
		}
	}

	if _, err := host.Derive(root, valid, 32); err != nil {
		t.Fatalf("valid Derive: %v", err)
	}
	if counter.count(CryptoExtract) != 1 || counter.count(CryptoExpand) != 1 {
		t.Fatalf("valid operation was not observed: extract=%d expand=%d",
			counter.count(CryptoExtract), counter.count(CryptoExpand))
	}
}

func TestHostCopiesRootAndRejectsBadRootKeys(t *testing.T) {
	original := []byte("root-copy-material")
	hostA := NewHost()
	rootA := mustInstallRoot(t, hostA, 1, original)

	hostB := NewHost()
	rootB := mustInstallRoot(t, hostB, 1, append([]byte(nil), original...))

	clear(original)

	context := probeContext("tenant")
	keyA := deriveProbe(t, hostA, rootA, context)
	keyB := deriveProbe(t, hostB, rootB, context)
	if !bytes.Equal(keyA, keyB) {
		t.Fatal("host did not copy imported root key material")
	}

	if _, err := hostA.InstallRoot(2, nil); !errors.Is(err, ErrInvalidRootKey) {
		t.Errorf("empty root key: got %v, want ErrInvalidRootKey", err)
	}
	if _, err := hostA.InstallRoot(2, make([]byte, MaxRootKeyBytes+1)); !errors.Is(err, ErrInvalidRootKey) {
		t.Errorf("oversized root key: got %v, want ErrInvalidRootKey", err)
	}

	if _, err := hostA.GenerateRoot(3); err != nil {
		t.Errorf("GenerateRoot: %v", err)
	}
}

func TestNilOperationAndInvalidHandles(t *testing.T) {
	if err := (*DerivedHandle)(nil).Use(func(*Key) error { return nil }); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("nil handle: got %v, want ErrInvalidHandle", err)
	}
	if err := (&DerivedHandle{}).Use(func(*Key) error { return nil }); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("zero handle: got %v, want ErrInvalidHandle", err)
	}

	host := NewHost()
	root := mustInstallRoot(t, host, 1, []byte("root-nil-op"))
	handle, err := host.Derive(root, probeContext("tenant"), 32)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if err := handle.Use(nil); !errors.Is(err, ErrNilOperation) {
		t.Fatalf("nil operation: got %v, want ErrNilOperation", err)
	}
	if handle.Used() {
		t.Fatal("nil operation consumed the handle")
	}
	if err := handle.Use(func(*Key) error { return nil }); err != nil {
		t.Fatalf("use after nil operation: %v", err)
	}
}

func TestZeroValueHostIsUsable(t *testing.T) {
	var host Host
	root, err := host.InstallRoot(1, []byte("zero-value-root"))
	if err != nil {
		t.Fatalf("InstallRoot on zero-value Host: %v", err)
	}
	if got := deriveProbe(t, &host, root, probeContext("tenant")); len(got) == 0 {
		t.Fatal("zero-value Host could not derive")
	}
}

func TestHostMetadataAliasesAndObserverSetter(t *testing.T) {
	host := NewHost()
	root := mustInstallRoot(t, host, 9, []byte("metadata-root"))
	if root.Generation() != 9 {
		t.Fatalf("root generation = %d, want 9", root.Generation())
	}
	if !(RootHandle{}).IsZero() {
		t.Fatal("zero RootHandle was not reported as zero")
	}
	if root.IsZero() {
		t.Fatal("installed RootHandle was reported as zero")
	}

	counter := &eventCounter{}
	host.SetObserver(counter.observe)

	context := probeContext("tenant")
	handle, err := host.DeriveKey(root, context, 16)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if handle.RootGeneration() != 9 {
		t.Fatalf("handle generation = %d, want 9", handle.RootGeneration())
	}
	if handle.KeySize() != 16 {
		t.Fatalf("handle key size = %d, want 16", handle.KeySize())
	}
	if got := handle.Context().Tenant; !bytes.Equal(got, context.Tenant) {
		t.Fatalf("handle context tenant = %q, want %q", got, context.Tenant)
	}

	if err := handle.WithKey(func(key *Key) error {
		if key.Size() != 16 {
			t.Errorf("Key.Size = %d, want 16", key.Size())
		}
		if len(key.HMACSHA256([]byte("message"))) != SHA256Size {
			t.Error("HMACSHA256 did not return a SHA-256 digest")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithKey: %v", err)
	}
	if counter.count(CryptoUse) != 1 {
		t.Fatalf("use events = %d, want 1", counter.count(CryptoUse))
	}
}

type eventCounter struct {
	mu     sync.Mutex
	events []CryptoEvent
}

func (counter *eventCounter) observe(event CryptoEvent) {
	counter.mu.Lock()
	counter.events = append(counter.events, event)
	counter.mu.Unlock()
}

func (counter *eventCounter) count(operation CryptoOperation) int {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	total := 0
	for _, event := range counter.events {
		if event.Operation == operation {
			total++
		}
	}
	return total
}

func mustInstallRoot(t *testing.T, host *Host, generation uint64, key []byte) RootHandle {
	t.Helper()
	root, err := host.InstallRoot(generation, key)
	if err != nil {
		t.Fatalf("InstallRoot: %v", err)
	}
	return root
}

func probeContext(tenant string) Context {
	return Context{
		Tenant:    []byte(tenant),
		Purpose:   PurposeEncryption,
		Version:   1,
		Direction: DirectionSend,
		Epoch:     7,
	}
}

func deriveProbe(t *testing.T, host *Host, root RootHandle, context Context) []byte {
	t.Helper()
	handle, err := host.Derive(root, context, 32)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return useProbe(t, handle)
}

func useProbe(t *testing.T, handle *DerivedHandle) []byte {
	t.Helper()
	var result []byte
	if err := handle.Use(func(key *Key) error {
		result = append([]byte(nil), key.HMAC([]byte("probe"))...)
		return nil
	}); err != nil {
		t.Fatalf("Use: %v", err)
	}
	return result
}
