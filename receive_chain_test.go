package netsec_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"testing"

	netsec "example.org/netsec/q0031"
)

type testFixture struct {
	receiver *netsec.ReceiveChain
	sender   *netsec.SenderChain
	chainKey []byte
}

func newFixture(t *testing.T, name string) testFixture {
	t.Helper()

	chainKey := sha256.Sum256([]byte("chain-key:" + name))
	ratchetPublicKey := sha256.Sum256([]byte("ratchet-key:" + name))

	receiver, err := netsec.NewReceiveChain(ratchetPublicKey[:], chainKey[:])
	if err != nil {
		t.Fatalf("NewReceiveChain: %v", err)
	}
	sender, err := netsec.NewSenderChain(chainKey[:])
	if err != nil {
		t.Fatalf("NewSenderChain: %v", err)
	}
	return testFixture{
		receiver: receiver,
		sender:   sender,
		chainKey: chainKey[:],
	}
}

func nonceFor(sequence uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[:8], sequence)
	nonce[8] = 1
	return nonce
}

func (f testFixture) sealAt(t *testing.T, sequence uint64, plaintext string) ([]byte, []byte) {
	t.Helper()
	nonce := nonceFor(sequence)
	ciphertext, err := f.sender.SealAt(sequence, nonce, []byte(plaintext), []byte("aad"))
	if err != nil {
		t.Fatalf("SealAt(%d): %v", sequence, err)
	}
	return nonce, ciphertext
}

func (f testFixture) decrypt(t *testing.T, sequence uint64, nonce, ciphertext []byte) []byte {
	t.Helper()
	plaintext, err := f.receiver.Decrypt(sequence, nonce, ciphertext, []byte("aad"))
	if err != nil {
		t.Fatalf("Decrypt(%d): %v", sequence, err)
	}
	return plaintext
}

func flipTag(ciphertext []byte) []byte {
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 0x01
	return tampered
}

func TestHugeSequenceRejectedBeforeDerivation(t *testing.T) {
	fixture := newFixture(t, "huge")
	nonce, ciphertext := fixture.sealAt(t, 1001, "never reached")

	for _, sequence := range []uint64{1001, math.MaxUint32, math.MaxUint64} {
		_, err := fixture.receiver.Decrypt(sequence, nonce, ciphertext, []byte("aad"))
		if !errors.Is(err, netsec.ErrSkipLimitExceeded) {
			t.Fatalf("sequence %d: got %v, want ErrSkipLimitExceeded", sequence, err)
		}
		if next := fixture.receiver.NextSequence(); next != 0 {
			t.Fatalf("sequence %d advanced head to %d", sequence, next)
		}
		if count := fixture.receiver.SkippedKeyCount(); count != 0 {
			t.Fatalf("sequence %d retained %d skipped keys", sequence, count)
		}
	}

	// The rejected messages must leave the chain usable from its original head.
	nonce, ciphertext = fixture.sealAt(t, 0, "still valid")
	if got := string(fixture.decrypt(t, 0, nonce, ciphertext)); got != "still valid" {
		t.Fatalf("decrypt after rejection = %q", got)
	}
	if next := fixture.receiver.NextSequence(); next != 1 {
		t.Fatalf("head after valid message = %d, want 1", next)
	}
}

