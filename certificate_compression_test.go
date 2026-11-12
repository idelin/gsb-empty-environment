package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func testChain(size int) []byte {
	unit := []byte("certificate-chain-data-")
	chain := make([]byte, 0, size)
	for len(chain) < size {
		chain = append(chain, unit...)
	}
	return chain[:size]
}

func compressibleChain() []byte {
	return testChain(96 << 10)
}

func decodeCompressed(t *testing.T, wire []byte, dictionary []byte) []byte {
	t.Helper()
	var message CompressedCertificate
	if err := message.UnmarshalBinary(wire); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	codec, ok := DefaultRegistry().Lookup(message.Algorithm)
	if !ok {
		t.Fatalf("no codec for algorithm %d", message.Algorithm)
	}
	plaintext, err := DecompressCertificate(message, codec, dictionary)
	if err != nil {
		t.Fatalf("DecompressCertificate: %v", err)
	}
	return plaintext
}

func TestOnlyClientListedAlgorithm(t *testing.T) {
	server := NewServer(ServerConfig{})
	chain := compressibleChain()

	connection, err := server.NewConnection(ClientOffer{
		SNI:        "tenant-a.example",
		Algorithms: []Algorithm{AlgorithmBrotli},
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	wire, compressed, err := connection.SendCertificate(chain)
	if err != nil {
		t.Fatalf("SendCertificate: %v", err)
	}
	if compressed {
		t.Fatal("server used an algorithm the client did not list")
	}
	if !bytes.Equal(wire, chain) {
		t.Fatal("plaintext fallback changed certificate bytes")
	}

	connection, err = server.NewConnection(ClientOffer{
		SNI:        "tenant-a.example",
		Algorithms: []Algorithm{AlgorithmZlib},
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	wire, compressed, err = connection.SendCertificate(chain)
	if err != nil {
		t.Fatalf("SendCertificate: %v", err)
	}
	if !compressed {
		t.Fatal("server did not use the client-listed zlib algorithm")
	}
	if got := decodeCompressed(t, wire, nil); !bytes.Equal(got, chain) {
		t.Fatal("compressed certificate did not round-trip")
	}
}

func TestCrossTenantDictionaryRejected(t *testing.T) {
	store := NewDictionaryStore()
	if err := store.Register("tenant-b.example", "tenant-b-dictionary", []byte("tenant-b-secret")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	server := NewServer(ServerConfig{Dictionaries: store})
	_, err := server.NewConnection(ClientOffer{
		SNI:           "tenant-a.example",
		Algorithms:    []Algorithm{AlgorithmZlib},
		DictionaryIDs: []string{"tenant-b-dictionary"},
	})
	if !errors.Is(err, ErrCrossTenantDictionary) {
		t.Fatalf("got %v, want ErrCrossTenantDictionary", err)
	}
}

func TestDictionarySelectedWithinTenantAndRoundTrips(t *testing.T) {
	store := NewDictionaryStore()
	dictionary := bytes.Repeat([]byte("tenant-a-dictionary-"), 64)
	if err := store.Register("TENANT-A.EXAMPLE.", "a-dictionary", dictionary); err != nil {
		t.Fatalf("Register: %v", err)
	}

	server := NewServer(ServerConfig{Dictionaries: store})
	connection, err := server.NewConnection(ClientOffer{
		SNI:           "tenant-a.example",
		Algorithms:    []Algorithm{AlgorithmZlib},
		DictionaryIDs: []string{"a-dictionary"},
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}

	chain := testChain(8 << 10)
	wire, compressed, err := connection.SendCertificate(chain)
	if err != nil {
		t.Fatalf("SendCertificate: %v", err)
	}
	if !compressed {
		t.Fatal("dictionary-compressed chain was not compressed")
	}
	if got := decodeCompressed(t, wire, dictionary); !bytes.Equal(got, chain) {
		t.Fatal("dictionary-compressed certificate did not round-trip")
	}
}

func TestUnknownDictionaryFallsBackToPlaintext(t *testing.T) {
	store := NewDictionaryStore()
	server := NewServer(ServerConfig{Dictionaries: store})
	connection, err := server.NewConnection(ClientOffer{
		SNI:           "tenant-a.example",
		Algorithms:    []Algorithm{AlgorithmZlib},
		DictionaryIDs: []string{"missing"},
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	chain := compressibleChain()
	wire, compressed, err := connection.SendCertificate(chain)
	if err != nil {
		t.Fatalf("SendCertificate: %v", err)
	}
	if compressed {
		t.Fatal("server compressed with an unselected dictionary")
	}
	if !bytes.Equal(wire, chain) {
		t.Fatal("plaintext fallback changed certificate bytes")
	}
}

func TestSameDictionaryIDIsTenantScopedInCache(t *testing.T) {
	store := NewDictionaryStore()
	if err := store.Register("tenant-a.example", "shared-id", []byte("AAAAAAAAAAAAAAAA")); err != nil {
		t.Fatalf("Register tenant-a: %v", err)
	}
	if err := store.Register("tenant-b.example", "shared-id", []byte("BBBBBBBBBBBBBBBB")); err != nil {
		t.Fatalf("Register tenant-b: %v", err)
	}

	cache := NewCertificateCache(8, 1<<20)
	key := NewCacheKey(AlgorithmZlib, "shared-id", []byte("same-chain"))
	cache.Put("tenant-a.example", key, []byte("tenant-a-compressed"))
	cache.Put("tenant-b.example", key, []byte("tenant-b-compressed"))

	gotA, ok := cache.Get("tenant-a.example", key)
	if !ok || string(gotA) != "tenant-a-compressed" {
		t.Fatalf("tenant A got cache entry %q", gotA)
	}
	gotB, ok := cache.Get("tenant-b.example", key)
	if !ok || string(gotB) != "tenant-b-compressed" {
		t.Fatalf("tenant B got cache entry %q", gotB)
	}
}

func TestDeclaredLengthMustMatchExactly(t *testing.T) {
	codec, _ := DefaultRegistry().Lookup(AlgorithmZlib)
	chain := testChain(4096)
	payload, err := codec.Compress(chain, nil)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}

	for _, declared := range []uint32{uint32(len(chain) - 1), uint32(len(chain) + 1)} {
		message := CompressedCertificate{
			Algorithm:          AlgorithmZlib,
			UncompressedLength: declared,
			Compressed:         payload,
		}
		if _, err := DecompressCertificate(message, codec, nil); !errors.Is(err, ErrBadCertificate) {
			t.Fatalf("declared %d, got %v, want ErrBadCertificate", declared, err)
		}
	}
}

func TestDeclaredLengthAbove24BitMaximum(t *testing.T) {
	codec, _ := DefaultRegistry().Lookup(AlgorithmZlib)
	payload, err := codec.Compress(testChain(1024), nil)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	message := CompressedCertificate{
		Algorithm:          AlgorithmZlib,
		UncompressedLength: MaxUncompressedLength + 1,
		Compressed:         payload,
	}
	_, err = DecompressCertificate(message, codec, nil)
	if !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("got %v, want ErrBadCertificate", err)
	}
	if got := AlertForError(err); got != AlertBadCertificate {
		t.Fatalf("alert = %d, want %d", got, AlertBadCertificate)
	}
	if _, err := message.MarshalBinary(); !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("MarshalBinary got %v, want ErrBadCertificate", err)
	}
}

func TestCompressionLargerFallsBackToPlaintext(t *testing.T) {
	server := NewServer(ServerConfig{})
	connection, err := server.NewConnection(ClientOffer{
		SNI:        "tenant-a.example",
		Algorithms: []Algorithm{AlgorithmZlib},
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}

	chain := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	wire, compressed, err := connection.SendCertificate(chain)
	if err != nil {
		t.Fatalf("SendCertificate: %v", err)
	}
	if compressed {
		t.Fatal("sent compression even though it was not smaller than plaintext")
	}
	if !bytes.Equal(wire, chain) {
		t.Fatal("fallback did not return the original certificate bytes")
	}
}

func TestCompressedCertificateOnlyOncePerConnection(t *testing.T) {
	server := NewServer(ServerConfig{})
	connection, err := server.NewConnection(ClientOffer{
		SNI:        "tenant-a.example",
		Algorithms: []Algorithm{AlgorithmZlib},
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	chain := compressibleChain()
	if _, compressed, err := connection.SendCertificate(chain); err != nil || !compressed {
		t.Fatalf("first SendCertificate: compressed=%v err=%v", compressed, err)
	}
	if _, _, err := connection.SendCertificate(chain); !errors.Is(err, ErrCompressedCertificateAlreadySent) {
		t.Fatalf("second SendCertificate got %v, want ErrCompressedCertificateAlreadySent", err)
	}
}

func TestCacheKeyUsesChainSHA256(t *testing.T) {
	chain := []byte("chain")
	key := NewCacheKey(AlgorithmZlib, "dict", chain)
	if key.Algorithm != AlgorithmZlib || key.DictionaryID != "dict" {
		t.Fatalf("unexpected key: %+v", key)
	}
	if key.ChainSHA256 != sha256.Sum256(chain) {
		t.Fatal("cache key does not contain SHA-256 of chain bytes")
	}
}
