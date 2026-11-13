package netsec_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	netsec "example.org/netsec/q0152"
)

const (
	testSession = "session-0152"
	testMessage = "message-0152"
)

type fixture struct {
	sealed    *netsec.SealedAttachment
	sender    *netsec.Device
	recipient *netsec.Device
	fileID    [netsec.FileIDSize]byte
	fileKey   [netsec.FileKeySize]byte
	metadata  []byte
}

func newDevice(t *testing.T, id string) *netsec.Device {
	t.Helper()
	device, err := netsec.GenerateDevice(id)
	if err != nil {
		t.Fatalf("GenerateDevice(%q): %v", id, err)
	}
	return device
}

func fixtureMetadata(name string) []byte {
	return []byte(`{"name":"` + name + `","type":"text/plain"}`)
}

func makeFixture(
	t *testing.T,
	name string,
	fileIDByte byte,
	fileKey [netsec.FileKeySize]byte,
	plaintext []byte,
	chunkSize uint64,
	sender *netsec.Device,
	recipients ...*netsec.Device,
) *fixture {
	t.Helper()
	var fileID [netsec.FileIDSize]byte
	fileID[0] = fileIDByte
	fileID[1] = 0xa5
	fileID[len(fileID)-1] = 0x5a
	metadata := fixtureMetadata(name)
	recipientDescriptors := make([]netsec.Recipient, len(recipients))
	for i, recipient := range recipients {
		recipientDescriptors[i] = recipient.Recipient()
	}
	sealed, err := netsec.SealAttachment(bytes.NewReader(plaintext), netsec.SealOptions{
		ChunkSize:      chunkSize,
		Metadata:       metadata,
		SessionID:      testSession,
		MessageID:      testMessage,
		SenderDeviceID: sender.DeviceID,
		SigningKey:     sender.SigningKey,
		Recipients:     recipientDescriptors,
		FileID:         &fileID,
		FileKey:        &fileKey,
	})
	if err != nil {
		t.Fatalf("SealAttachment(%q): %v", name, err)
	}
	return &fixture{
		sealed:    sealed,
		sender:    sender,
		recipient: recipients[0],
		fileID:    fileID,
		fileKey:   fileKey,
		metadata:  metadata,
	}
}

func openOptions(recipient, sender *netsec.Device) netsec.OpenOptions {
	return netsec.OpenOptions{
		DeviceID:             recipient.DeviceID,
		EncryptionPrivateKey: recipient.EncryptionKey,
		TrustedSenders: map[string]ed25519.PublicKey{
			sender.DeviceID: sender.SigningPublicKey(),
		},
		ExpectedSessionID: testSession,
		ExpectedMessageID: testMessage,
	}
}

func fixtureKey() [netsec.FileKeySize]byte {
	var key [netsec.FileKeySize]byte
	for i := range key {
		key[i] = byte(0x31 + i)
	}
	return key
}

func cloneChunks(chunks []netsec.Chunk) []netsec.Chunk {
	clone := make([]netsec.Chunk, len(chunks))
	for i, chunk := range chunks {
		clone[i] = netsec.Chunk{
			Index:      chunk.Index,
			Ciphertext: append([]byte(nil), chunk.Ciphertext...),
		}
	}
	return clone
}

func TestNonceDerivationMatchesSpecification(t *testing.T) {
	var fileKey [netsec.FileKeySize]byte
	var fileID [netsec.FileIDSize]byte
	for i := range fileKey {
		fileKey[i] = byte(i + 1)
	}
	for i := range fileID {
		fileID[i] = byte(i + 11)
	}
	const index = uint64(7)

	mac := hmac.New(sha256.New, fileKey[:])
	_, _ = mac.Write([]byte("nonce"))
	_, _ = mac.Write(fileID[:])
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], index)
	_, _ = mac.Write(encoded[:])
	sum := mac.Sum(nil)
	var want [netsec.ChunkNonceSize]byte
	copy(want[:], sum[:netsec.ChunkNonceSize])

	if got := netsec.DeriveChunkNonce(fileKey, fileID, index); got != want {
		t.Fatalf("DeriveChunkNonce mismatch: got %x want %x", got, want)
	}
}