func TestSkipLimitBoundaryAndCacheConsumption(t *testing.T) {
	fixture := newFixture(t, "boundary")

	nonce1000, ciphertext1000 := fixture.sealAt(t, 1000, "at limit")
	if got := string(fixture.decrypt(t, 1000, nonce1000, ciphertext1000)); got != "at limit" {
		t.Fatalf("seq 1000 plaintext = %q", got)
	}
	if next := fixture.receiver.NextSequence(); next != 1001 {
		t.Fatalf("head = %d, want 1001", next)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 1000 {
		t.Fatalf("skipped count = %d, want 1000", count)
	}
	if !fixture.receiver.HasSkippedKey(0) || !fixture.receiver.HasSkippedKey(999) {
		t.Fatal("expected sequence 0 and 999 to be retained")
	}
	if fixture.receiver.HasSkippedKey(1000) {
		t.Fatal("consumed sequence 1000 must not be retained")
	}

	nonce0, ciphertext0 := fixture.sealAt(t, 0, "late first")
	if got := string(fixture.decrypt(t, 0, nonce0, ciphertext0)); got != "late first" {
		t.Fatalf("seq 0 plaintext = %q", got)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 999 {
		t.Fatalf("skipped count after consuming seq 0 = %d, want 999", count)
	}

	nonce1001, ciphertext1001 := fixture.sealAt(t, 1001, "next")
	if got := string(fixture.decrypt(t, 1001, nonce1001, ciphertext1001)); got != "next" {
		t.Fatalf("seq 1001 plaintext = %q", got)
	}
	if next := fixture.receiver.NextSequence(); next != 1002 {
		t.Fatalf("head after seq 1001 = %d, want 1002", next)
	}
}

func TestFailureThenNormalMessage(t *testing.T) {
	fixture := newFixture(t, "failure-normal")

	nonce0, ciphertext0 := fixture.sealAt(t, 0, "zero")
	if _, err := fixture.receiver.Decrypt(0, nonce0, flipTag(ciphertext0), []byte("aad")); err == nil {
		t.Fatal("tampered head message authenticated")
	}
	if next := fixture.receiver.NextSequence(); next != 0 {
		t.Fatalf("failed head authentication advanced head to %d", next)
	}

	nonce5, ciphertext5 := fixture.sealAt(t, 5, "authentic later")
	if _, err := fixture.receiver.Decrypt(5, nonce5, flipTag(ciphertext5), []byte("aad")); err == nil {
		t.Fatal("tampered message authenticated")
	}

	if next := fixture.receiver.NextSequence(); next != 0 {
		t.Fatalf("failed authentication advanced head to %d", next)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 0 {
		t.Fatalf("failed authentication retained %d skipped keys", count)
	}
	if fixture.receiver.HasSkippedKey(0) {
		t.Fatal("failed authentication cached a skipped key")
	}

	for sequence, plaintext := range []string{"zero", "one", "two"} {
		nonce, ciphertext := nonce0, ciphertext0
		if sequence != 0 {
			nonce, ciphertext = fixture.sealAt(t, uint64(sequence), plaintext)
		}
		if got := string(fixture.decrypt(t, uint64(sequence), nonce, ciphertext)); got != plaintext {
			t.Fatalf("seq %d plaintext = %q, want %q", sequence, got, plaintext)
		}
	}

	// The earlier failed sequence must still authenticate with the genuine
	// ciphertext and the chain key that was never advanced.
	if got := string(fixture.decrypt(t, 5, nonce5, ciphertext5)); got != "authentic later" {
		t.Fatalf("seq 5 plaintext after recovery = %q", got)
	}
}

func TestOutOfOrderAndDuplicateMessages(t *testing.T) {
	fixture := newFixture(t, "ordering")

	nonce2, ciphertext2 := fixture.sealAt(t, 2, "two")
	if got := string(fixture.decrypt(t, 2, nonce2, ciphertext2)); got != "two" {
		t.Fatalf("seq 2 plaintext = %q", got)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 2 {
		t.Fatalf("skipped count = %d, want 2", count)
	}

	ids := fixture.receiver.SkippedKeys().IDs()
	if len(ids) != 2 {
		t.Fatalf("skipped IDs = %d, want 2", len(ids))
	}
	for i, sequence := range []uint64{0, 1} {
		if ids[i].Sequence != sequence {
			t.Fatalf("skipped ID %d has sequence %d, want %d", i, ids[i].Sequence, sequence)
		}
		if !bytes.Equal(ids[i].RatchetPublicKey, fixture.receiver.RatchetPublicKey()) {
			t.Fatal("skipped key is not namespaced by the ratchet public key")
		}
	}

	nonce1, ciphertext1 := fixture.sealAt(t, 1, "one")
	if got := string(fixture.decrypt(t, 1, nonce1, ciphertext1)); got != "one" {
		t.Fatalf("seq 1 plaintext = %q", got)
	}
	nonce0, ciphertext0 := fixture.sealAt(t, 0, "zero")
	if got := string(fixture.decrypt(t, 0, nonce0, ciphertext0)); got != "zero" {
		t.Fatalf("seq 0 plaintext = %q", got)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 0 {
		t.Fatalf("skipped count after replaying backlog = %d, want 0", count)
	}
	if next := fixture.receiver.NextSequence(); next != 3 {
		t.Fatalf("head = %d, want 3", next)
	}

	for _, sequence := range []uint64{0, 1, 2} {
		nonce := nonceFor(sequence)
		_, err := fixture.receiver.Decrypt(sequence, nonce, ciphertext0, []byte("aad"))
		if !errors.Is(err, netsec.ErrDuplicateMessage) {
			t.Fatalf("duplicate seq %d: got %v, want ErrDuplicateMessage", sequence, err)
		}
	}
}

func TestFailedAuthenticationDoesNotConsumeSkippedKey(t *testing.T) {
	fixture := newFixture(t, "skipped-auth")

	nonce2, ciphertext2 := fixture.sealAt(t, 2, "two")
	if got := string(fixture.decrypt(t, 2, nonce2, ciphertext2)); got != "two" {
		t.Fatalf("seq 2 plaintext = %q", got)
	}

	nonce0, ciphertext0 := fixture.sealAt(t, 0, "zero")
	if _, err := fixture.receiver.Decrypt(0, nonce0, flipTag(ciphertext0), []byte("aad")); err == nil {
		t.Fatal("tampered skipped-key message authenticated")
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 2 {
		t.Fatalf("failed authentication dropped a key; count = %d, want 2", count)
	}
	if !fixture.receiver.HasSkippedKey(0) {
		t.Fatal("failed authentication consumed seq 0")
	}

	if got := string(fixture.decrypt(t, 0, nonce0, ciphertext0)); got != "zero" {
		t.Fatalf("seq 0 plaintext after retry = %q", got)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 1 {
		t.Fatalf("skipped count after successful retry = %d, want 1", count)
	}
	if _, err := fixture.receiver.Decrypt(0, nonce0, ciphertext0, []byte("aad")); !errors.Is(err, netsec.ErrDuplicateMessage) {
		t.Fatalf("reused skipped key error = %v, want ErrDuplicateMessage", err)
	}
}

func TestSharedStoreNamespacesSkippedKeysByRatchetKey(t *testing.T) {
	store := netsec.NewSkippedKeyStore()

	keyA := sha256.Sum256([]byte("chain-a"))
	keyB := sha256.Sum256([]byte("chain-b"))
	pubA := sha256.Sum256([]byte("ratchet-a"))
	pubB := sha256.Sum256([]byte("ratchet-b"))

	chainA, err := netsec.NewReceiveChain(pubA[:], keyA[:], netsec.WithSkippedKeyStore(store))
	if err != nil {
		t.Fatal(err)
	}
	chainB, err := netsec.NewReceiveChain(pubB[:], keyB[:], netsec.WithSkippedKeyStore(store))
	if err != nil {
		t.Fatal(err)
	}
	senderA := netsec.MustNewSenderChain(keyA[:])
	senderB := netsec.MustNewSenderChain(keyB[:])

	seal := func(sender *netsec.SenderChain, sequence uint64, plaintext string) ([]byte, []byte) {
		nonce := nonceFor(sequence)
		ciphertext, err := sender.SealAt(sequence, nonce, []byte(plaintext), []byte("aad"))
		if err != nil {
			t.Fatal(err)
		}
		return nonce, ciphertext
	}
	decrypt := func(chain *netsec.ReceiveChain, sequence uint64, nonce, ciphertext []byte) string {
		plaintext, err := chain.Decrypt(sequence, nonce, ciphertext, []byte("aad"))
		if err != nil {
			t.Fatalf("Decrypt(%d): %v", sequence, err)
		}
		return string(plaintext)
	}

	nonceA2, ciphertextA2 := seal(senderA, 2, "a-two")
	nonceB2, ciphertextB2 := seal(senderB, 2, "b-two")
	if got := decrypt(chainA, 2, nonceA2, ciphertextA2); got != "a-two" {
		t.Fatalf("chain A seq 2 = %q", got)
	}
	if got := decrypt(chainB, 2, nonceB2, ciphertextB2); got != "b-two" {
		t.Fatalf("chain B seq 2 = %q", got)
	}
	if ids := store.IDs(); len(ids) != 4 {
		t.Fatalf("store IDs = %d, want 4", len(ids))
	}

	nonceA0, ciphertextA0 := seal(senderA, 0, "a-zero")
	nonceB0, ciphertextB0 := seal(senderB, 0, "b-zero")
	if got := decrypt(chainA, 0, nonceA0, ciphertextA0); got != "a-zero" {
		t.Fatalf("chain A seq 0 = %q", got)
	}
	if !store.Contains(netsec.NewSkippedKeyID(pubB[:], 0)) {
		t.Fatal("consuming chain A sequence 0 removed chain B's key")
	}
	if got := decrypt(chainB, 0, nonceB0, ciphertextB0); got != "b-zero" {
		t.Fatalf("chain B seq 0 = %q", got)
	}
}

func TestConfigurableLowerSkipLimit(t *testing.T) {
	chainKey := sha256.Sum256([]byte("small-limit"))
	ratchetKey := sha256.Sum256([]byte("small-limit-ratchet"))

	chain, err := netsec.NewReceiveChain(ratchetKey[:], chainKey[:], netsec.WithMaxSkippedKeys(2))
	if err != nil {
		t.Fatal(err)
	}
	sender := netsec.MustNewSenderChain(chainKey[:])

	nonce2 := nonceFor(2)
	ciphertext2, err := sender.SealAt(2, nonce2, []byte("two"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.Decrypt(2, nonce2, ciphertext2, []byte("aad")); err != nil {
		t.Fatalf("seq 2 at configured limit: %v", err)
	}

	nonce4 := nonceFor(4)
	ciphertext4, err := sender.SealAt(4, nonce4, []byte("four"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.Decrypt(4, nonce4, ciphertext4, []byte("aad")); !errors.Is(err, netsec.ErrSkipLimitExceeded) {
		t.Fatalf("seq 4 over configured limit: got %v", err)
	}
}

func TestRejectsRaisedOrInvalidSkipLimit(t *testing.T) {
	chainKey := sha256.Sum256([]byte("invalid-limit"))
	ratchetKey := sha256.Sum256([]byte("invalid-limit-ratchet"))

	for _, limit := range []int{-1, netsec.MaxSkippedKeysPerChain + 1} {
		if _, err := netsec.NewReceiveChain(
			ratchetKey[:],
			chainKey[:],
			netsec.WithMaxSkippedKeys(limit),
		); !errors.Is(err, netsec.ErrInvalidMaxSkippedKeys) {
			t.Fatalf("limit %d: got %v, want ErrInvalidMaxSkippedKeys", limit, err)
		}
	}
}

func TestSequenceAcceptsIntegerCallSites(t *testing.T) {
	fixture := newFixture(t, "sequence-types")

	var sequence32 uint32
	nonce, ciphertext := fixture.sealAt(t, 0, "first")
	plaintext, err := fixture.receiver.Decrypt(sequence32, nonce, ciphertext, []byte("aad"))
	if err != nil {
		t.Fatalf("uint32 sequence: %v", err)
	}
	if got := string(plaintext); got != "first" {
		t.Fatalf("uint32 sequence plaintext = %q", got)
	}

	sequenceInt := 1
	nonce, ciphertext = fixture.sealAt(t, 1, "second")
	plaintext, err = fixture.receiver.Decrypt(sequenceInt, nonce, ciphertext, []byte("aad"))
	if err != nil {
		t.Fatalf("int sequence: %v", err)
	}
	if got := string(plaintext); got != "second" {
		t.Fatalf("int sequence plaintext = %q", got)
	}

	next := fixture.receiver.NextSequence()
	if _, err := fixture.receiver.Decrypt(-1, nonce, ciphertext, []byte("aad")); !errors.Is(err, netsec.ErrInvalidSequence) {
		t.Fatalf("negative sequence error = %v", err)
	}
	if got := fixture.receiver.NextSequence(); got != next {
		t.Fatalf("negative sequence changed head from %d to %d", next, got)
	}
}

func TestSkippedKeyConsumedOnceUnderConcurrency(t *testing.T) {
	fixture := newFixture(t, "concurrent-skipped")

	nonce2, ciphertext2 := fixture.sealAt(t, 2, "two")
	if got := string(fixture.decrypt(t, 2, nonce2, ciphertext2)); got != "two" {
		t.Fatalf("seq 2 plaintext = %q", got)
	}

	nonce0, ciphertext0 := fixture.sealAt(t, 0, "zero")
	const attempts = 16
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := fixture.receiver.Decrypt(0, nonce0, ciphertext0, []byte("aad"))
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	duplicates := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, netsec.ErrDuplicateMessage):
			duplicates++
		default:
			t.Fatalf("unexpected concurrent decrypt error: %v", err)
		}
	}
	if successes != 1 || duplicates != attempts-1 {
		t.Fatalf("successes = %d, duplicates = %d", successes, duplicates)
	}
	if count := fixture.receiver.SkippedKeyCount(); count != 1 {
		t.Fatalf("skipped count = %d, want 1", count)
	}
}
