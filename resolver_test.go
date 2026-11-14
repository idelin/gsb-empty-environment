package netsec_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	netsec "example.org/netsec/q0338"
)

const (
	publicSourceID   = "public-proxy"
	privateSourceID  = "corp-private"
	privateNamespace = "corp.example/internal"
	modulePath       = "corp.example/internal/payments"
	moduleVersion    = "v1.2.3"
	signerKeyID      = "corp-release-key"
)

var (
	releasePrivateKey  = ed25519.NewKeyFromSeed(sha256Sum("netsec test release key"))
	releasePublicKey   = releasePrivateKey.Public().(ed25519.PublicKey)
	attackerPrivateKey = ed25519.NewKeyFromSeed(sha256Sum("netsec test attacker key"))
)

func sha256Sum(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func TestPrivateNamespaceDoesNotUseSameNamedPublicPackage(t *testing.T) {
	privateArtifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	publicArtifact := validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)
	publicArtifact.Content = moduleZip(t, modulePath, moduleVersion, map[string]string{
		"payments.go": "package payments\n\n// attacker payload\n",
	})
	publicArtifact.Checksum = mustH1(t, publicArtifact.Content)
	publicArtifact.Signature = signArtifact(t, attackerPrivateKey, "attacker-key", publicArtifact)

	publicRepo := &fakeRepository{artifact: publicArtifact}
	privateRepo := &fakeRepository{artifact: privateArtifact}
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: privateRepo},
	}, audit)

	result, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil {
		t.Fatalf("resolve legitimate private module: %v", err)
	}
	if publicRepo.Calls() != 0 {
		t.Fatalf("public repository was probed %d time(s); pinned source must be used exclusively", publicRepo.Calls())
	}
	if privateRepo.Calls() != 1 {
		t.Fatalf("private repository calls = %d, want 1", privateRepo.Calls())
	}
	if result.Source.ID != privateSourceID || result.Source.Kind != netsec.SourcePrivate {
		t.Fatalf("resolved source = %#v, want private source %q", result.Source, privateSourceID)
	}
	if result.Artifact.SourceID != privateSourceID {
		t.Fatalf("artifact source = %q, want %q", result.Artifact.SourceID, privateSourceID)
	}
	if !hasEvent(audit.Snapshot(), netsec.AuditVerified) {
		t.Fatal("missing verified audit event")
	}
}

func TestPackageMetadataCannotRewriteSource(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, map[string]string{
		"source":     publicSourceID,
		"repository": "https://attacker.example",
		"checksum":   "sha256:attacker-controlled",
		"version":    "v9.9.9",
	})
	publicRepo := &fakeRepository{artifact: validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)}
	privateRepo := &fakeRepository{artifact: artifact}
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: privateRepo},
	}, audit)

	result, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil {
		t.Fatalf("resolve module with hostile metadata: %v", err)
	}
	if result.Source.ID != privateSourceID {
		t.Fatalf("metadata changed source to %q", result.Source.ID)
	}
	if publicRepo.Calls() != 0 {
		t.Fatalf("public repository was probed %d time(s)", publicRepo.Calls())
	}
	if !hasEvent(audit.Snapshot(), netsec.AuditMetadataIgnored) {
		t.Fatal("source-related metadata was not recorded as ignored")
	}
}

func TestChecksumMismatchRejectsArtifactWithoutFallback(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	artifact.Checksum = "h1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	artifact.Signature = signArtifact(t, releasePrivateKey, signerKeyID, artifact)

	publicRepo := &fakeRepository{artifact: validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)}
	privateRepo := &fakeRepository{artifact: artifact}
	cache := netsec.NewMemoryCache()
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, cache, []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: privateRepo},
	}, audit)

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrChecksumMismatch) {
		t.Fatalf("resolve error = %v, want ErrChecksumMismatch", err)
	}
	if publicRepo.Calls() != 0 {
		t.Fatalf("checksum failure fell back to public source (%d call(s))", publicRepo.Calls())
	}
	if cache.SourceLen(privateSourceID) != 0 || cache.SourceLen(publicSourceID) != 0 {
		t.Fatal("rejected artifact was written to cache")
	}
	if !hasEvent(audit.Snapshot(), netsec.AuditRejected) {
		t.Fatal("rejection was not audited")
	}
}

