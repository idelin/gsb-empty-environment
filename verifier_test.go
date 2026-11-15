package netsec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

type testSourceEntry struct {
	path    string
	digest  [sha256.Size]byte
	content io.Reader
}

type testSource struct {
	entries   []testSourceEntry
	index     int
	nextErr   error
	nextCalls int
	reads     int
}

func (source *testSource) Next() (Entry, error) {
	source.nextCalls++
	if source.index >= len(source.entries) {
		if source.nextErr != nil {
			err := source.nextErr
			source.nextErr = nil
			return Entry{}, err
		}
		return Entry{}, io.EOF
	}

	entry := source.entries[source.index]
	source.index++
	if entry.content == nil {
		entry.content = bytes.NewReader(nil)
	}
	return Entry{
		Path:           entry.path,
		DeclaredSHA256: entry.digest,
		Content:        &countingReader{reader: iotest.OneByteReader(entry.content), reads: &source.reads},
	}, nil
}

type countingReader struct {
	reader io.Reader
	reads  *int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	*reader.reads++
	return reader.reader.Read(buffer)
}

func TestVerifyAndInstallValidPackage(t *testing.T) {
	t.Parallel()

	contents := map[string][]byte{
		"bin/tool":        []byte("#!/bin/sh\necho trusted\n"),
		"docs/résumé.txt": []byte("unicode path"),
		"empty":           {},
	}
	manifestEntries := make([]ManifestEntry, 0, len(contents))
	for path, data := range contents {
		manifestEntries = append(manifestEntries, manifestEntryFor(path, data))
	}
	publicKey, manifest, signature := testSignedManifest(t, manifestEntries)

	source := &testSource{entries: []testSourceEntry{
		testEntryFor("docs/./résumé.txt", contents["docs/résumé.txt"]),
		testEntryFor("empty/../empty", contents["empty"]),
		testEntryFor("bin//tool", contents["bin/tool"]),
	}}

	installs := 0
	err := VerifyAndInstall(
		context.Background(),
		publicKey,
		manifest,
		signature,
		source,
		Limits{MaxFileBytes: 128, MaxTotalBytes: 256},
		func(files map[string][]byte) error {
			installs++
			if len(files) != len(contents) {
				t.Fatalf("installed %d files, want %d", len(files), len(contents))
			}
			for path, want := range contents {
				if !bytes.Equal(files[path], want) {
					t.Fatalf("installed %q = %q, want %q", path, files[path], want)
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("VerifyAndInstall() error = %v", err)
	}
	if installs != 1 {
		t.Fatalf("install callback count = %d, want 1", installs)
	}
	if source.reads <= len(contents) {
		t.Fatalf("source reads = %d, verifier did not consume the stream in chunks", source.reads)
	}
}

func TestVerifyRejectsDuplicateNameOverwrite(t *testing.T) {
	t.Parallel()

	safe := []byte("signed release binary")
	evil := []byte("attacker replacement")
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("bin/tool", safe)})
	source := &testSource{entries: []testSourceEntry{
		testEntryFor("bin/tool", safe),
		testEntryFor("bin/tool", evil),
	}}

	assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrDuplicateEntry)
}

func TestVerifyRejectsNormalizationCollision(t *testing.T) {
	t.Parallel()

	content := []byte("signed")
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("etc/config", content)})
	source := &testSource{entries: []testSourceEntry{
		testEntryFor("etc/../etc/config", content),
		testEntryFor("etc//config", content),
	}}

	assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrDuplicateEntry)
}

func TestVerifyAcceptsZeroByteFileWithZeroLimits(t *testing.T) {
	t.Parallel()

	empty := []byte{}
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("empty", empty)})
	source := &testSource{entries: []testSourceEntry{testEntryFor("empty", empty)}}

	installs := 0
	err := VerifyAndInstall(
		context.Background(),
		publicKey,
		manifest,
		signature,
		source,
		Limits{MaxFileBytes: 0, MaxTotalBytes: 0},
		func(files map[string][]byte) error {
			installs++
			data, exists := files["empty"]
			if !exists || len(data) != 0 {
				t.Fatalf("installed mapping = %#v, want empty file", files)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("VerifyAndInstall() error = %v", err)
	}
	if installs != 1 {
		t.Fatalf("install callback count = %d, want 1", installs)
	}
}

func TestVerifyRejectsTruncatedStream(t *testing.T) {
	t.Parallel()

	full := []byte("complete payload")
	declared := sha256.Sum256(full)
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{{Path: "payload", SHA256: declared}})

	tests := []struct {
		name    string
		content io.Reader
		wantErr error
	}{
		{
			name:    "short content",
			content: bytes.NewReader(full[:len(full)-3]),
			wantErr: ErrDigestMismatch,
		},
		{
			name:    "reader reports truncation",
			content: io.MultiReader(bytes.NewReader(full[:len(full)-3]), iotest.ErrReader(io.ErrUnexpectedEOF)),
			wantErr: ErrStream,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := &testSource{entries: []testSourceEntry{{
				path:    "payload",
				digest:  declared,
				content: test.content,
			}}}
			assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, test.wantErr)
		})
	}
}

