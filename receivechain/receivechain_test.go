package receivechain_test

import (
	"crypto/sha256"
	"errors"
	"math"
	"testing"

	"example.org/netsec/q0031/receivechain"
)

func TestReExportedReceiveChainAPI(t *testing.T) {
	chainKey := sha256.Sum256([]byte("alias-chain"))
	ratchetKey := sha256.Sum256([]byte("alias-ratchet"))

	chain, err := receivechain.New(ratchetKey[:], chainKey[:])
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sender, err := receivechain.NewSenderChain(chainKey[:])
	if err != nil {
		t.Fatalf("NewSenderChain: %v", err)
	}

	nonce := make([]byte, 12)
	ciphertext, err := sender.SealAt(0, nonce, []byte("hello"), nil)
	if err != nil {
		t.Fatalf("SealAt: %v", err)
	}
	plaintext, err := chain.Decrypt(0, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(plaintext) != "hello" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	if _, err := chain.Decrypt(uint64(math.MaxUint64), nonce, ciphertext, nil); !errors.Is(err, receivechain.ErrSkipLimitExceeded) {
		t.Fatalf("huge sequence error = %v", err)
	}
}
