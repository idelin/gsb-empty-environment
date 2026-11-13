package netsec_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"

	netsec "example.org/netsec/q0009"
)

const largeBudget = 1 << 20

func TestRecoverLastBlockWithRandomKeys(t *testing.T) {
	t.Parallel()

	fixtures := paddingFixtures(t)
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()

			key := fixture.key
			oracle, err := netsec.NewAESCBCPaddingOracle(key)
			if err != nil {
				t.Fatalf("create legacy oracle: %v", err)
			}
			if got := oracle(fixture.iv, fixture.ciphertext); got != netsec.PaddingValid {
				t.Fatalf("target must have valid padding, got %s", got)
			}

			result, err := netsec.RecoverLastBlock(oracle, fixture.iv, fixture.ciphertext, largeBudget)
			if err != nil {
				t.Fatalf("recover final block: %v", err)
			}
			if !result.Complete || !result.OracleEstablished {
				t.Fatalf("expected complete oracle recovery, got complete=%t established=%t reason=%s",
					result.Complete, result.OracleEstablished, result.StopReason)
			}
			if !bytes.Equal(result.PlaintextLastBlock, fixture.lastPlaintext) {
				t.Fatalf("recovered plaintext mismatch:\n got %x\nwant %x",
					result.PlaintextLastBlock, fixture.lastPlaintext)
			}
			if result.UnknownMask.UnknownCount() != 0 {
				t.Fatalf("complete result has %d unknown bytes", result.UnknownMask.UnknownCount())
			}
			if result.QueriesUsed != len(result.QueryLog) {
				t.Fatalf("queries used=%d, log length=%d", result.QueriesUsed, len(result.QueryLog))
			}
			if result.QueriesUsed < netsec.BlockSize*256 {
				t.Fatalf("all candidate guesses must be scanned; only %d queries", result.QueriesUsed)
			}
			if err := netsec.Replay(result.QueryLog, oracle); err != nil {
				t.Fatalf("replay query log: %v", err)
			}

			// The recovery API receives only the callback, never this key. Also
			// ensure the observable result does not accidentally serialize it.
			observable, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			if bytes.Contains(observable, key) {
				t.Fatal("result exposed the raw target key")
			}
		})
	}
}

func TestExactQueryBudgetAndPartialRecovery(t *testing.T) {
	t.Parallel()

	fixtures := paddingFixtures(t)
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()

			key := fixture.key
			oracle, err := netsec.NewAESCBCPaddingOracle(key)
			if err != nil {
				t.Fatalf("create legacy oracle: %v", err)
			}

			first, err := netsec.RecoverLastBlock(oracle, fixture.iv, fixture.ciphertext, largeBudget)
			if err != nil {
				t.Fatalf("unbounded recovery: %v", err)
			}
			exactBudget := first.QueriesUsed
			if exactBudget < netsec.BlockSize*256 {
				t.Fatalf("unexpectedly small exact budget: %d", exactBudget)
			}

			exact, err := netsec.RecoverLastBlock(oracle, fixture.iv, fixture.ciphertext, exactBudget)
			if err != nil {
				t.Fatalf("recovery with exact budget: %v", err)
			}
			if !exact.Complete {
				t.Fatalf("exact budget did not complete: %s", exact.StopReason)
			}
			if exact.QueriesUsed != exactBudget {
				t.Fatalf("used %d queries with a budget of %d", exact.QueriesUsed, exactBudget)
			}
			if !bytes.Equal(exact.PlaintextLastBlock, fixture.lastPlaintext) {
				t.Fatalf("exact-budget plaintext mismatch")
			}
			if err := netsec.Replay(exact.QueryLog, oracle); err != nil {
				t.Fatalf("replay exact-budget log: %v", err)
			}

			short, err := netsec.RecoverLastBlock(oracle, fixture.iv, fixture.ciphertext, exactBudget-1)
			if !errors.Is(err, netsec.ErrBudgetExhausted) {
				t.Fatalf("short budget error = %v, want ErrBudgetExhausted", err)
			}
			if short.Complete {
				t.Fatal("short budget incorrectly reported complete recovery")
			}
			if short.PlaintextLastBlock != nil {
				t.Fatal("short budget returned a plaintext block")
			}
			if short.QueriesUsed != exactBudget-1 {
				t.Fatalf("used %d queries with a budget of %d", short.QueriesUsed, exactBudget-1)
			}
			if short.UnknownMask.UnknownCount() == 0 {
				t.Fatal("short budget did not mark unknown bytes")
			}
			if short.QueryLog[len(short.QueryLog)-1].Index != short.QueriesUsed {
				t.Fatal("partial query log is not complete up to the budget")
			}
			if err := netsec.Replay(short.QueryLog, oracle); err != nil {
				t.Fatalf("replay partial log: %v", err)
			}
		})
	}
}

