package netsec

import (
	"bytes"
	"errors"
	"testing"
)

var (
	testKey  = bytes.Repeat([]byte{0x42}, 32)
	testSalt = bytes.Repeat([]byte{0x17}, SaltSize)
)

func newTestCipher(t *testing.T, chunkSize int, policy SequencePolicy) *Cipher {
	t.Helper()
	c, err := NewCipher(testKey, Config{
		ChunkSize: chunkSize,
		Salt:      testSalt,
		Policy:    policy,
	})
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestRoundTripPositional(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	plaintext := bytes.Repeat([]byte("0123456789abcdef"), 20)

	message, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(message.Chunks) != 5 {
		t.Fatalf("chunk count = %d, want 5", len(message.Chunks))
	}
	for i, chunk := range message.Chunks[:len(message.Chunks)-1] {
		if got, want := chunk.Sequence, uint64(i); got != want {
			t.Fatalf("chunk %d sequence = %d, want %d", i, got, want)
		}
		if chunk.Final {
			t.Fatalf("chunk %d unexpectedly final", i)
		}
	}
	if last := message.Chunks[len(message.Chunks)-1]; !last.Final || last.Sequence != FinalSequence {
		t.Fatalf("last chunk = %#v, want final marker", last)
	}

	got, err := c.Decrypt(message.Chunks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch")
	}

	encoded, err := message.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	parsed, err := ParseMessage(encoded)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	got, err = parsed.Decrypt(testKey)
	if err != nil {
		t.Fatalf("parsed Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("parsed plaintext mismatch")
	}
}

func TestPackageEncryptDecrypt(t *testing.T) {
	plaintext := bytes.Repeat([]byte("mail body with more than one chunk\n"), 8)
	encoded, err := Encrypt(testKey, plaintext, Config{ChunkSize: 64, Salt: testSalt})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := Decrypt(testKey, encoded)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: %q", got)
	}
}

func TestConsecutiveChunksWithoutFinal(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("first")),
		mustSeal(t, c, 1, false, []byte("second")),
	}
	got, err := c.Decrypt(chunks)
	if err != nil {
		t.Fatalf("Decrypt consecutive chunks: %v", err)
	}
	if string(got) != "firstsecond" {
		t.Fatalf("plaintext = %q", got)
	}
}

func TestSequenceJumpFails(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("first")),
		mustSeal(t, c, 2, false, []byte("third")),
		mustSeal(t, c, FinalSequence, true, []byte("end")),
	}
	_, err := c.Decrypt(chunks)
	if !errors.Is(err, ErrSequenceJump) {
		t.Fatalf("Decrypt error = %v, want ErrSequenceJump", err)
	}
}

func TestDuplicateFinalFails(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("first")),
		mustSeal(t, c, FinalSequence, true, []byte("end")),
		mustSeal(t, c, FinalSequence, true, []byte("again")),
	}
	_, err := c.Decrypt(chunks)
	if !errors.Is(err, ErrDuplicateFinal) {
		t.Fatalf("Decrypt error = %v, want ErrDuplicateFinal", err)
	}
}

func TestNonceReuseAllowsPlaintextSubstitution(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	first := mustSeal(t, c, 0, false, []byte("FIRST"))
	second := mustSeal(t, c, 0, false, []byte("SECOND"))
	final := mustSeal(t, c, FinalSequence, true, []byte("END"))
	if !bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("expected reused sequence numbers to produce the same nonce")
	}

	chunks := []Chunk{first, second, final}
	if err := SwapPayloads(chunks, 0, 1); err != nil {
		t.Fatalf("SwapPayloads: %v", err)
	}

	got, err := c.Decrypt(chunks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "SECONDFIRSTEND" {
		t.Fatalf("plaintext = %q, want later chunk to reveal the earlier plaintext", got)
	}
	if string(got[len("SECOND"):len("SECOND")+len("FIRST")]) != "FIRST" {
		t.Fatalf("second chunk did not decrypt to the first plaintext: %q", got)
	}
}

func TestSwapChunksWithReusedSequence(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("FIRST")),
		mustSeal(t, c, 0, false, []byte("SECOND")),
		mustSeal(t, c, FinalSequence, true, []byte("END")),
	}
	if err := SwapChunks(chunks, 0, 1); err != nil {
		t.Fatalf("SwapChunks: %v", err)
	}
	got, err := c.Decrypt(chunks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "SECONDFIRSTEND" {
		t.Fatalf("plaintext = %q", got)
	}
}

func TestReuseSequenceRevealsPreviousPlaintext(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("FIRST")),
		mustSeal(t, c, 0, false, []byte("SECOND")),
		mustSeal(t, c, FinalSequence, true, []byte("END")),
	}
	if err := ReuseSequence(chunks, 0, 1); err != nil {
		t.Fatalf("ReuseSequence: %v", err)
	}
	got, err := c.Decrypt(chunks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "FIRSTFIRSTEND" {
		t.Fatalf("plaintext = %q, want the later chunk to reveal the earlier plaintext", got)
	}
}

func TestStrictPolicyRejectsReuse(t *testing.T) {
	c := newTestCipher(t, 64, RequireSequential)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("FIRST")),
		mustSeal(t, c, 0, false, []byte("SECOND")),
		mustSeal(t, c, FinalSequence, true, []byte("END")),
	}
	_, err := c.Decrypt(chunks)
	if !errors.Is(err, ErrSequenceReuse) {
		t.Fatalf("Decrypt error = %v, want ErrSequenceReuse", err)
	}
}