func TestTrustedChecksumPinRejectsRepositorySubstitution(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	expected := "sha256:" + fmt.Sprintf("%064x", 1)
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: &fakeRepository{}},
		{SourceID: privateSourceID, Repository: &fakeRepository{artifact: artifact}},
	}, audit)

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion, ExpectedChecksum: expected,
	})
	if !errors.Is(err, netsec.ErrChecksumMismatch) {
		t.Fatalf("resolve error = %v, want pinned checksum mismatch", err)
	}
}

func TestSameModuleFromAnotherSourceIsAConflict(t *testing.T) {
	forged := validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)
	publicRepo := &fakeRepository{artifact: forged}
	privateRepo := &fakeRepository{artifact: forged}
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: privateRepo},
	}, audit)

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrSourceConflict) {
		t.Fatalf("resolve error = %v, want ErrSourceConflict", err)
	}
	if publicRepo.Calls() != 0 {
		t.Fatalf("source conflict triggered public repository access")
	}
	if !hasEvent(audit.Snapshot(), netsec.AuditConflict) {
		t.Fatal("conflict was not audited")
	}
}

func TestSignatureFromUntrustedKeyIsRejected(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, attackerPrivateKey, "attacker-key", nil)
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: &fakeRepository{}},
		{SourceID: privateSourceID, Repository: &fakeRepository{artifact: artifact}},
	}, &netsec.AuditLog{})

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrUntrustedSigner) {
		t.Fatalf("resolve error = %v, want ErrUntrustedSigner", err)
	}
}

func TestSignatureFailuresAreRejected(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*netsec.Artifact)
		wantError error
	}{
		{
			name: "missing signature",
			mutate: func(artifact *netsec.Artifact) {
				artifact.Signature = nil
			},
			wantError: netsec.ErrSignatureMissing,
		},
		{
			name: "tampered signature",
			mutate: func(artifact *netsec.Artifact) {
				artifact.Signature.Value[0] ^= 0xff
			},
			wantError: netsec.ErrSignatureInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
			test.mutate(&artifact)
			resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
				{SourceID: publicSourceID, Repository: &fakeRepository{}},
				{SourceID: privateSourceID, Repository: &fakeRepository{artifact: artifact}},
			}, &netsec.AuditLog{})

			_, err := resolver.Resolve(context.Background(), netsec.Request{
				ModulePath: modulePath, Version: moduleVersion,
			})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("resolve error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestVersionMismatchIsRejected(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	artifact.Version = "v1.2.2"
	artifact.Signature = signArtifact(t, releasePrivateKey, signerKeyID, artifact)
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: &fakeRepository{}},
		{SourceID: privateSourceID, Repository: &fakeRepository{artifact: artifact}},
	}, &netsec.AuditLog{})

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrVersionMismatch) {
		t.Fatalf("resolve error = %v, want ErrVersionMismatch", err)
	}
}

