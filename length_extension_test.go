package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

// legacyPrefixMACServer is a test-only oracle.  It holds the secret privately
// and exposes only a callback that accepts a message and tag.
type legacyPrefixMACServer struct {
	secret []byte
}

func newLegacyPrefixMACServer(secret []byte) *legacyPrefixMACServer {
	return &legacyPrefixMACServer{secret: cloneBytes(secret)}
}

func (server *legacyPrefixMACServer) verify(message, tag []byte) bool {
	h := sha256.New()
	_, _ = h.Write(server.secret)
	_, _ = h.Write(message)
	expected := h.Sum(nil)
	return hmac.Equal(tag, expected)
}

func TestAttackFindsEverySecretLength(t *testing.T) {
	t.Parallel()

	message := []byte("user=alice")
	suffix := []byte("&role=admin")

	for secretLen := MinSecretLength; secretLen <= MaxSecretLength; secretLen++ {
		secretLen := secretLen
		t.Run(fmt.Sprintf("secret_len_%d", secretLen), func(t *testing.T) {
			t.Parallel()

			secret := bytes.Repeat([]byte{byte(secretLen)}, secretLen)
			server := newLegacyPrefixMACServer(secret)
			originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

			result, err := AttackSHA256PrefixMAC(message, originalTag[:], suffix, server.verify)
			if err != nil {
				t.Fatalf("AttackSHA256PrefixMAC() error = %v", err)
			}
			if !result.Found() {
				t.Fatal("AttackSHA256PrefixMAC() did not find the secret length")
			}
			if result.Witness.SecretLength != secretLen {
				t.Fatalf("accepted secret length = %d, want %d", result.Witness.SecretLength, secretLen)
			}
			if result.Queries != secretLen {
				t.Fatalf("queries = %d, want %d", result.Queries, secretLen)
			}
			if !server.verify(result.Witness.ForgedMessage, result.Witness.ForgedTag) {
				t.Fatal("forged witness was not accepted by the legacy server")
			}
		})
	}
}

func TestAttackPaddingBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		secretLen  int
		messageLen int
	}{
		{"remaining_55", 10, 45},
		{"remaining_56_crosses_block", 10, 46},
		{"remaining_63_crosses_block", 10, 53},
		{"total_64", 9, 55},
		{"total_119", 9, 110},
		{"total_120", 9, 111},
		{"total_128", 64, 64},
		{"total_192", 64, 128},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			secret := bytes.Repeat([]byte{0xa5}, tc.secretLen)
			message := bytes.Repeat([]byte{0x42}, tc.messageLen)
			suffix := []byte("&next=1")
			server := newLegacyPrefixMACServer(secret)
			originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

			result, err := AttackSHA256PrefixMACInRange(message, originalTag[:], suffix,
				tc.secretLen, tc.secretLen, server.verify)
			if err != nil {
				t.Fatalf("attack error = %v", err)
			}
			if result.Queries != 1 {
				t.Fatalf("queries = %d, want 1", result.Queries)
			}

			witness := result.Witness
			if witness == nil {
				t.Fatal("attack returned no witness")
			}
			if witness.OriginalLength != uint64(tc.secretLen+tc.messageLen) {
				t.Fatalf("OriginalLength = %d, want %d", witness.OriginalLength, tc.secretLen+tc.messageLen)
			}
			if witness.PaddedPrefixLength%SHA256BlockSize != 0 {
				t.Fatalf("PaddedPrefixLength = %d, not block-aligned", witness.PaddedPrefixLength)
			}
			if len(witness.ForgedMessage) == 0 {
				t.Fatal("forged message is empty")
			}
		})
	}
}

func TestAttackEmptyMessage(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef")
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(secret)
	suffix := []byte("field=value")

	result, err := AttackSHA256PrefixMAC(nil, originalTag[:], suffix, server.verify)
	if err != nil {
		t.Fatalf("AttackSHA256PrefixMAC() error = %v", err)
	}
	if !result.Found() {
		t.Fatal("attack did not find the empty-message forgery")
	}
	if len(result.Witness.OriginalMessage) != 0 {
		t.Fatalf("OriginalMessage length = %d, want 0", len(result.Witness.OriginalMessage))
	}
	if !server.verify(result.Witness.ForgedMessage, result.Witness.ForgedTag) {
		t.Fatal("forged empty-message witness was not accepted")
	}
}

func TestAttackEmptySuffix(t *testing.T) {
	t.Parallel()

	secret := []byte("correct horse battery staple")
	message := []byte("user=alice")
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

	result, err := AttackSHA256PrefixMAC(message, originalTag[:], nil, server.verify)
	if err != nil {
		t.Fatalf("AttackSHA256PrefixMAC() error = %v", err)
	}
	if !result.Found() {
		t.Fatal("attack did not find the empty-suffix forgery")
	}
	if len(result.Witness.Suffix) != 0 {
		t.Fatalf("Suffix length = %d, want 0", len(result.Witness.Suffix))
	}
	if !server.verify(result.Witness.ForgedMessage, result.Witness.ForgedTag) {
		t.Fatal("forged empty-suffix witness was not accepted")
	}
}