func TestStrictConfigOption(t *testing.T) {
	c, err := NewCipher(testKey, Config{ChunkSize: 64, Salt: testSalt, Strict: true})
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if c.Policy != RequireSequential {
		t.Fatalf("Policy = %d, want RequireSequential", c.Policy)
	}
}

func TestStrictPolicyRequiresFinal(t *testing.T) {
	c := newTestCipher(t, 64, RequireSequential)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("first")),
		mustSeal(t, c, 1, false, []byte("second")),
	}
	_, err := c.Decrypt(chunks)
	if !errors.Is(err, ErrMissingFinal) {
		t.Fatalf("Decrypt error = %v, want ErrMissingFinal", err)
	}
}

func TestSwapSequenceNumbersBreaksAuthenticationOrder(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{
		mustSeal(t, c, 0, false, []byte("first")),
		mustSeal(t, c, 1, false, []byte("second")),
		mustSeal(t, c, FinalSequence, true, []byte("end")),
	}
	if err := SwapSequenceNumbers(chunks, 0, 1); err != nil {
		t.Fatalf("SwapSequenceNumbers: %v", err)
	}
	if chunks[0].Sequence != 1 || chunks[1].Sequence != 0 {
		t.Fatalf("sequences not swapped: %d, %d", chunks[0].Sequence, chunks[1].Sequence)
	}
	_, err := c.Decrypt(chunks)
	if !errors.Is(err, ErrSequenceOrder) && !errors.Is(err, ErrSequenceJump) {
		t.Fatalf("Decrypt error = %v, want an ordering error", err)
	}
}

func TestIndexedRoundTrip(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	message, err := c.Encrypt([]byte("indexed message body"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	encoded, err := message.MarshalIndexed()
	if err != nil {
		t.Fatalf("MarshalIndexed: %v", err)
	}
	parsed, err := ParseIndexed(encoded)
	if err != nil {
		t.Fatalf("ParseIndexed: %v", err)
	}
	got, err := parsed.Decrypt(testKey)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "indexed message body" {
		t.Fatalf("plaintext = %q", got)
	}
}

func TestIndexedReuseSurvivesSerialization(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks, err := c.EncryptWithSequences(
		[]uint64{0, 0, FinalSequence},
		[][]byte{[]byte("FIRST"), []byte("SECOND"), []byte("END")},
	)
	if err != nil {
		t.Fatalf("EncryptWithSequences: %v", err)
	}
	message := &Message{
		Version:   Version2,
		Cipher:    c.Cipher,
		Mode:      c.Mode,
		ChunkSize: c.ChunkSize,
		Salt:      c.Salt,
		Chunks:    chunks,
	}
	encoded, err := message.MarshalIndexed()
	if err != nil {
		t.Fatalf("MarshalIndexed: %v", err)
	}
	parsed, err := ParseIndexed(encoded)
	if err != nil {
		t.Fatalf("ParseIndexed: %v", err)
	}
	if parsed.Chunks[0].Sequence != 0 || parsed.Chunks[1].Sequence != 0 {
		t.Fatalf("duplicate sequences not preserved: %d, %d",
			parsed.Chunks[0].Sequence, parsed.Chunks[1].Sequence)
	}
	if err := SwapPayloads(parsed.Chunks, 0, 1); err != nil {
		t.Fatalf("SwapPayloads: %v", err)
	}
	got, err := parsed.Decrypt(testKey)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "SECONDFIRSTEND" {
		t.Fatalf("plaintext = %q", got)
	}
	if _, err := parsed.Decrypt(testKey, RequireSequential); !errors.Is(err, ErrSequenceReuse) {
		t.Fatalf("strict Decrypt error = %v, want ErrSequenceReuse", err)
	}
	if _, err := DecryptMessage(testKey, parsed, Config{Strict: true}); !errors.Is(err, ErrSequenceReuse) {
		t.Fatalf("strict DecryptMessage error = %v, want ErrSequenceReuse", err)
	}
}

func TestTamperedTagFails(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	chunks := []Chunk{mustSeal(t, c, FinalSequence, true, []byte("mail"))}
	chunks[0].Tag[0] ^= 0xff
	_, err := c.Decrypt(chunks)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Decrypt error = %v, want ErrAuthentication", err)
	}
}

func TestEmptyPlaintext(t *testing.T) {
	c := newTestCipher(t, 64, AllowSequenceReuse)
	message, err := c.Encrypt(nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(message.Chunks) != 1 || !message.Chunks[0].IsFinal() {
		t.Fatalf("empty message chunks = %#v", message.Chunks)
	}
	got, err := c.Decrypt(message.Chunks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("plaintext = %q", got)
	}
}

func TestPacketWrapRoundTrip(t *testing.T) {
	plaintext := bytes.Repeat([]byte("packet body "), 20)
	packet, err := EncryptPacket(testKey, plaintext, Config{ChunkSize: 64, Salt: testSalt})
	if err != nil {
		t.Fatalf("EncryptPacket: %v", err)
	}
	if packet[0] != 0xD2 {
		t.Fatalf("packet tag octet = %#x, want 0xD2", packet[0])
	}
	got, err := DecryptPacket(testKey, packet)
	if err != nil {
		t.Fatalf("DecryptPacket: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch")
	}
}

func mustSeal(t *testing.T, c *Cipher, sequence uint64, final bool, plaintext []byte) Chunk {
	t.Helper()
	chunk, err := c.SealChunk(sequence, final, plaintext)
	if err != nil {
		t.Fatalf("SealChunk(%d): %v", sequence, err)
	}
	return chunk
}