func TestMerkleProofs(t *testing.T) {
	var leaves [][32]byte
	for total := 0; total <= 17; total++ {
		root := netsec.ComputeMerkleRoot(leaves)
		for index := uint64(0); index < uint64(total); index++ {
			proof, err := netsec.MerkleProof(leaves, index)
			if err != nil {
				t.Fatalf("MerkleProof leaves=%d index=%d: %v", total, index, err)
			}
			if !netsec.VerifyMerkleProof(leaves[index], index, uint64(total), proof, root) {
				t.Fatalf("proof failed leaves=%d index=%d", total, index)
			}
			if len(proof) > 0 {
				proof[0].Hash[0] ^= 1
				if netsec.VerifyMerkleProof(leaves[index], index, uint64(total), proof, root) {
					t.Fatalf("tampered proof accepted leaves=%d index=%d", total, index)
				}
			}
		}
		leaf := sha256.Sum256([]byte{byte(total)})
		leaves = append(leaves, leaf)
	}
}

func TestRoundTripAndLastChunkBoundaries(t *testing.T) {
	const chunkSize = uint64(8)
	sender := newDevice(t, "sender-boundary")
	recipient := newDevice(t, "recipient-boundary")
	key := fixtureKey()

	cases := []struct {
		name       string
		length     int
		wantChunks uint64
	}{
		{name: "empty", length: 0, wantChunks: 0},
		{name: "one-byte", length: 1, wantChunks: 1},
		{name: "below-boundary", length: 7, wantChunks: 1},
		{name: "exact-boundary", length: 8, wantChunks: 1},
		{name: "one-past-boundary", length: 9, wantChunks: 2},
		{name: "two-exact-boundaries", length: 16, wantChunks: 2},
		{name: "two-boundaries-plus-one", length: 17, wantChunks: 3},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plaintext := bytes.Repeat([]byte{byte(0x60 + i)}, tc.length)
			f := makeFixture(t, tc.name, byte(i+1), key, plaintext, chunkSize, sender, recipient)
			if f.sealed.Manifest.TotalChunks != tc.wantChunks {
				t.Fatalf("TotalChunks=%d want %d", f.sealed.Manifest.TotalChunks, tc.wantChunks)
			}
			if got := len(f.sealed.Chunks); uint64(got) != tc.wantChunks {
				t.Fatalf("chunk count=%d want %d", got, tc.wantChunks)
			}
			for i, chunk := range f.sealed.Chunks {
				if chunk.Index != uint64(i) {
					t.Fatalf("chunk index=%d want %d", chunk.Index, i)
				}
			}
			if tc.wantChunks > 0 {
				last := f.sealed.Chunks[len(f.sealed.Chunks)-1]
				wantLast := tc.length - int(tc.wantChunks-1)*int(chunkSize)
				if got := len(last.Ciphertext) - netsec.GCMTagSize; got != wantLast {
					t.Fatalf("last chunk plaintext length=%d want %d", got, wantLast)
				}
			}

			opts := openOptions(recipient, sender)
			reader, err := netsec.OpenReader(f.sealed.Manifest, opts)
			if err != nil {
				t.Fatalf("OpenReader: %v", err)
			}
			for i := len(f.sealed.Chunks) - 1; i >= 0; i-- {
				chunk := f.sealed.Chunks[i]
				plainChunk, err := reader.ReadChunk(chunk.Index, chunk.Ciphertext)
				if err != nil {
					t.Fatalf("ReadChunk(%d): %v", chunk.Index, err)
				}
				wantLength := int(chunkSize)
				if uint64(i) == tc.wantChunks-1 {
					wantLength = tc.length - int(tc.wantChunks-1)*int(chunkSize)
				}
				if len(plainChunk) != wantLength {
					t.Fatalf("chunk %d plaintext length=%d want %d", i, len(plainChunk), wantLength)
				}
			}
			if err := reader.Finish(); err != nil {
				t.Fatalf("Finish: %v", err)
			}

			outOfOrder := make([]netsec.Chunk, 0, len(f.sealed.Chunks))
			for i := len(f.sealed.Chunks) - 1; i >= 0; i-- {
				outOfOrder = append(outOfOrder, f.sealed.Chunks[i])
			}
			got, err := netsec.OpenAttachment(f.sealed.Manifest, outOfOrder, opts)
			if err != nil {
				t.Fatalf("OpenAttachment out of order: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("round-trip plaintext mismatch")
			}
		})
	}
}