func TestAttackEmptyMessageAndSuffix(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x7e}, 33)
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(secret)

	result, err := AttackSHA256PrefixMAC(nil, originalTag[:], nil, server.verify)
	if err != nil {
		t.Fatalf("AttackSHA256PrefixMAC() error = %v", err)
	}
	if !result.Found() {
		t.Fatal("attack did not find the empty-message/empty-suffix forgery")
	}
	if !bytes.Equal(result.Witness.ForgedMessage, result.Witness.Padding) {
		t.Fatal("empty-message/empty-suffix forged message should be the padding block")
	}
	if !server.verify(result.Witness.ForgedMessage, result.Witness.ForgedTag) {
		t.Fatal("forged empty-message/empty-suffix witness was not accepted")
	}
}

func TestAttackWrongTagReportsRangeMiss(t *testing.T) {
	t.Parallel()

	message := []byte("original")
	suffix := []byte("appended")
	server := newLegacyPrefixMACServer([]byte("server-secret"))

	wrongTag := bytes.Repeat([]byte{0xff}, SHA256TagSize)
	result, err := AttackSHA256PrefixMAC(message, wrongTag, suffix, server.verify)
	if !errors.Is(err, ErrRangeMiss) {
		t.Fatalf("attack error = %v, want ErrRangeMiss", err)
	}
	if result.Found() {
		t.Fatal("attack unexpectedly found a witness for a wrong tag")
	}
	if result.Queries != DefaultSecretLengthRange().Count() {
		t.Fatalf("queries = %d, want %d", result.Queries, DefaultSecretLengthRange().Count())
	}
	if result.Range != DefaultSecretLengthRange() {
		t.Fatalf("range = %+v, want %+v", result.Range, DefaultSecretLengthRange())
	}
	if !result.RangeMissed() {
		t.Fatal("RangeMissed() = false, want true")
	}
}

func TestAttackSecretOutsideRangeReportsMiss(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x11}, 40)
	message := []byte("m")
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(append(cloneBytes(secret), message...))
	suffix := []byte("x")

	result, err := AttackSHA256PrefixMACInRange(message, originalTag[:], suffix, 1, 10, server.verify)
	if !errors.Is(err, ErrRangeMiss) {
		t.Fatalf("attack error = %v, want ErrRangeMiss", err)
	}
	if result.Queries != 10 {
		t.Fatalf("queries = %d, want 10", result.Queries)
	}
	if result.Range != (SecretLengthRange{Min: 1, Max: 10}) {
		t.Fatalf("range = %+v", result.Range)
	}
}

func TestAttackSuffixWithExistingSeparators(t *testing.T) {
	t.Parallel()

	secret := []byte("secret|with&separators")
	message := []byte("user=alice|role=user")
	suffix := []byte("|role=admin&next=\x00\n;\xff")
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

	result, err := AttackSHA256PrefixMAC(message, originalTag[:], suffix, server.verify)
	if err != nil {
		t.Fatalf("attack error = %v", err)
	}
	if !result.Found() {
		t.Fatal("attack did not find a witness")
	}
	if !bytes.Equal(result.Witness.Suffix, suffix) {
		t.Fatalf("witness suffix = %q, want %q", result.Witness.Suffix, suffix)
	}
	if !server.verify(result.Witness.ForgedMessage, result.Witness.ForgedTag) {
		t.Fatal("separator-containing forged witness was not accepted")
	}
}

func TestAttackReturnsFirstAcceptedQueryAndFullWitness(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdefg")
	message := []byte("operation=read")
	suffix := []byte("&operation=write")
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

	result, err := AttackSHA256PrefixMACInRange(message, originalTag[:], suffix, 10, 20, server.verify)
	if err != nil {
		t.Fatalf("attack error = %v", err)
	}

	witness := result.Witness
	if witness == nil {
		t.Fatal("attack returned no witness")
	}
	if witness.SecretLength != len(secret) {
		t.Fatalf("SecretLength = %d, want %d", witness.SecretLength, len(secret))
	}
	if result.Queries != len(secret)-10+1 {
		t.Fatalf("queries = %d, want %d", result.Queries, len(secret)-10+1)
	}
	if witness.QueryNumber != result.Queries {
		t.Fatalf("QueryNumber = %d, want %d", witness.QueryNumber, result.Queries)
	}
	if !bytes.Equal(witness.OriginalMessage, message) {
		t.Fatal("witness OriginalMessage mismatch")
	}
	if !bytes.Equal(witness.OriginalTag, originalTag[:]) {
		t.Fatal("witness OriginalTag mismatch")
	}
	if !bytes.Equal(witness.ForgedMessage, append(append(cloneBytes(message), witness.Padding...), suffix...)) {
		t.Fatal("ForgedMessage is not message || padding || suffix")
	}
	if !server.verify(witness.ForgedMessage, witness.ForgedTag) {
		t.Fatal("forged witness was not accepted")
	}
}

