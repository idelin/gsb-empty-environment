package netsec_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"example.org/netsec/q0475"
)

func certificateMessage(t *testing.T, certificate []byte) []byte {
	t.Helper()
	message, err := netsec.MarshalCertificateMessage(nil, [][]byte{certificate})
	if err != nil {
		t.Fatalf("MarshalCertificateMessage: %v", err)
	}
	return message
}

func compressibleCertificate() []byte {
	return bytes.Repeat([]byte("test-certificate-chain-"), 128)
}

func TestOnlyOfferedAlgorithmIsUsed(t *testing.T) {
	server := netsec.NewServer()
	chain := certificateMessage(t, compressibleCertificate())

	unsupportedOffer := server.NewConnection("tenant-a", "a.example")
	result, err := unsupportedOffer.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZstd}, netsec.NoDictionary)
	if err != nil {
		t.Fatalf("CompressCertificate with unoffered algorithm: %v", err)
	}
	if result.Compressed {
		t.Fatal("compressed certificate sent for an algorithm the client did not offer as supported")
	}
	if result.HandshakeType != netsec.HandshakeTypeCertificate {
		t.Fatalf("handshake type = %d, want Certificate(%d)", result.HandshakeType, netsec.HandshakeTypeCertificate)
	}
	if !bytes.Equal(result.Body, chain) {
		t.Fatal("fallback did not preserve the uncompressed certificate message")
	}
	if unsupportedOffer.SentCompressedCertificate() {
		t.Fatal("fallback incorrectly consumed the compressed-certificate allowance")
	}

	mixedOffer := server.NewConnection("tenant-a", "a.example")
	result, err = mixedOffer.CompressCertificate(
		chain,
		[]netsec.Algorithm{netsec.AlgorithmZstd, netsec.AlgorithmZlib},
		netsec.NoDictionary,
	)
	if err != nil {
		t.Fatalf("CompressCertificate with zlib offered: %v", err)
	}
	if !result.Compressed || result.Algorithm != netsec.AlgorithmZlib {
		t.Fatalf("compressed = %v, algorithm = %d, want zlib", result.Compressed, result.Algorithm)
	}
}

type fixedCodec struct {
	output byte
}

func (codec fixedCodec) Compress(_, _ []byte) ([]byte, error) {
	return []byte{codec.output}, nil
}

func (codec fixedCodec) Decompress(_, _ []byte, _ int) ([]byte, error) {
	return nil, errors.New("fixedCodec cannot decompress")
}

func TestSupportedButUnlistedAlgorithmIsNotSelected(t *testing.T) {
	codecs := netsec.NewCodecRegistry()
	if err := codecs.Register(netsec.AlgorithmZstd, fixedCodec{output: 0x5a}); err != nil {
		t.Fatalf("register zstd test codec: %v", err)
	}
	server := netsec.NewServer(netsec.ServerConfig{
		Codecs:     codecs,
		Preference: []netsec.Algorithm{netsec.AlgorithmZstd, netsec.AlgorithmZlib},
	})
	chain := certificateMessage(t, compressibleCertificate())

	zlibOnly, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, netsec.NoDictionary)
	if err != nil {
		t.Fatalf("zlib-only compression: %v", err)
	}
	if !zlibOnly.Compressed || zlibOnly.Algorithm != netsec.AlgorithmZlib {
		t.Fatalf("compressed = %v, algorithm = %d, want zlib although zstd is server-preferred", zlibOnly.Compressed, zlibOnly.Algorithm)
	}

	zstdOnly, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZstd}, netsec.NoDictionary)
	if err != nil {
		t.Fatalf("zstd-only compression: %v", err)
	}
	if !zstdOnly.Compressed || zstdOnly.Algorithm != netsec.AlgorithmZstd {
		t.Fatalf("compressed = %v, algorithm = %d, want the offered zstd codec", zstdOnly.Compressed, zstdOnly.Algorithm)
	}

	notOffered, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmBrotli}, netsec.NoDictionary)
	if err != nil {
		t.Fatalf("unavailable algorithm compression: %v", err)
	}
	if notOffered.Compressed || notOffered.HandshakeType != netsec.HandshakeTypeCertificate {
		t.Fatal("server used an algorithm that the client did not list")
	}
}