func TestCrossFileChunkRejectedAfterManifestResign(t *testing.T) {
	const chunkSize = uint64(8)
	sender := newDevice(t, "sender-cross-file")
	recipient := newDevice(t, "recipient-cross-file")
	key := fixtureKey()
	plaintextA := []byte("AAAAAAAAAAAAAAAAAAAAAAAA")
	plaintextB := []byte("BBBBBBBBBBBBBBBBBBBBBBBB")
	fixtureA := makeFixture(t, "file-a", 1, key, plaintextA, chunkSize, sender, recipient)
	fixtureB := makeFixture(t, "file-b", 2, key, plaintextB, chunkSize, sender, recipient)

	if fixtureA.fileID == fixtureB.fileID {
		t.Fatal("fixture file IDs must differ")
	}
	if fixtureA.fileKey != fixtureB.fileKey {
		t.Fatal("fixture file keys must be identical")
	}
	if netsec.DeriveChunkNonce(fixtureA.fileKey, fixtureA.fileID, 1) ==
		netsec.DeriveChunkNonce(fixtureB.fileKey, fixtureB.fileID, 1) {
		t.Fatal("chunk nonces must bind file ID")
	}

	const index = 1
	chunks := cloneChunks(fixtureA.sealed.Chunks)
	chunks[index].Ciphertext = append([]byte(nil), fixtureB.sealed.Chunks[index].Ciphertext...)
	manifest := fixtureA.sealed.Manifest.Clone()
	manifest.ChunkSHA256[index] = netsec.HashCiphertext(chunks[index].Ciphertext)
	manifest.RecomputeIntegrityFields()
	if err := manifest.Sign(sender.SigningKey); err != nil {
		t.Fatalf("re-sign cross-file manifest: %v", err)
	}

	_, err := netsec.OpenAttachment(manifest, chunks, openOptions(recipient, sender))
	if !errors.Is(err, netsec.ErrChunkAuthentication) {
		t.Fatalf("cross-file chunk error=%v, want ErrChunkAuthentication", err)
	}
}

func TestSwappedChunksRejectedAfterManifestResign(t *testing.T) {
	const chunkSize = uint64(8)
	sender := newDevice(t, "sender-swap")
	recipient := newDevice(t, "recipient-swap")
	f := makeFixture(t, "swap", 3, fixtureKey(), bytes.Repeat([]byte("S"), 27), chunkSize, sender, recipient)

	chunks := cloneChunks(f.sealed.Chunks)
	chunks[0].Ciphertext, chunks[1].Ciphertext = chunks[1].Ciphertext, chunks[0].Ciphertext
	manifest := f.sealed.Manifest.Clone()
	manifest.ChunkSHA256[0] = netsec.HashCiphertext(chunks[0].Ciphertext)
	manifest.ChunkSHA256[1] = netsec.HashCiphertext(chunks[1].Ciphertext)
	manifest.RecomputeIntegrityFields()
	if err := manifest.Sign(sender.SigningKey); err != nil {
		t.Fatalf("re-sign swapped manifest: %v", err)
	}

	_, err := netsec.OpenAttachment(manifest, chunks, openOptions(recipient, sender))
	if !errors.Is(err, netsec.ErrChunkAuthentication) {
		t.Fatalf("swapped chunk error=%v, want ErrChunkAuthentication", err)
	}
}

func TestTruncationRejected(t *testing.T) {
	const chunkSize = uint64(8)
	sender := newDevice(t, "sender-truncate")
	recipient := newDevice(t, "recipient-truncate")
	f := makeFixture(t, "truncate", 4, fixtureKey(), bytes.Repeat([]byte("T"), 25), chunkSize, sender, recipient)
	opts := openOptions(recipient, sender)

	t.Run("missing-chunk", func(t *testing.T) {
		_, err := netsec.OpenAttachment(f.sealed.Manifest, f.sealed.Chunks[:len(f.sealed.Chunks)-1], opts)
		if !errors.Is(err, netsec.ErrMissingChunks) {
			t.Fatalf("missing chunk error=%v, want ErrMissingChunks", err)
		}
	})
	t.Run("short-final-ciphertext", func(t *testing.T) {
		chunks := cloneChunks(f.sealed.Chunks)
		last := &chunks[len(chunks)-1]
		last.Ciphertext = last.Ciphertext[:len(last.Ciphertext)-1]
		_, err := netsec.OpenAttachment(f.sealed.Manifest, chunks, opts)
		if err == nil {
			t.Fatal("short final ciphertext accepted")
		}
	})
	t.Run("non-canonical-total", func(t *testing.T) {
		manifest := f.sealed.Manifest.Clone()
		manifest.TotalLength += chunkSize
		manifest.Signature = ed25519.Sign(sender.SigningKey, manifest.SigningPayload())
		_, err := netsec.OpenAttachment(manifest, f.sealed.Chunks, opts)
		if !errors.Is(err, netsec.ErrInvalidManifest) {
			t.Fatalf("non-canonical total error=%v, want ErrInvalidManifest", err)
		}
	})
}