func TestForgeWrongLengthIsNotAccepted(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x33}, 17)
	message := []byte("message")
	suffix := []byte("suffix")
	server := newLegacyPrefixMACServer(secret)
	originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

	witness, err := ForgeSHA256PrefixMAC(message, originalTag[:], suffix, 16)
	if err != nil {
		t.Fatalf("ForgeSHA256PrefixMAC() error = %v", err)
	}
	if server.verify(witness.ForgedMessage, witness.ForgedTag) {
		t.Fatal("forgery for the wrong secret length was accepted")
	}
}

func TestForgeConvenienceOutputMatchesWitness(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789")
	message := []byte("m")
	suffix := []byte("s")
	originalTag := sha256.Sum256(append(cloneBytes(secret), message...))

	forgedMessage, forgedTag, err := ForgeSHA256LengthExtension(message, originalTag[:], suffix, len(secret))
	if err != nil {
		t.Fatalf("ForgeSHA256LengthExtension() error = %v", err)
	}

	witness, err := ForgeSHA256PrefixMAC(message, originalTag[:], suffix, len(secret))
	if err != nil {
		t.Fatalf("ForgeSHA256PrefixMAC() error = %v", err)
	}
	if !bytes.Equal(forgedMessage, witness.ForgedMessage) {
		t.Fatal("forged message mismatch")
	}
	if !bytes.Equal(forgedTag, witness.ForgedTag) {
		t.Fatal("forged tag mismatch")
	}

	server := newLegacyPrefixMACServer(secret)
	if !server.verify(forgedMessage, forgedTag) {
		t.Fatal("convenience forgery was not accepted")
	}
}

func TestAttackRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	verify := func(message, tag []byte) bool { return false }
	validMessage := []byte("m")
	validTag := bytes.Repeat([]byte{0}, SHA256TagSize)

	if _, err := AttackSHA256PrefixMAC(validMessage, validTag[:31], nil, verify); !errors.Is(err, ErrInvalidTagSize) {
		t.Fatalf("short tag error = %v, want ErrInvalidTagSize", err)
	}
	if _, err := ForgeSHA256PrefixMAC(validMessage, validTag[:31], nil, 10); !errors.Is(err, ErrInvalidTagSize) {
		t.Fatalf("Forge short tag error = %v, want ErrInvalidTagSize", err)
	}
	if _, _, err := ForgeSHA256LengthExtension(validMessage, validTag[:31], nil, 10); !errors.Is(err, ErrInvalidTagSize) {
		t.Fatalf("ForgeSHA256LengthExtension short tag error = %v, want ErrInvalidTagSize", err)
	}
	if _, _, err := ExtendSHA256(validTag[:31], nil, 0); !errors.Is(err, ErrInvalidTagSize) {
		t.Fatalf("ExtendSHA256 short tag error = %v, want ErrInvalidTagSize", err)
	}
	if _, err := AttackSHA256PrefixMAC(validMessage, validTag, nil, nil); !errors.Is(err, ErrNilVerifier) {
		t.Fatalf("nil verifier error = %v, want ErrNilVerifier", err)
	}
	for _, badRange := range [][2]int{{0, 10}, {1, 65}, {20, 10}, {-1, 5}} {
		if _, err := AttackSHA256PrefixMACInRange(validMessage, validTag, nil, badRange[0], badRange[1], verify); !errors.Is(err, ErrInvalidSecretRange) {
			t.Fatalf("range %v error = %v, want ErrInvalidSecretRange", badRange, err)
		}
	}
	for _, badLength := range []int{0, -1, 65} {
		if _, err := ForgeSHA256PrefixMAC(validMessage, validTag, nil, badLength); !errors.Is(err, ErrInvalidSecretLength) {
			t.Fatalf("secret length %d error = %v, want ErrInvalidSecretLength", badLength, err)
		}
	}
}

func TestSecretLengthRangeHelpers(t *testing.T) {
	t.Parallel()

	range10to20 := SecretLengthRange{Min: 10, Max: 20}
	if range10to20.Count() != 11 {
		t.Fatalf("Count() = %d, want 11", range10to20.Count())
	}
	if !range10to20.Contains(10) || !range10to20.Contains(20) {
		t.Fatal("Contains() rejected an inclusive endpoint")
	}
	if range10to20.Contains(9) || range10to20.Contains(21) {
		t.Fatal("Contains() accepted a value outside the range")
	}
	if got := (SecretLengthRange{Min: 20, Max: 10}).Count(); got != 0 {
		t.Fatalf("invalid range Count() = %d, want 0", got)
	}
	if (AttackResult{}).RangeMissed() {
		t.Fatal("empty result without completed queries reported a range miss")
	}
}