func TestCrossTenantDictionaryRejected(t *testing.T) {
	registry := netsec.NewDictionaryRegistry()
	dictionaryA := []byte("tenant-a certificate dictionary")
	dictionaryB := []byte("tenant-b different dictionary")
	if err := registry.Register("tenant-a", "a.example", 7, dictionaryA); err != nil {
		t.Fatalf("register tenant A dictionary: %v", err)
	}
	if err := registry.Register("tenant-b", "b.example", 8, dictionaryB); err != nil {
		t.Fatalf("register tenant B dictionary: %v", err)
	}
	if err := registry.Register("tenant-b", "b.example", 7, dictionaryB); !errors.Is(err, netsec.ErrDictionaryIDConflict) {
		t.Fatalf("reusing a dictionary ID across tenants returned %v, want ErrDictionaryIDConflict", err)
	}

	server := netsec.NewServer(netsec.ServerConfig{Dictionaries: registry})
	chain := certificateMessage(t, compressibleCertificate())

	attacker := server.NewConnection("tenant-b", "b.example")
	if _, err := attacker.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 7); !errors.Is(err, netsec.ErrCrossTenantDictionary) {
		t.Fatalf("cross-tenant dictionary use returned %v, want ErrCrossTenantDictionary", err)
	}
	if server.Cache.Len() != 0 {
		t.Fatal("cross-tenant attempt populated the compression cache")
	}

	wrongSNI := server.NewConnection("tenant-b", "a.example")
	if _, err := wrongSNI.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 7); !errors.Is(err, netsec.ErrCrossTenantDictionary) {
		t.Fatalf("dictionary use through another tenant's SNI returned %v, want ErrCrossTenantDictionary", err)
	}

	if _, err := registry.Lookup("tenant-b", "b.example", 7); !errors.Is(err, netsec.ErrCrossTenantDictionary) {
		t.Fatalf("registry lookup across tenants returned %v, want ErrCrossTenantDictionary", err)
	}

	owner := server.NewConnection("tenant-a", "a.example")
	result, err := owner.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 7)
	if err != nil {
		t.Fatalf("owner dictionary use failed: %v", err)
	}
	if !result.Compressed {
		t.Fatal("owner dictionary did not produce a compressed certificate")
	}
}