func TestRecipientIdentityUniqueness(t *testing.T) {
	sender := newDevice(t, "sender-recipients")
	recipientA := newDevice(t, "device-a")
	recipientB := newDevice(t, "device-b")
	key := fixtureKey()

	t.Run("unique-devices-open-independently", func(t *testing.T) {
		f := makeFixture(t, "unique", 5, key, []byte("recipient uniqueness"), 8, sender, recipientA, recipientB)
		for _, recipient := range []*netsec.Device{recipientA, recipientB} {
			got, err := f.sealed.Open(openOptions(recipient, sender))
			if err != nil {
				t.Fatalf("open as %s: %v", recipient.DeviceID, err)
			}
			if string(got) != "recipient uniqueness" {
				t.Fatalf("open as %s: wrong plaintext", recipient.DeviceID)
			}
		}
	})

	t.Run("duplicate-device-rejected-by-seal", func(t *testing.T) {
		_, err := netsec.SealAttachment(bytes.NewReader([]byte("duplicate")), netsec.SealOptions{
			ChunkSize:      8,
			SessionID:      testSession,
			MessageID:      testMessage,
			SenderDeviceID: sender.DeviceID,
			SigningKey:     sender.SigningKey,
			Recipients: []netsec.Recipient{
				recipientA.Recipient(),
				recipientA.Recipient(),
			},
		})
		if !errors.Is(err, netsec.ErrDuplicateRecipient) {
			t.Fatalf("duplicate recipient seal error=%v", err)
		}
	})

	t.Run("duplicate-envelope-rejected-by-open", func(t *testing.T) {
		f := makeFixture(t, "duplicate", 6, key, []byte("duplicate envelope"), 8, sender, recipientA, recipientB)
		manifest := f.sealed.Manifest.Clone()
		manifest.Envelopes = append(manifest.Envelopes, manifest.Envelopes[0])
		manifest.Signature = ed25519.Sign(sender.SigningKey, manifest.SigningPayload())
		_, err := netsec.OpenAttachment(manifest, f.sealed.Chunks, openOptions(recipientA, sender))
		if !errors.Is(err, netsec.ErrDuplicateRecipient) {
			t.Fatalf("duplicate envelope open error=%v", err)
		}
	})
}

func TestRecipientEnvelopeCannotBeReassigned(t *testing.T) {
	sender := newDevice(t, "sender-envelope")
	alice := newDevice(t, "alice-device")
	bob := newDevice(t, "bob-device")
	key := fixtureKey()
	aliceFixture := makeFixture(t, "alice-file", 20, key, []byte("envelope identity"), 8, sender, alice)
	bobFixture := makeFixture(t, "bob-file", 20, key, []byte("envelope identity"), 8, sender, bob)

	manifest := aliceFixture.sealed.Manifest.Clone()
	manifest.Envelopes[0] = bobFixture.sealed.Manifest.Envelopes[0]
	manifest.Envelopes[0].RecipientDeviceID = alice.DeviceID
	if err := manifest.Sign(sender.SigningKey); err != nil {
		t.Fatalf("re-sign reassigned envelope: %v", err)
	}
	_, err := netsec.OpenAttachment(manifest, aliceFixture.sealed.Chunks, openOptions(alice, sender))
	if !errors.Is(err, netsec.ErrChunkAuthentication) {
		t.Fatalf("reassigned envelope error=%v, want ErrChunkAuthentication", err)
	}
}

