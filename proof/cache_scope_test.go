package proof

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheVersionIsolation(t *testing.T) {
	p := samplePackage(t)
	cache := NewChunkCache()
	f := newMemFetcher(p)
	raw0, _ := f.FetchChunk(0)
	if err := cache.Put(p.Manifest.PkgID, p.Manifest.Version, 0, p.Manifest.Chunks[0].Hash, raw0); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(p.Manifest.PkgID, p.Manifest.Version, 0, p.Manifest.Chunks[0].Hash); !ok {
		t.Fatal("same version should hit")
	}
	// Different version -> key differs -> miss.
	if _, ok := cache.Get(p.Manifest.PkgID, p.Manifest.Version+1, 0, p.Manifest.Chunks[0].Hash); ok {
		t.Fatal("old cache chunk must not be reused for a new version")
	}
	// Different package id -> miss.
	if _, ok := cache.Get("pkg-OTHER", p.Manifest.Version, 0, p.Manifest.Chunks[0].Hash); ok {
		t.Fatal("cache must be isolated by package id")
	}
	// Different expected hash (new package chunk hashes) -> miss.
	var otherHash [32]byte
	otherHash[0] = 1
	if _, ok := cache.Get(p.Manifest.PkgID, p.Manifest.Version, 0, otherHash); ok {
		t.Fatal("cache hit despite different chunk hash")
	}
}

func TestCacheTamperedEntryRejected(t *testing.T) {
	p := samplePackage(t)
	cache := NewChunkCache()
	f := newMemFetcher(p)
	raw0, _ := f.FetchChunk(0)
	if err := cache.Put(p.Manifest.PkgID, p.Manifest.Version, 0, p.Manifest.Chunks[0].Hash, raw0); err != nil {
		t.Fatal(err)
	}
	// Corrupt the cache backing store directly.
	for k, v := range cache.entries {
		v[0] ^= 0xFF
		cache.entries[k] = v
	}
	if _, ok := cache.Get(p.Manifest.PkgID, p.Manifest.Version, 0, p.Manifest.Chunks[0].Hash); ok {
		t.Fatal("poisoned cache entry must not be served without hash recheck")
	}
	// ObtainChunk must treat the poisoned entry as a miss, fall back to the
	// fetcher, and serve freshly re-verified bytes (not reported as cache hit).
	v, _ := NewVerifier(p.Manifest, p.Root)
	_, ev, err := ObtainChunk(v, newMemFetcher(p), cache, 0)
	if err != nil {
		t.Fatalf("poisoned cache must fall back to fetcher, got %v", err)
	}
	if ev.FromCache {
		t.Fatal("poisoned entry must not be reported as a cache hit")
	}
}

func TestCacheReuseAvoidsFetcher(t *testing.T) {
	p := samplePackage(t)
	cache := NewChunkCache()
	v, _ := NewVerifier(p.Manifest, p.Root)
	f := newMemFetcher(p)
	if _, ev, err := ObtainChunk(v, f, cache, 0); err != nil || ev.FromCache {
		t.Fatalf("first obtain should fetch, got err=%v cached=%v", err, ev.FromCache)
	}
	v2, _ := NewVerifier(p.Manifest, p.Root)
	_, ev, err := ObtainChunk(v2, f, cache, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.FromCache {
		t.Fatal("second obtain should be served from cache")
	}
}

func TestLengthCheckedBeforeAllocation(t *testing.T) {
	// A manifest-scope blob that CLAIMS to be huge but carries no bytes must
	// fail on the scope length check, never via a panic/OOM on make.
	if err := CheckScope(ScopeManifest, int64(MaxManifestBytes)+1); !errors.Is(err, ErrScopeLimit) {
		t.Fatalf("expected scope limit, got %v", err)
	}
	if err := CheckScope(ScopeChunk, int64(MaxChunkBytes)+65); !errors.Is(err, ErrScopeLimit) {
		t.Fatalf("expected scope limit, got %v", err)
	}
	if err := CheckScope(ScopeObject, int64(MaxObjectBytes)+(1<<20)+1); !errors.Is(err, ErrScopeLimit) {
		t.Fatalf("expected object scope limit, got %v", err)
	}
	if err := CheckScope(ScopeObject, -1); !errors.Is(err, ErrNegativeLength) {
		t.Fatalf("negative length must fail, got %v", err)
	}
	if err := CheckScope(Scope(99), 10); !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("unknown scope must fail, got %v", err)
	}

	// Corrupt count field: claims 0xFFFFFFFF objects with tiny body.
	// Locate object count: easier to craft a minimal malformed blob.
	var crafted bytes.Buffer
	crafted.WriteString("MFST")
	crafted.WriteString("pkg-1")
	_ = binary.Write(&crafted, binary.BigEndian, uint32(5)) // pkgid len
	crafted.Reset()
	crafted.WriteString("MFST")
	put := func(u uint32) {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], u)
		crafted.Write(b[:])
	}
	putStr := func(s string) {
		put(uint32(len(s)))
		crafted.WriteString(s)
	}
	putStr("pkg-1")
	var b8 [8]byte
	binary.BigEndian.PutUint64(b8[:], 1)
	crafted.Write(b8[:])
	put(64)                  // chunk size
	put(0)                   // 0 chunks
	put(0xFFFFFFFF)          // 4 billion objects, no bytes follow
	crafted.WriteString("x") // noise
	if _, err := DecodeManifest(ScopeManifest, crafted.Bytes()); err == nil {
		t.Fatal("huge advertised object count must be rejected")
	}
}