func TestUncompressedLengthBoundsAndMismatch(t *testing.T) {
	registry := netsec.NewDictionaryRegistry()
	dictionary := []byte("shared dictionary for length tests")
	if err := registry.Register("tenant-a", "a.example", 11, dictionary); err != nil {
		t.Fatalf("register dictionary: %v", err)
	}
	server := netsec.NewServer(netsec.ServerConfig{Dictionaries: registry})
	connection := server.NewConnection("tenant-a", "a.example")
	chain := certificateMessage(t, compressibleCertificate())

	result, err := connection.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 11)
	if err != nil {
		t.Fatalf("CompressCertificate: %v", err)
	}
	if !result.Compressed {
		t.Fatal("test requires a compressed certificate")
	}
	message, err := netsec.ParseCompressedCertificate(result.Body)
	if err != nil {
		t.Fatalf("ParseCompressedCertificate: %v", err)
	}
	plain, err := server.DecompressCertificate("tenant-a", "a.example", 11, message)
	if err != nil {
		t.Fatalf("DecompressCertificate: %v", err)
	}
	if !bytes.Equal(plain, chain) {
		t.Fatal("decompressed certificate does not match input")
	}

	for _, declared := range []uint32{message.UncompressedLength - 1, message.UncompressedLength + 1} {
		mutated := *message
		mutated.UncompressedLength = declared
		body, err := mutated.Marshal()
		if err != nil {
			t.Fatalf("marshal mutation %d: %v", declared, err)
		}
		parsed, err := netsec.ParseCompressedCertificate(body)
		if err != nil {
			t.Fatalf("parse mutation %d: %v", declared, err)
		}
		_, err = server.DecompressCertificate("tenant-a", "a.example", 11, parsed)
		if !errors.Is(err, netsec.ErrBadCertificate) {
			t.Fatalf("declared length %d returned %v, want bad_certificate", declared, err)
		}
		if !errors.Is(err, netsec.ErrUncompressedLengthMismatch) {
			t.Fatalf("declared length %d returned %v, want ErrUncompressedLengthMismatch", declared, err)
		}
	}

	tooLarge := &netsec.CompressedCertificate{
		Algorithm:          message.Algorithm,
		UncompressedLength: netsec.MaxUncompressedLength + 1,
		Compressed:         message.Compressed,
	}
	if _, err := tooLarge.Marshal(); !errors.Is(err, netsec.ErrBadCertificate) || !errors.Is(err, netsec.ErrUncompressedLengthTooLarge) {
		t.Fatalf("oversized declaration marshal returned %v, want bad_certificate and length error", err)
	}
	if _, err := netsec.DecompressCertificate(tooLarge, dictionary); !errors.Is(err, netsec.ErrBadCertificate) {
		t.Fatalf("oversized declaration decompression returned %v, want bad_certificate", err)
	}

	// A yawning declaration must be rejected without expanding the stream to
	// that size. 2^24-1 is the maximum the field can represent.
	huge := *message
	huge.UncompressedLength = netsec.MaxUncompressedLength
	if _, err := netsec.DecompressCertificate(&huge, dictionary); !errors.Is(err, netsec.ErrBadCertificate) {
		t.Fatalf("maximum declaration with short stream returned %v, want bad_certificate", err)
	}
}

func TestCompressionNotSmallerFallsBack(t *testing.T) {
	randomCertificate := make([]byte, 4096)
	if _, err := rand.Read(randomCertificate); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	chain := certificateMessage(t, randomCertificate)

	server := netsec.NewServer()
	connection := server.NewConnection("tenant-a", "a.example")
	result, err := connection.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, netsec.NoDictionary)
	if err != nil {
		t.Fatalf("CompressCertificate: %v", err)
	}
	if result.Compressed {
		t.Fatalf("compressed output was not smaller: input %d, compressed message %d", len(chain), len(result.Body))
	}
	if result.HandshakeType != netsec.HandshakeTypeCertificate || !bytes.Equal(result.Body, chain) {
		t.Fatal("non-shrinking result did not fall back to the uncompressed certificate")
	}
	if connection.SentCompressedCertificate() {
		t.Fatal("uncompressed fallback consumed the compressed-certificate allowance")
	}
	if server.Cache.Len() != 0 {
		t.Fatal("non-shrinking compression was cached")
	}
}

func TestOneCompressedCertificatePerConnection(t *testing.T) {
	server := netsec.NewServer()
	connection := server.NewConnection("tenant-a", "a.example")
	chain := certificateMessage(t, compressibleCertificate())

	result, err := connection.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, netsec.NoDictionary)
	if err != nil {
		t.Fatalf("first CompressCertificate: %v", err)
	}
	if !result.Compressed || !connection.SentCompressedCertificate() {
		t.Fatal("first certificate was not compressed")
	}

	_, err = connection.CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, netsec.NoDictionary)
	if !errors.Is(err, netsec.ErrCompressedCertificateAlreadySent) {
		t.Fatalf("second compressed certificate returned %v, want ErrCompressedCertificateAlreadySent", err)
	}
}