func TestMetadataRewriteRejected(t *testing.T) {
	sender := newDevice(t, "sender-metadata")
	recipient := newDevice(t, "recipient-metadata")
	f := makeFixture(t, "metadata", 7, fixtureKey(), bytes.Repeat([]byte("M"), 21), 8, sender, recipient)
	opts := openOptions(recipient, sender)

	t.Run("unsigned-metadata-change", func(t *testing.T) {
		manifest := f.sealed.Manifest.Clone()
		manifest.Metadata = append(manifest.Metadata, '!')
		_, err := netsec.OpenAttachment(manifest, f.sealed.Chunks, opts)
		if !errors.Is(err, netsec.ErrInvalidSignature) {
			t.Fatalf("metadata rewrite error=%v, want ErrInvalidSignature", err)
		}
	})

	t.Run("rebound-digest-still-fails-aead", func(t *testing.T) {
		manifest := f.sealed.Manifest.Clone()
		manifest.Metadata = append(manifest.Metadata, '!')
		manifest.MetadataDigest = netsec.HashMetadata(manifest.Metadata)
		if err := manifest.Sign(sender.SigningKey); err != nil {
			t.Fatalf("re-sign metadata manifest: %v", err)
		}
		_, err := netsec.OpenAttachment(manifest, f.sealed.Chunks, opts)
		if !errors.Is(err, netsec.ErrChunkAuthentication) {
			t.Fatalf("rebound metadata error=%v, want ErrChunkAuthentication", err)
		}
	})

	t.Run("expected-metadata-mismatch", func(t *testing.T) {
		badOpts := opts
		badOpts.ExpectedMetadata = []byte("different")
		_, err := netsec.OpenAttachment(f.sealed.Manifest, f.sealed.Chunks, badOpts)
		if !errors.Is(err, netsec.ErrMetadataMismatch) {
			t.Fatalf("expected metadata error=%v", err)
		}
	})
}

func TestSignatureContextAndTrust(t *testing.T) {
	sender := newDevice(t, "sender-context")
	otherSender := newDevice(t, "other-sender")
	recipient := newDevice(t, "recipient-context")
	f := makeFixture(t, "context", 8, fixtureKey(), []byte("context binding"), 8, sender, recipient)

	t.Run("wrong-sender-key", func(t *testing.T) {
		opts := openOptions(recipient, sender)
		opts.SenderPublicKey = otherSender.SigningPublicKey()
		_, err := f.sealed.Open(opts)
		if !errors.Is(err, netsec.ErrInvalidSignature) {
			t.Fatalf("wrong sender error=%v", err)
		}
	})
	t.Run("unknown-sender", func(t *testing.T) {
		opts := openOptions(recipient, sender)
		opts.TrustedSenders = nil
		_, err := f.sealed.Open(opts)
		if !errors.Is(err, netsec.ErrUntrustedSender) {
			t.Fatalf("unknown sender error=%v", err)
		}
	})
	t.Run("wrong-session", func(t *testing.T) {
		opts := openOptions(recipient, sender)
		opts.ExpectedSessionID = "other-session"
		_, err := f.sealed.Open(opts)
		if !errors.Is(err, netsec.ErrInvalidManifest) {
			t.Fatalf("wrong session error=%v", err)
		}
	})
	t.Run("wrong-message", func(t *testing.T) {
		opts := openOptions(recipient, sender)
		opts.ExpectedMessageID = "other-message"
		_, err := f.sealed.Open(opts)
		if !errors.Is(err, netsec.ErrInvalidManifest) {
			t.Fatalf("wrong message error=%v", err)
		}
	})
	t.Run("tampered-signature", func(t *testing.T) {
		manifest := f.sealed.Manifest.Clone()
		manifest.Signature[0] ^= 1
		_, err := netsec.OpenAttachment(manifest, f.sealed.Chunks, openOptions(recipient, sender))
		if !errors.Is(err, netsec.ErrInvalidSignature) {
			t.Fatalf("tampered signature error=%v", err)
		}
	})
}

func TestRepeatedChunkIndexRejected(t *testing.T) {
	sender := newDevice(t, "sender-repeat")
	recipient := newDevice(t, "recipient-repeat")
	f := makeFixture(t, "repeat", 9, fixtureKey(), []byte("repeat index"), 8, sender, recipient)
	reader, err := netsec.OpenReader(f.sealed.Manifest, openOptions(recipient, sender))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	chunk := f.sealed.Chunks[0]
	if _, err := reader.ReadChunk(chunk.Index, chunk.Ciphertext); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if _, err := reader.ReadChunk(chunk.Index, chunk.Ciphertext); !errors.Is(err, netsec.ErrDuplicateChunk) {
		t.Fatalf("duplicate error=%v, want ErrDuplicateChunk", err)
	}
}