func TestInfrastructureErrorsAreNotPaddingInvalid(t *testing.T) {
	t.Parallel()

	fixture := paddingFixtures(t)[1]
	key := fixture.key
	base, err := netsec.NewAESCBCPaddingOracle(key)
	if err != nil {
		t.Fatalf("create legacy oracle: %v", err)
	}

	t.Run("transient", func(t *testing.T) {
		var calls int
		wrapped := func(iv, ciphertext []byte) netsec.Feedback {
			calls++
			if calls == 1 {
				return netsec.InfrastructureError
			}
			return base(iv, ciphertext)
		}

		result, err := netsec.RecoverLastBlock(wrapped, fixture.iv, fixture.ciphertext, largeBudget)
		if err != nil {
			t.Fatalf("transient infrastructure error should be retried once: %v", err)
		}
		if !result.Complete || !bytes.Equal(result.PlaintextLastBlock, fixture.lastPlaintext) {
			t.Fatal("recovery after retry did not complete correctly")
		}
		if result.QueryLog[0].Feedback != netsec.InfrastructureError {
			t.Fatal("first logged query is not the infrastructure error")
		}
		if result.QueryLog[1].Feedback == netsec.InfrastructureError {
			t.Fatal("retry did not reach the underlying oracle")
		}
	})

	t.Run("persistent", func(t *testing.T) {
		var calls int
		wrapped := func(iv, ciphertext []byte) netsec.Feedback {
			calls++
			if calls >= 100 {
				return netsec.InfrastructureError
			}
			return base(iv, ciphertext)
		}

		result, err := netsec.RecoverLastBlock(wrapped, fixture.iv, fixture.ciphertext, largeBudget)
		if !errors.Is(err, netsec.ErrInfrastructureFailure) {
			t.Fatalf("persistent error = %v, want ErrInfrastructureFailure", err)
		}
		if result.Complete || result.PlaintextLastBlock != nil {
			t.Fatal("persistent infrastructure failure produced plaintext")
		}
		if result.QueriesUsed != 101 {
			t.Fatalf("used %d calls, want exactly one bounded retry after call 100", result.QueriesUsed)
		}
		if result.UnknownMask.UnknownCount() != netsec.BlockSize {
			t.Fatalf("expected all bytes unknown, got %d", result.UnknownMask.UnknownCount())
		}
		if result.QueryLog[99].Feedback != netsec.InfrastructureError ||
			result.QueryLog[100].Feedback != netsec.InfrastructureError {
			t.Fatal("persistent failures were not both logged as infrastructure errors")
		}
	})
}