func TestVerifyRejectsInvalidSignatureBeforeReading(t *testing.T) {
	t.Parallel()

	content := []byte("payload")
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("payload", content)})
	manifest[len(manifest)-1] ^= 0xff
	source := &testSource{entries: []testSourceEntry{testEntryFor("payload", content)}}

	assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrInvalidSignature)
	if source.nextCalls != 0 {
		t.Fatalf("source Next calls = %d, want 0 for invalid signature", source.nextCalls)
	}
}

func TestVerifyRejectsValidlySignedDuplicateManifestBeforeReading(t *testing.T) {
	t.Parallel()

	content := []byte("payload")
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x5a}, ed25519.SeedSize))
	manifest := append(
		testRecord("payload", sha256.Sum256(content)),
		testRecord("payload", sha256.Sum256([]byte("different")))...,
	)
	signature := ed25519.Sign(privateKey, manifest)
	source := &testSource{}

	assertRejectedBeforeInstall(
		t,
		privateKey.Public().(ed25519.PublicKey),
		manifest,
		signature,
		source,
		Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024},
		ErrDuplicateManifestPath,
	)
	if source.nextCalls != 0 {
		t.Fatalf("source Next calls = %d, want 0 for duplicate manifest", source.nextCalls)
	}
}

func TestVerifyRejectsEntryDeclaredDigestMismatch(t *testing.T) {
	t.Parallel()

	content := []byte("payload")
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("payload", content)})
	source := &testSource{entries: []testSourceEntry{{
		path:    "payload",
		digest:  sha256.Sum256([]byte("different")),
		content: bytes.NewReader(content),
	}}}

	assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrDigestMismatch)
}

func TestVerifyRejectsExtraAndMissingEntries(t *testing.T) {
	t.Parallel()

	one := []byte("one")
	two := []byte("two")

	t.Run("extra entry", func(t *testing.T) {
		t.Parallel()

		publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("one", one)})
		source := &testSource{entries: []testSourceEntry{
			testEntryFor("one", one),
			testEntryFor("two", two),
		}}
		assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrExtraEntry)
	})

	t.Run("missing entry", func(t *testing.T) {
		t.Parallel()

		publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{
			manifestEntryFor("one", one),
			manifestEntryFor("two", two),
		})
		source := &testSource{entries: []testSourceEntry{testEntryFor("one", one)}}
		assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrMissingEntry)
	})
}

func TestVerifyEnforcesLimits(t *testing.T) {
	t.Parallel()

	five := []byte("12345")
	four := []byte("1234")

	t.Run("per-file limit", func(t *testing.T) {
		t.Parallel()

		publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{manifestEntryFor("file", five)})
		source := &testSource{entries: []testSourceEntry{testEntryFor("file", five)}}
		assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 4, MaxTotalBytes: 100}, ErrFileTooLarge)
	})

	t.Run("total limit", func(t *testing.T) {
		t.Parallel()

		publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{
			manifestEntryFor("a", four),
			manifestEntryFor("b", four),
		})
		source := &testSource{entries: []testSourceEntry{
			testEntryFor("a", four),
			testEntryFor("b", four),
		}}
		assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 4, MaxTotalBytes: 7}, ErrTotalTooLarge)
	})
}

func TestVerifyRejectsFileDirectoryConflictBeforeReading(t *testing.T) {
	t.Parallel()

	content := []byte("content")
	publicKey, manifest, signature := testSignedManifest(t, []ManifestEntry{
		manifestEntryFor("a", content),
		manifestEntryFor("a/b", content),
	})
	source := &testSource{}

	assertRejectedBeforeInstall(t, publicKey, manifest, signature, source, Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024}, ErrPathConflict)
	if source.nextCalls != 0 {
		t.Fatalf("source Next calls = %d, want 0 for conflicting manifest", source.nextCalls)
	}
}

func assertRejectedBeforeInstall(
	t *testing.T,
	publicKey ed25519.PublicKey,
	manifest, signature []byte,
	source EntrySource,
	limits Limits,
	wantErr error,
) {
	t.Helper()

	installs := 0
	err := VerifyAndInstall(
		context.Background(),
		publicKey,
		manifest,
		signature,
		source,
		limits,
		func(map[string][]byte) error {
			installs++
			return nil
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("VerifyAndInstall() error = %v, want %v", err, wantErr)
	}
	if installs != 0 {
		t.Fatalf("install callback count = %d, want 0", installs)
	}
}

func testSignedManifest(t *testing.T, entries []ManifestEntry) (ed25519.PublicKey, []byte, []byte) {
	t.Helper()

	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x5a}, ed25519.SeedSize))
	manifest, signature, err := SignManifest(privateKey, entries)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey.Public().(ed25519.PublicKey), manifest, signature
}

func manifestEntryFor(path string, data []byte) ManifestEntry {
	return ManifestEntry{Path: path, SHA256: sha256.Sum256(data)}
}

func testEntryFor(path string, data []byte) testSourceEntry {
	return testSourceEntry{path: path, digest: sha256.Sum256(data), content: bytes.NewReader(data)}
}