func TestReaderFreezesVerifiedManifest(t *testing.T) {
	sender := newDevice(t, "sender-freeze")
	recipient := newDevice(t, "recipient-freeze")
	plaintext := bytes.Repeat([]byte("Z"), 27)
	f := makeFixture(t, "freeze", 19, fixtureKey(), plaintext, 8, sender, recipient)

	reader, err := netsec.OpenReader(f.sealed.Manifest, openOptions(recipient, sender))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	// Mutating the caller's object after verification must not change the
	// authenticated state used by the reader.
	f.sealed.Manifest.Metadata[0] ^= 1
	f.sealed.Manifest.TotalLength++
	f.sealed.Manifest.ChunkSHA256[0][0] ^= 1

	var output bytes.Buffer
	for _, chunk := range f.sealed.Chunks {
		if err := reader.ReadChunkTo(chunk.Index, chunk.Ciphertext, &output); err != nil {
			t.Fatalf("ReadChunkTo(%d): %v", chunk.Index, err)
		}
	}
	if err := reader.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !bytes.Equal(output.Bytes(), plaintext) {
		t.Fatal("frozen reader plaintext mismatch")
	}
}

func TestManifestBinaryRoundTrip(t *testing.T) {
	sender := newDevice(t, "sender-binary")
	recipient := newDevice(t, "recipient-binary")
	f := makeFixture(t, "binary", 10, fixtureKey(), bytes.Repeat([]byte("B"), 19), 8, sender, recipient)

	encoded, err := f.sealed.Manifest.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	var decoded netsec.Manifest
	if err := decoded.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	got, err := netsec.OpenAttachment(&decoded, f.sealed.Chunks, openOptions(recipient, sender))
	if err != nil {
		t.Fatalf("open decoded manifest: %v", err)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte("B"), 19)) {
		t.Fatal("decoded manifest plaintext mismatch")
	}

	encoded[len(encoded)-1] ^= 1
	var tampered netsec.Manifest
	if err := tampered.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("UnmarshalBinary tampered signature: %v", err)
	}
	if _, err := netsec.OpenAttachment(&tampered, f.sealed.Chunks, openOptions(recipient, sender)); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("tampered encoded manifest error=%v", err)
	}
}