func TestAuthenticatedCallbackRefusesToEstablishOracle(t *testing.T) {
	t.Parallel()

	fixture := paddingFixtures(t)[1]
	encKey := fixture.key
	macKey := randomBytes(t, 32)

	fixed, err := netsec.NewAuthenticatedCBCPaddingOracle(
		encKey,
		macKey,
		fixture.iv,
		fixture.ciphertext,
	)
	if err != nil {
		t.Fatalf("create authenticated callback: %v", err)
	}

	if got := fixed(fixture.iv, fixture.ciphertext); got != netsec.PaddingValid {
		t.Fatalf("authenticated target should decrypt to valid padding, got %s", got)
	}

	modifiedIV := clone(fixture.iv)
	modifiedIV[0] ^= 0x80
	modifiedCiphertext := clone(fixture.ciphertext)
	modifiedCiphertext[0] ^= 0x01
	modifiedPrevious := clone(fixture.ciphertext)
	modifiedPrevious[len(modifiedPrevious)-netsec.BlockSize-1] ^= 0x40

	cases := []struct {
		name       string
		iv         []byte
		ciphertext []byte
	}{
		{name: "changed IV", iv: modifiedIV, ciphertext: fixture.ciphertext},
		{name: "changed body", iv: fixture.iv, ciphertext: modifiedCiphertext},
		{name: "changed previous block", iv: fixture.iv, ciphertext: modifiedPrevious},
		{name: "truncated body", iv: fixture.iv, ciphertext: fixture.ciphertext[:len(fixture.ciphertext)-netsec.BlockSize]},
		{name: "appended body", iv: fixture.iv, ciphertext: append(clone(fixture.ciphertext), make([]byte, netsec.BlockSize)...)},
		{name: "malformed IV", iv: fixture.iv[:netsec.BlockSize-1], ciphertext: fixture.ciphertext},
		{name: "empty body", iv: fixture.iv, ciphertext: nil},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// Malformed lengths are rejected too: authentication happens before
			// structural validation and before AES decryption.
			if got := fixed(test.iv, test.ciphertext); got != netsec.Rejected {
				t.Fatalf("unauthenticated change got %s, want uniform %s", got, netsec.Rejected)
			}
		})
	}

	result, err := netsec.RecoverLastBlock(fixed, fixture.iv, fixture.ciphertext, largeBudget)
	if !errors.Is(err, netsec.ErrOracleNotEstablished) {
		t.Fatalf("fixed callback error = %v, want ErrOracleNotEstablished", err)
	}
	if result.OracleEstablished || result.Complete {
		t.Fatal("fixed callback was incorrectly treated as a padding oracle")
	}
	if result.PlaintextLastBlock != nil {
		t.Fatal("fixed callback experiment returned a wrong/recovered plaintext")
	}
	if result.UnknownMask.UnknownCount() != netsec.BlockSize {
		t.Fatalf("expected all bytes unknown, got %d", result.UnknownMask.UnknownCount())
	}
	if result.QueriesUsed != 256 {
		t.Fatalf("used %d queries while establishing no oracle, want one complete 256-guess scan", result.QueriesUsed)
	}
	if err := netsec.Replay(result.QueryLog, fixed); err != nil {
		t.Fatalf("replay authenticated rejection log: %v", err)
	}
}

func TestPaddingValidationAndStructuralErrors(t *testing.T) {
	t.Parallel()

	key := randomBytes(t, 16)
	iv := randomBytes(t, netsec.BlockSize)
	oracle, err := netsec.NewAESCBCPaddingOracle(key)
	if err != nil {
		t.Fatalf("create oracle: %v", err)
	}

	validPaddings := []struct {
		name  string
		block []byte
	}{
		{name: "01", block: append(randomBytes(t, 15), 0x01)},
		{name: "0202", block: append(randomBytes(t, 14), 0x02, 0x02)},
		{name: "full", block: bytes.Repeat([]byte{0x10}, netsec.BlockSize)},
	}
	for _, test := range validPaddings {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ciphertext := encryptCBC(t, key, iv, test.block)
			if got := oracle(iv, ciphertext); got != netsec.PaddingValid {
				t.Fatalf("valid padding returned %s", got)
			}
		})
	}

	invalidPaddings := []struct {
		name  string
		block []byte
	}{
		{name: "zero length", block: append(randomBytes(t, 15), 0x00)},
		{name: "too long", block: append(randomBytes(t, 15), 0x11)},
		{name: "mismatched 0203", block: append(randomBytes(t, 14), 0x02, 0x03)},
		{name: "mismatched 0102", block: append(randomBytes(t, 14), 0x01, 0x02)},
	}
	for _, test := range invalidPaddings {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ciphertext := encryptCBC(t, key, iv, test.block)
			if got := oracle(iv, ciphertext); got != netsec.PaddingInvalid {
				t.Fatalf("invalid padding returned %s", got)
			}
		})
	}

	structural := []struct {
		name       string
		iv         []byte
		ciphertext []byte
	}{
		{name: "short IV", iv: iv[:netsec.BlockSize-1], ciphertext: make([]byte, netsec.BlockSize)},
		{name: "empty body", iv: iv, ciphertext: nil},
		{name: "partial block", iv: iv, ciphertext: make([]byte, netsec.BlockSize-1)},
	}
	for _, test := range structural {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if got := oracle(test.iv, test.ciphertext); got != netsec.InfrastructureError {
				t.Fatalf("structural error returned %s", got)
			}
		})
	}
}