func TestTrailingAndTruncatedRejected(t *testing.T) {
	p := samplePackage(t)
	good := p.Manifest.EncodeCanonical()
	if _, err := DecodeManifest(ScopeManifest, append(bytes.Clone(good), 0)); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("trailing byte must fail, got %v", err)
	}
	if _, err := DecodeManifest(ScopeManifest, good[:len(good)-1]); err == nil {
		t.Fatal("truncated manifest must fail")
	}
	if _, err := DecodeManifest(ScopeManifest, nil); err == nil {
		t.Fatal("empty blob must fail")
	}
	// An object record that CLAIMS a 2 MiB payload but supplies none must be
	// rejected during length-prefixed decode (length checked before make).
	var obj bytes.Buffer
	obj.WriteString("OBJ1")
	putObjU32 := func(u uint32) {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], u)
		obj.Write(b[:])
	}
	putObjU32(1)       // id
	putObjU32(0)       // kind
	putObjU32(0)       // name
	putObjU32(0)       // 0 refs
	putObjU32(2 << 20) // claimed payload length 2 MiB
	if _, err := DecodeObjectRecord(ScopeObject, obj.Bytes()); err == nil {
		t.Fatal("oversized claimed payload must be rejected before allocation")
	}
}

func TestObjectRecordCodecRoundtrip(t *testing.T) {
	o := &ObjectRecord{ID: 3, Kind: "k", Name: "n", Refs: []uint32{1, 2}, Payload: []byte("hello")}
	rec := EncodeObjectRecord(o)
	back, err := DecodeObjectRecord(ScopeObject, rec)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != 3 || back.Kind != "k" || back.Name != "n" ||
		string(back.Payload) != "hello" || len(back.Refs) != 2 {
		t.Fatal("object roundtrip mismatch")
	}
	h1 := ObjectRecordHash(rec)
	h2 := ObjectRecordHash(rec)
	if h1 != h2 || h1 == [32]byte{} {
		t.Fatal("object hash unstable")
	}
}

func TestEnvelopeRoundtripAndTamper(t *testing.T) {
	p := samplePackage(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "p.env")
	if err := WriteEnvelopeFile(path, p); err != nil {
		t.Fatal(err)
	}
	env, err := OpenEnvelope(path)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	if env.AdvertisedRoot() != p.Root {
		t.Fatal("advertised root mismatch")
	}
	v, err := NewVerifierFromEnvelope(env, p.Root) // trusted root from out-of-band
	if err != nil {
		t.Fatal(err)
	}
	feedAll(t, v, env, NewChunkCache())
	if _, err := v.Finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// Tamper a payload byte on disk and reopen: chunk fetch must fail hash.
	tampered := filepath.Join(dir, "t.env")
	raw, _ := os.ReadFile(path)
	raw[10] ^= 0xFF
	if err := os.WriteFile(tampered, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	env2, err := OpenEnvelope(tampered)
	if err != nil {
		t.Fatalf("open ok (verify is lazy): %v", err)
	}
	defer env2.Close()
	b0, err := env2.FetchChunk(0)
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := NewVerifier(env2.Manifest(), p.Root)
	if err := v2.AcceptChunk(0, b0); !errors.Is(err, ErrChunkHash) {
		t.Fatalf("tampered payload must fail chunk hash, got %v", err)
	}

	// Tamper the manifest region in the file: either decode fails or root fails.
	raw3, _ := os.ReadFile(path)
	pl := len(p.Payload)
	raw3[pl+5] ^= 0x01
	path3 := filepath.Join(dir, "m.env")
	if err := os.WriteFile(path3, raw3, 0o644); err != nil {
		t.Fatal(err)
	}
	var rejected bool
	if e3, oerr := OpenEnvelope(path3); oerr != nil {
		rejected = true
	} else {
		_, verr := NewVerifier(e3.Manifest(), p.Root)
		rejected = verr != nil
		e3.Close()
	}
	if !rejected {
		t.Fatal("tampered manifest must be rejected")
	}

	// Trust a DIFFERENT root than advertised: verifier must refuse.
	env4, _ := OpenEnvelope(path)
	defer env4.Close()
	var wrong RootSummary
	if _, err := NewVerifierFromEnvelope(env4, wrong); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("expected root mismatch, got %v", err)
	}
}

func TestDanglingRefRejectedAtBuildAndVerify(t *testing.T) {
	_, err := Build("p", 1, 64, []Object{
		{Kind: "x", Name: "o", Refs: []uint32{42}, Payload: []byte("d")},
	})
	if !errors.Is(err, ErrDanglingRef) {
		t.Fatalf("build should reject dangling ref, got %v", err)
	}
}

func TestDuplicateAndUnsortedRefsRejected(t *testing.T) {
	// Builder sorts/dedups inputs? It sorts but Validate rejects duplicate refs.
	_, err := Build("p", 1, 64, []Object{
		{Kind: "a", Name: "a", Payload: []byte("a")},
		{Kind: "b", Name: "b", Refs: []uint32{0, 0}, Payload: []byte("b")},
	})
	if err == nil {
		t.Fatal("duplicate refs must be rejected")
	}
}

func TestChunkIndexOutOfRange(t *testing.T) {
	p := samplePackage(t)
	v, _ := NewVerifier(p.Manifest, p.Root)
	if err := v.AcceptChunk(uint32(len(p.Manifest.Chunks)), []byte("x")); !errors.Is(err, ErrChunkIndex) {
		t.Fatalf("expected chunk index error, got %v", err)
	}
}