func TestCacheHitUsesOnlyDeclaredSource(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	publicArtifact := validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)
	cache := netsec.NewMemoryCache()
	// A hostile public cache entry with the same module/version must not satisfy
	// the private-source lookup.
	if err := cache.Put(context.Background(), netsec.CacheKey{
		SourceID: publicSourceID, ModulePath: modulePath, Version: moduleVersion,
	}, publicArtifact); err != nil {
		t.Fatalf("seed public cache partition: %v", err)
	}

	publicRepo := &fakeRepository{artifact: publicArtifact}
	privateRepo := &fakeRepository{artifact: artifact}
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, cache, []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: privateRepo},
	}, audit)

	first, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if first.FromCache {
		t.Fatal("first resolution unexpectedly came from cache")
	}

	second, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if !second.FromCache {
		t.Fatal("second resolution did not hit the source-isolated cache")
	}
	if second.Source.ID != privateSourceID || second.Artifact.SourceID != privateSourceID {
		t.Fatalf("cache hit used source %q, want %q", second.Artifact.SourceID, privateSourceID)
	}
	if privateRepo.Calls() != 1 {
		t.Fatalf("private repository calls = %d, want 1", privateRepo.Calls())
	}
	if publicRepo.Calls() != 0 {
		t.Fatalf("public repository calls = %d, want 0", publicRepo.Calls())
	}
	if cache.SourceLen(privateSourceID) != 1 {
		t.Fatalf("private cache partition size = %d, want 1", cache.SourceLen(privateSourceID))
	}
	if cache.SourceLen(publicSourceID) != 1 {
		t.Fatalf("public cache partition size = %d, want 1", cache.SourceLen(publicSourceID))
	}
	if !hasEvent(audit.Snapshot(), netsec.AuditCacheHit) {
		t.Fatal("cache hit was not audited")
	}
}

func TestMemoryCachePartitionsSameNameBySource(t *testing.T) {
	cache := netsec.NewMemoryCache()
	privateArtifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	publicArtifact := validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)
	publicArtifact.Content = moduleZip(t, modulePath, moduleVersion, map[string]string{
		"payments.go": "package payments\n\n// attacker payload\n",
	})
	publicArtifact.Checksum = mustH1(t, publicArtifact.Content)

	for _, artifact := range []netsec.Artifact{privateArtifact, publicArtifact} {
		if err := cache.Put(context.Background(), netsec.CacheKey{
			SourceID: artifact.SourceID, ModulePath: modulePath, Version: moduleVersion,
		}, artifact); err != nil {
			t.Fatalf("put %s: %v", artifact.SourceID, err)
		}
	}

	privateHit, ok, err := cache.Get(context.Background(), netsec.CacheKey{
		SourceID: privateSourceID, ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil || !ok {
		t.Fatalf("get private partition: hit=%v err=%v", ok, err)
	}
	if privateHit.SourceID != privateSourceID || bytes.Equal(privateHit.Content, publicArtifact.Content) {
		t.Fatal("private cache key returned the public entry")
	}

	publicHit, ok, err := cache.Get(context.Background(), netsec.CacheKey{
		SourceID: publicSourceID, ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil || !ok {
		t.Fatalf("get public partition: hit=%v err=%v", ok, err)
	}
	if publicHit.SourceID != publicSourceID {
		t.Fatal("public cache key returned another source")
	}
}

func TestPoisonedCacheHitCannotChangeSource(t *testing.T) {
	publicArtifact := validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)
	privateRepo := &fakeRepository{artifact: validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)}
	publicRepo := &fakeRepository{artifact: publicArtifact}
	cache := &poisonedCache{artifact: publicArtifact}
	resolver := newResolver(t, cache, []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: privateRepo},
	}, &netsec.AuditLog{})

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrSourceConflict) {
		t.Fatalf("resolve error = %v, want ErrSourceConflict", err)
	}
	if publicRepo.Calls() != 0 || privateRepo.Calls() != 0 {
		t.Fatal("poisoned cache hit caused a repository lookup")
	}
}

func TestSameSourceMirrorFallbackIsAudited(t *testing.T) {
	artifact := validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)
	first := &fakeRepository{err: errors.New("mirror transport failure")}
	second := &fakeRepository{artifact: artifact}
	publicRepo := &fakeRepository{artifact: validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)}
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: first},
		{SourceID: privateSourceID, Repository: second},
	}, audit)

	result, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if err != nil {
		t.Fatalf("resolve via same-source mirror: %v", err)
	}
	if !result.FallbackUsed {
		t.Fatal("fallback use was not reported")
	}
	if first.Calls() != 1 || second.Calls() != 1 {
		t.Fatalf("mirror calls = (%d, %d), want (1, 1)", first.Calls(), second.Calls())
	}
	if publicRepo.Calls() != 0 {
		t.Fatalf("fallback crossed to public source (%d call(s))", publicRepo.Calls())
	}
	events := audit.Snapshot()
	if !hasEvent(events, netsec.AuditFallback) || !hasEvent(events, netsec.AuditDegraded) {
		t.Fatal("fallback/degraded events were not recorded")
	}
}