func TestEnvelopeInputAndInvalidSizes(t *testing.T) {
	t.Parallel()

	fixture := paddingFixtures(t)[0]
	oracle, err := netsec.NewAESCBCPaddingOracle(fixture.key)
	if err != nil {
		t.Fatalf("create oracle: %v", err)
	}

	envelope := append(clone(fixture.iv), fixture.ciphertext...)
	result, err := netsec.RecoverLastBlockFromEnvelope(oracle, envelope, largeBudget)
	if err != nil {
		t.Fatalf("recover from envelope: %v", err)
	}
	if !bytes.Equal(result.PlaintextLastBlock, fixture.lastPlaintext) {
		t.Fatal("envelope recovery returned the wrong plaintext")
	}

	short, err := netsec.RecoverLastBlockFromEnvelope(oracle, envelope[:netsec.BlockSize+netsec.BlockSize-1], 10)
	if !errors.Is(err, netsec.ErrInvalidInput) {
		t.Fatalf("short envelope error = %v, want ErrInvalidInput", err)
	}
	if short.UnknownMask.UnknownCount() != netsec.BlockSize {
		t.Fatal("short envelope did not mark all bytes unknown")
	}
	if short.QueriesUsed != 0 || len(short.QueryLog) != 0 {
		t.Fatal("invalid input consumed callback queries")
	}

	if _, err := netsec.RecoverLastBlockWithConfig(oracle, fixture.iv, fixture.ciphertext, netsec.AttackConfig{
		Budget:                largeBudget,
		InfrastructureRetries: netsec.MaxInfrastructureRetries + 1,
	}); !errors.Is(err, netsec.ErrInvalidInput) {
		t.Fatalf("oversized retry configuration error = %v, want ErrInvalidInput", err)
	}
}

type paddingFixture struct {
	name          string
	key           []byte
	iv            []byte
	ciphertext    []byte
	lastPlaintext []byte
}

func paddingFixtures(t *testing.T) []paddingFixture {
	t.Helper()

	last01 := append(randomBytes(t, 15), 0x01)
	last0202 := append(randomBytes(t, 14), 0x02, 0x02)
	lastFull := bytes.Repeat([]byte{0x10}, netsec.BlockSize)

	plaintexts := []struct {
		name         string
		prefixBlocks int
		lastBlock    []byte
	}{
		{name: "single-block-ends-in-01", lastBlock: last01},
		{name: "ends-in-0202", prefixBlocks: 1, lastBlock: last0202},
		{name: "full-padding-block", prefixBlocks: 1, lastBlock: lastFull},
	}

	fixtures := make([]paddingFixture, 0, len(plaintexts))
	for _, test := range plaintexts {
		key := randomBytes(t, 16)
		iv := randomBytes(t, netsec.BlockSize)
		plaintext := randomBytes(t, test.prefixBlocks*netsec.BlockSize)
		plaintext = append(plaintext, test.lastBlock...)
		ciphertext := encryptCBC(t, key, iv, plaintext)

		fixtures = append(fixtures, paddingFixture{
			name:          test.name,
			key:           key,
			iv:            iv,
			ciphertext:    ciphertext,
			lastPlaintext: clone(test.lastBlock),
		})
	}
	return fixtures
}

func encryptCBC(t *testing.T, key, iv, plaintext []byte) []byte {
	t.Helper()

	if len(plaintext)%netsec.BlockSize != 0 {
		t.Fatalf("plaintext length %d is not block aligned", len(plaintext))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("create AES cipher: %v", err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, clone(iv)).CryptBlocks(ciphertext, plaintext)
	return ciphertext
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()

	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("read random bytes: %v", err)
	}
	return value
}

func clone(value []byte) []byte {
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