func TestCacheKeysIsolateDictionaryAndChain(t *testing.T) {
	registry := netsec.NewDictionaryRegistry()
	if err := registry.Register("tenant-a", "a.example", 1, []byte("dictionary one")); err != nil {
		t.Fatalf("register dictionary one: %v", err)
	}
	if err := registry.Register("tenant-a", "a.example", 2, []byte("dictionary two")); err != nil {
		t.Fatalf("register dictionary two: %v", err)
	}
	cache := netsec.NewCompressionCache(8)
	server := netsec.NewServer(netsec.ServerConfig{Dictionaries: registry, Cache: cache})
	chain := certificateMessage(t, compressibleCertificate())

	first, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 1)
	if err != nil {
		t.Fatalf("first compression: %v", err)
	}
	if first.CacheHit {
		t.Fatal("first compression unexpectedly hit the cache")
	}

	second, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 1)
	if err != nil {
		t.Fatalf("second compression: %v", err)
	}
	if !second.CacheHit {
		t.Fatal("second identical compression did not hit the cache")
	}
	if !bytes.Equal(first.Body, second.Body) {
		t.Fatal("cache hit returned different compressed output")
	}

	differentDictionary, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(chain, []netsec.Algorithm{netsec.AlgorithmZlib}, 2)
	if err != nil {
		t.Fatalf("compression with second dictionary: %v", err)
	}
	if differentDictionary.CacheHit {
		t.Fatal("dictionary ID 2 shared dictionary ID 1's cache entry")
	}
	if !differentDictionary.Compressed {
		t.Fatal("dictionary ID 2 did not produce a compressed certificate")
	}

	otherChain := certificateMessage(t, append(compressibleCertificate(), []byte("-other")...))
	differentChain, err := server.NewConnection("tenant-a", "a.example").
		CompressCertificate(otherChain, []netsec.Algorithm{netsec.AlgorithmZlib}, 1)
	if err != nil {
		t.Fatalf("compression with second chain: %v", err)
	}
	if differentChain.CacheHit {
		t.Fatal("different certificate chain shared a cache entry")
	}
	if len(first.Body) == 0 || len(differentDictionary.Body) == 0 || len(differentChain.Body) == 0 {
		t.Fatal("empty compressed result")
	}
}

func TestExtensionRoundTripAndValidation(t *testing.T) {
	algorithms := []netsec.Algorithm{netsec.AlgorithmZstd, netsec.AlgorithmZlib, 0x1234}
	body, err := netsec.MarshalCompressCertificateExtension(algorithms)
	if err != nil {
		t.Fatalf("marshal algorithm extension: %v", err)
	}
	wantWire := []byte{0x06, 0x00, 0x03, 0x00, 0x01, 0x12, 0x34}
	if !bytes.Equal(body, wantWire) {
		t.Fatalf("algorithm extension wire = %x, want %x", body, wantWire)
	}
	parsed, err := netsec.ParseCompressCertificateExtension(body)
	if err != nil {
		t.Fatalf("parse algorithm extension: %v", err)
	}
	if len(parsed) != len(algorithms) {
		t.Fatalf("parsed %d algorithms, want %d", len(parsed), len(algorithms))
	}
	for index := range algorithms {
		if parsed[index] != algorithms[index] {
			t.Fatalf("algorithm %d = %d, want %d", index, parsed[index], algorithms[index])
		}
	}
	for _, invalid := range [][]byte{
		nil,
		{},
		{0x00},
		{0x01, 0x00},
		{0x03, 0x00, 0x01},
		{0x04, 0x00, 0x01, 0x00},
	} {
		if _, err := netsec.ParseCompressCertificateExtension(invalid); !errors.Is(err, netsec.ErrInvalidExtension) {
			t.Fatalf("invalid extension length %d returned %v", len(invalid), err)
		}
	}

	dictionaryIDs := []netsec.DictionaryID{1, 65535}
	dictionaryBody, err := netsec.MarshalCertificateCompressionDictionaryExtension(dictionaryIDs)
	if err != nil {
		t.Fatalf("marshal dictionary extension: %v", err)
	}
	parsedIDs, err := netsec.ParseCertificateCompressionDictionaryExtension(dictionaryBody)
	if err != nil {
		t.Fatalf("parse dictionary extension: %v", err)
	}
	if len(parsedIDs) != len(dictionaryIDs) {
		t.Fatalf("parsed %d dictionary IDs, want %d", len(parsedIDs), len(dictionaryIDs))
	}
	if _, err := netsec.MarshalCertificateCompressionDictionaryExtension([]netsec.DictionaryID{netsec.NoDictionary}); !errors.Is(err, netsec.ErrInvalidExtension) {
		t.Fatalf("reserved dictionary ID returned %v", err)
	}
}