func TestExhaustedPinnedSourceBlocksCrossSourceFallback(t *testing.T) {
	first := &fakeRepository{err: errors.New("private mirror offline")}
	publicRepo := &fakeRepository{artifact: validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)}
	audit := &netsec.AuditLog{}
	resolver := newResolver(t, netsec.NewMemoryCache(), []netsec.RepositoryBinding{
		{SourceID: publicSourceID, Repository: publicRepo},
		{SourceID: privateSourceID, Repository: first},
	}, audit)

	_, err := resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: modulePath, Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrSourceUnavailable) {
		t.Fatalf("resolve error = %v, want ErrSourceUnavailable", err)
	}
	if publicRepo.Calls() != 0 {
		t.Fatal("unavailable private source fell back to the public source")
	}
	if !hasEvent(audit.Snapshot(), netsec.AuditFallbackBlocked) {
		t.Fatal("blocked cross-source fallback was not audited")
	}
}

func TestPolicyRejectsAmbiguousNamespaceSources(t *testing.T) {
	_, err := netsec.NewPolicy(netsec.PolicyConfig{
		Sources: []netsec.Source{
			{ID: privateSourceID, Kind: netsec.SourcePrivate},
			{ID: publicSourceID, Kind: netsec.SourcePublic},
		},
		Bindings: []netsec.NamespaceBinding{
			{Namespace: "corp.example", SourceID: privateSourceID},
			{Namespace: privateNamespace, SourceID: publicSourceID},
		},
	})
	if !errors.Is(err, netsec.ErrInvalidPolicy) {
		t.Fatalf("policy error = %v, want ErrInvalidPolicy", err)
	}
}

func TestUnboundModuleDoesNotProbeAnyRepository(t *testing.T) {
	policy, err := netsec.NewPolicy(netsec.PolicyConfig{
		Sources: []netsec.Source{
			{ID: publicSourceID, Kind: netsec.SourcePublic},
			{ID: privateSourceID, Kind: netsec.SourcePrivate},
		},
		Bindings: []netsec.NamespaceBinding{
			{Namespace: privateNamespace, SourceID: privateSourceID},
		},
	})
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	publicRepo := &fakeRepository{artifact: validArtifact(t, publicSourceID, attackerPrivateKey, "attacker-key", nil)}
	privateRepo := &fakeRepository{artifact: validArtifact(t, privateSourceID, releasePrivateKey, signerKeyID, nil)}
	resolver, err := netsec.NewResolver(netsec.ResolverConfig{
		Policy: policy,
		Repositories: []netsec.RepositoryBinding{
			{SourceID: publicSourceID, Repository: publicRepo},
			{SourceID: privateSourceID, Repository: privateRepo},
		},
		Verifier: netsec.NewEd25519Verifier(map[string]ed25519.PublicKey{
			signerKeyID: releasePublicKey,
		}),
	})
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}

	_, err = resolver.Resolve(context.Background(), netsec.Request{
		ModulePath: "other.example/unbound", Version: moduleVersion,
	})
	if !errors.Is(err, netsec.ErrNoDeclaredSource) {
		t.Fatalf("resolve error = %v, want ErrNoDeclaredSource", err)
	}
	if publicRepo.Calls() != 0 || privateRepo.Calls() != 0 {
		t.Fatal("unbound module probed a repository")
	}
}