func TestFilePublicationIsAtomicAndCallbackGated(t *testing.T) {
	const chunkSize = uint64(8)
	sender := newDevice(t, "sender-file")
	recipient := newDevice(t, "recipient-file")
	key := fixtureKey()
	plaintext := bytes.Repeat([]byte("F"), 29)

	t.Run("success", func(t *testing.T) {
		f := makeFixture(t, "file-success", 11, key, plaintext, chunkSize, sender, recipient)
		dir := t.TempDir()
		dest := filepath.Join(dir, "attachment.bin")
		calls := 0
		opts := openOptions(recipient, sender)
		opts.OnFile = func(path string) {
			calls++
			if path != dest {
				t.Errorf("OnFile path=%q want %q", path, dest)
			}
		}
		if err := f.sealed.OpenToFile(dest, opts); err != nil {
			t.Fatalf("OpenToFile: %v", err)
		}
		if calls != 1 {
			t.Fatalf("OnFile calls=%d want 1", calls)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatal("published plaintext mismatch")
		}
		assertDirectoryHasOnly(t, dir, "attachment.bin")
	})

	t.Run("failures-never-call-file-callback", func(t *testing.T) {
		key := fixtureKey()
		cases := []struct {
			name  string
			build func(t *testing.T) (*netsec.Manifest, []netsec.Chunk)
		}{
			{
				name: "missing-final-chunk",
				build: func(t *testing.T) (*netsec.Manifest, []netsec.Chunk) {
					f := makeFixture(t, "fail-missing", 12, key, plaintext, chunkSize, sender, recipient)
					return f.sealed.Manifest, f.sealed.Chunks[:len(f.sealed.Chunks)-1]
				},
			},
			{
				name: "cross-file-chunk",
				build: func(t *testing.T) (*netsec.Manifest, []netsec.Chunk) {
					f := makeFixture(t, "fail-cross-a", 13, key, bytes.Repeat([]byte("A"), 29), chunkSize, sender, recipient)
					other := makeFixture(t, "fail-cross-b", 14, key, bytes.Repeat([]byte("B"), 29), chunkSize, sender, recipient)
					chunks := cloneChunks(f.sealed.Chunks)
					chunks[1].Ciphertext = append([]byte(nil), other.sealed.Chunks[1].Ciphertext...)
					manifest := f.sealed.Manifest.Clone()
					manifest.ChunkSHA256[1] = netsec.HashCiphertext(chunks[1].Ciphertext)
					manifest.RecomputeIntegrityFields()
					if err := manifest.Sign(sender.SigningKey); err != nil {
						t.Fatalf("sign cross-file failure fixture: %v", err)
					}
					return manifest, chunks
				},
			},
			{
				name: "swapped-chunks",
				build: func(t *testing.T) (*netsec.Manifest, []netsec.Chunk) {
					f := makeFixture(t, "fail-swap", 15, key, plaintext, chunkSize, sender, recipient)
					chunks := cloneChunks(f.sealed.Chunks)
					chunks[0].Ciphertext, chunks[1].Ciphertext = chunks[1].Ciphertext, chunks[0].Ciphertext
					manifest := f.sealed.Manifest.Clone()
					manifest.ChunkSHA256[0] = netsec.HashCiphertext(chunks[0].Ciphertext)
					manifest.ChunkSHA256[1] = netsec.HashCiphertext(chunks[1].Ciphertext)
					manifest.RecomputeIntegrityFields()
					if err := manifest.Sign(sender.SigningKey); err != nil {
						t.Fatalf("sign swap failure fixture: %v", err)
					}
					return manifest, chunks
				},
			},
			{
				name: "truncated-final-ciphertext",
				build: func(t *testing.T) (*netsec.Manifest, []netsec.Chunk) {
					f := makeFixture(t, "fail-truncate", 16, key, plaintext, chunkSize, sender, recipient)
					chunks := cloneChunks(f.sealed.Chunks)
					last := &chunks[len(chunks)-1]
					last.Ciphertext = last.Ciphertext[:len(last.Ciphertext)-2]
					return f.sealed.Manifest, chunks
				},
			},
			{
				name: "metadata-rewrite",
				build: func(t *testing.T) (*netsec.Manifest, []netsec.Chunk) {
					f := makeFixture(t, "fail-metadata", 17, key, plaintext, chunkSize, sender, recipient)
					manifest := f.sealed.Manifest.Clone()
					manifest.Metadata = append(manifest.Metadata, '!')
					return manifest, f.sealed.Chunks
				},
			},
			{
				name: "duplicate-recipient-envelope",
				build: func(t *testing.T) (*netsec.Manifest, []netsec.Chunk) {
					f := makeFixture(t, "fail-duplicate", 18, key, plaintext, chunkSize, sender, recipient)
					manifest := f.sealed.Manifest.Clone()
					manifest.Envelopes = append(manifest.Envelopes, manifest.Envelopes[0])
					manifest.Signature = ed25519.Sign(sender.SigningKey, manifest.SigningPayload())
					return manifest, f.sealed.Chunks
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				manifest, chunks := tc.build(t)
				dir := t.TempDir()
				dest := filepath.Join(dir, "attachment.bin")
				calls := 0
				opts := openOptions(recipient, sender)
				opts.OnFile = func(string) {
					calls++
				}
				if err := netsec.OpenAttachmentToFile(manifest, chunks, dest, opts); err == nil {
					t.Fatal("invalid attachment was published")
				}
				if calls != 0 {
					t.Fatalf("OnFile calls=%d on failure, want 0", calls)
				}
				if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("destination exists after failure: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("ReadDir: %v", err)
				}
				if len(entries) != 0 {
					t.Fatalf("temporary or partial objects left behind: %v", entries)
				}
			})
		}
	})
}

func assertDirectoryHasOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(names) {
		t.Fatalf("directory entries=%d want %d", len(entries), len(names))
	}
	for _, entry := range entries {
		found := false
		for _, name := range names {
			if entry.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unexpected file %q", entry.Name())
		}
	}
}