func TestDictionaryExtensionUsesSNITenant(t *testing.T) {
	registry := netsec.NewDictionaryRegistry()
	if err := registry.Register("tenant-a", "a.example", 1, []byte("tenant-a dictionary")); err != nil {
		t.Fatalf("register tenant A dictionary: %v", err)
	}
	if err := registry.Register("tenant-b", "b.example", 2, []byte("tenant-b dictionary")); err != nil {
		t.Fatalf("register tenant B dictionary: %v", err)
	}
	server := netsec.NewServer(netsec.ServerConfig{Dictionaries: registry})
	chain := certificateMessage(t, compressibleCertificate())
	algorithms, err := netsec.MarshalCompressCertificateExtension([]netsec.Algorithm{netsec.AlgorithmZlib})
	if err != nil {
		t.Fatalf("marshal algorithm extension: %v", err)
	}
	tenantADictionary, err := netsec.MarshalCertificateCompressionDictionaryExtension([]netsec.DictionaryID{1})
	if err != nil {
		t.Fatalf("marshal dictionary extension: %v", err)
	}
	tenantBDictionary, err := netsec.MarshalCertificateCompressionDictionaryExtension([]netsec.DictionaryID{2})
	if err != nil {
		t.Fatalf("marshal dictionary extension: %v", err)
	}

	// An empty tenant is derived from the registry's SNI binding.
	owner := server.NewConnection("", "a.example")
	result, err := owner.CompressCertificateWithExtensions(chain, algorithms, tenantADictionary)
	if err != nil {
		t.Fatalf("owner dictionary extension failed: %v", err)
	}
	if !result.Compressed || result.DictionaryID != 1 {
		t.Fatalf("compressed = %v, dictionary ID = %d, want compressed with ID 1", result.Compressed, result.DictionaryID)
	}

	attacker := server.NewConnection("tenant-a", "a.example")
	if _, err := attacker.CompressCertificateWithExtensions(chain, algorithms, tenantBDictionary); !errors.Is(err, netsec.ErrCrossTenantDictionary) {
		t.Fatalf("other tenant's dictionary extension returned %v, want ErrCrossTenantDictionary", err)
	}

	noDictionary := server.NewConnection("tenant-a", "a.example")
	result, err = noDictionary.CompressCertificateWithExtensions(chain, algorithms, nil)
	if err != nil {
		t.Fatalf("dictionary-less extension compression failed: %v", err)
	}
	if !result.Compressed || result.DictionaryID != netsec.NoDictionary {
		t.Fatalf("dictionary-less compression returned compressed = %v, ID = %d", result.Compressed, result.DictionaryID)
	}
}

func TestCertificateMessageRoundTrip(t *testing.T) {
	requestContext := []byte("context")
	entries := []netsec.CertificateEntry{
		{Certificate: []byte("leaf"), Extensions: []byte{0x00, 0x01}},
		{Certificate: []byte("intermediate")},
	}
	body, err := netsec.MarshalCertificateMessageEntries(requestContext, entries)
	if err != nil {
		t.Fatalf("marshal certificate message: %v", err)
	}
	parsed, err := netsec.ParseCertificateMessage(body)
	if err != nil {
		t.Fatalf("parse certificate message: %v", err)
	}
	if !bytes.Equal(parsed.RequestContext, requestContext) {
		t.Fatalf("request context = %q, want %q", parsed.RequestContext, requestContext)
	}
	if len(parsed.Certificates) != len(entries) {
		t.Fatalf("parsed %d certificates, want %d", len(parsed.Certificates), len(entries))
	}
	for index := range entries {
		if !bytes.Equal(parsed.Certificates[index].Certificate, entries[index].Certificate) {
			t.Fatalf("certificate %d mismatch", index)
		}
		if !bytes.Equal(parsed.Certificates[index].Extensions, entries[index].Extensions) {
			t.Fatalf("extensions %d mismatch", index)
		}
	}
}