func TestPolicyCopiesCannotBeMutated(t *testing.T) {
	policy := newPolicy(t)
	sources := policy.Sources()
	sources[0].ID = "mutated"
	sources[0].Kind = netsec.SourcePrivate
	if got, _ := policy.Source(publicSourceID); got.ID != publicSourceID || got.Kind != netsec.SourcePublic {
		t.Fatalf("source was mutated through accessor: %#v", got)
	}

	bindings := policy.Bindings()
	for index := range bindings {
		if bindings[index].Namespace == privateNamespace {
			bindings[index].SourceID = publicSourceID
		}
	}
	if got, ok := policy.SourceFor(modulePath); !ok || got.ID != privateSourceID {
		t.Fatalf("binding was mutated through accessor: %#v (ok=%v)", got, ok)
	}
}

func newPolicy(t *testing.T) *netsec.Policy {
	t.Helper()
	policy, err := netsec.NewPolicy(netsec.PolicyConfig{
		Sources: []netsec.Source{
			{ID: publicSourceID, Kind: netsec.SourcePublic},
			{ID: privateSourceID, Kind: netsec.SourcePrivate},
		},
		Bindings: []netsec.NamespaceBinding{
			{Namespace: privateNamespace, SourceID: privateSourceID},
			{Namespace: "*", SourceID: publicSourceID},
		},
	})
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	return policy
}

func newResolver(t *testing.T, cache netsec.Cache, repositories []netsec.RepositoryBinding, audit netsec.Auditor) *netsec.Resolver {
	t.Helper()
	resolver, err := netsec.NewResolver(netsec.ResolverConfig{
		Policy:       newPolicy(t),
		Repositories: repositories,
		Cache:        cache,
		Verifier: netsec.NewEd25519Verifier(map[string]ed25519.PublicKey{
			signerKeyID: releasePublicKey,
		}),
		Auditor: audit,
	})
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	return resolver
}

func validArtifact(t *testing.T, sourceID string, key ed25519.PrivateKey, keyID string, metadata map[string]string) netsec.Artifact {
	t.Helper()
	content := moduleZip(t, modulePath, moduleVersion, map[string]string{
		"go.mod":      "module " + modulePath + "\n\ngo 1.24\n",
		"payments.go": "package payments\n\nfunc Pay() string { return \"internal\" }\n",
	})
	artifact := netsec.Artifact{
		SourceID: sourceID, ModulePath: modulePath, Version: moduleVersion,
		Checksum: mustH1(t, content), Content: content, Metadata: metadata,
	}
	artifact.Signature = signArtifact(t, key, keyID, artifact)
	return artifact
}

func signArtifact(t *testing.T, key ed25519.PrivateKey, keyID string, artifact netsec.Artifact) *netsec.Signature {
	t.Helper()
	signature, err := netsec.SignStatement(key, keyID, netsec.SignedStatement{
		SourceID: artifact.SourceID, ModulePath: artifact.ModulePath,
		Version: artifact.Version, Checksum: artifact.Checksum,
	})
	if err != nil {
		t.Fatalf("sign artifact: %v", err)
	}
	return &signature
}

func mustH1(t *testing.T, content []byte) string {
	t.Helper()
	checksum, err := netsec.H1ChecksumZip(content)
	if err != nil {
		t.Fatalf("hash module zip: %v", err)
	}
	return checksum
}

func moduleZip(t *testing.T, module, version string, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	prefix := module + "@" + version + "/"
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(prefix + name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close module zip: %v", err)
	}
	return buffer.Bytes()
}

func hasEvent(events []netsec.AuditEvent, kind netsec.AuditEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

type fakeRepository struct {
	mu       sync.Mutex
	calls    int
	artifact netsec.Artifact
	err      error
}

func (f *fakeRepository) Fetch(_ context.Context, _ netsec.Source, _ netsec.Request) (netsec.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return netsec.Artifact{}, f.err
	}
	return f.artifact, nil
}

func (f *fakeRepository) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type poisonedCache struct {
	artifact netsec.Artifact
}

func (c *poisonedCache) Get(_ context.Context, _ netsec.CacheKey) (netsec.Artifact, bool, error) {
	return c.artifact, true, nil
}

func (c *poisonedCache) Put(_ context.Context, _ netsec.CacheKey, _ netsec.Artifact) error {
	return nil
}
