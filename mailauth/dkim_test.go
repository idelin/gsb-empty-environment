package mailauth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func baseMessage() []byte {
	return []byte("From: alice@example.com\r\n" +
		"To: bob@example.org\r\n" +
		"Subject: Wire transfer confirmation\r\n" +
		"Date: Mon, 15 Jun 2026 12:00:00 +0000\r\n" +
		"Message-ID: <m1@example.com>\r\n" +
		"\r\n" +
		"Please confirm the transfer details below.\r\n" +
		"\r\n" +
		"Amount: 100 USD\r\n")
}

func newTestVerifier(t *testing.T, store *MemoryKeyStore) *Verifier {
	t.Helper()
	policy := DefaultPolicy()
	policy.Now = func() time.Time { return fixedNow }
	return NewVerifier(store, policy)
}

func TestRelaxedBodyCanonicalization(t *testing.T) {
	input := []byte("C \r\n" +
		" C\t\r\n" +
		"\r\n" +
		"\r\n")
	got := string(canonicalBody(canonRelaxed, input))
	want := "C\r\n C\r\n"
	if got != want {
		t.Fatalf("relaxed body mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestSimpleBodyCanonicalization(t *testing.T) {
	input := []byte("line one\r\nline two\r\n\r\n\r\n")
	got := string(canonicalBody(canonSimple, input))
	want := "line one\r\nline two\r\n"
	if got != want {
		t.Fatalf("simple body mismatch:\n got %q\nwant %q", got, want)
	}

	if got := string(canonicalBody(canonSimple, nil)); got != "\r\n" {
		t.Fatalf("empty simple body = %q, want CRLF", got)
	}
	if got := string(canonicalBody(canonRelaxed, []byte("\r\n\r\n"))); got != "\r\n" {
		t.Fatalf("empty relaxed body = %q, want CRLF", got)
	}
}

func TestEmptyBodyHash(t *testing.T) {
	sum := sha256.Sum256([]byte("\r\n"))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if got := bodyHash(canonRelaxed, nil, -1); got != want {
		t.Fatalf("empty body hash = %q, want %q", got, want)
	}
	if got := bodyHash(canonSimple, []byte("\r\n"), -1); got != want {
		t.Fatalf("simple empty body hash = %q, want %q", got, want)
	}
}

func TestRelaxedHeaderUnfoldingAndWSP(t *testing.T) {
	h := Header{
		Name:  "SubJect",
		Value: "  Hello\r\n \tWorld   ",
		Raw:   "SubJect:  Hello\r\n \tWorld   \r\n",
	}
	got := string(canonicalHeader(canonRelaxed, h))
	want := "subject:Hello World\r\n"
	if got != want {
		t.Fatalf("relaxed header = %q, want %q", got, want)
	}
	if simple := string(canonicalHeader(canonSimple, h)); simple != h.Raw {
		t.Fatalf("simple header changed wire bytes:\n got %q\nwant %q", simple, h.Raw)
	}
}

func TestOversignedMissingHeaderPlaceholder(t *testing.T) {
	if got := string(missingHeader(canonRelaxed, "From")); got != "from:\r\n" {
		t.Fatalf("relaxed missing header = %q", got)
	}
	if got := string(missingHeader(canonSimple, "From")); got != "\r\n" {
		t.Fatalf("simple missing header = %q", got)
	}
}

// injectHeaderAfterFirstFrom inserts a complete field immediately after the
// first From line, leaving the original From bytes intact.
func injectHeaderAfterFirstFrom(raw []byte, field string) []byte {
	text := string(raw)
	idx := strings.Index(text, "\r\n")
	if idx < 0 {
		return raw
	}
	out := text[:idx+2] + field + text[idx+2:]
	return []byte(out)
}

// canonBodyLen returns the canonicalized body length of a complete message,
// measured after the header/body split exactly as verification does.
func canonBodyLen(raw []byte, algo string) int64 {
	m, err := ParseMessage(raw)
	if err != nil {
		return -1
	}
	return int64(canonicalBodyLength(algo, m.Body))
}

func TestValidRSAAndEd25519Signatures(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}

	for _, alg := range []string{algRSASHA256, algEd25519SHA2} {
		store := NewMemoryKeyStore()
		keys.store(store, "example.com", "sel", alg)
		opt := defaultSignOptions()
		opt.algorithm = alg

		sig, err := signDKIM(baseMessage(), keys, opt)
		if err != nil {
			t.Fatalf("%s sign: %v", alg, err)
		}
		verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
		if err != nil {
			t.Fatalf("%s verify: %v", alg, err)
		}
		if !verdict.Accepted {
			t.Fatalf("%s verdict rejected: %s findings=%v", alg, verdict.Reason, verdict.Findings)
		}
		if len(verdict.DKIM) != 1 || !verdict.DKIM[0].Valid {
			t.Fatalf("%s expected one valid DKIM result, got %+v", alg, verdict.DKIM)
		}
	}
}

func TestSimpleCanonicalizationRoundTrip(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	opt := defaultSignOptions()
	opt.headerCanon = canonSimple
	opt.bodyCanon = canonSimple
	sig, err := signDKIM(baseMessage(), keys, opt)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Accepted {
		t.Fatalf("simple/simple message rejected: %s %v", verdict.Reason, verdict.Findings)
	}
}

func TestMultipleFromSpoofingRejected(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	sig, err := signDKIM(baseMessage(), keys, defaultSignOptions())
	if err != nil {
		t.Fatal(err)
	}

	attacker := withHeader(baseMessage(), "From: ceo@attacker.example\r\n")
	attacker = withHeader(attacker, sig)

	verdict, err := newTestVerifier(t, store).VerifyMessage(attacker)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted {
		t.Fatal("message with two From headers must be rejected")
	}
	if !verdict.HasCode(FindingMultipleFrom) {
		t.Fatalf("expected %s, got %v", FindingMultipleFrom, verdict.Findings)
	}
	if !verdict.HasCode(FindingExtraFromUnsigned) {
		t.Fatalf("expected %s, got %v", FindingExtraFromUnsigned, verdict.Findings)
	}
}

func TestFromNotCoveredRejected(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	opt := defaultSignOptions()
	opt.headers = []string{"to", "subject", "date", "message-id"}
	sig, err := signDKIM(baseMessage(), keys, opt)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted {
		t.Fatal("signature not covering From must be rejected")
	}
	if !verdict.HasCode(FindingFromNotCovered) {
		t.Fatalf("expected %s, got %v", FindingFromNotCovered, verdict.Findings)
	}
}

func TestOversignedFromBlocksInjection(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	opt := defaultSignOptions()
	opt.headers = []string{"from", "from", "to", "subject", "date", "message-id"}
	sig, err := signDKIM(baseMessage(), keys, opt)
	if err != nil {
		t.Fatal(err)
	}

	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Accepted {
		t.Fatalf("oversigned benign message rejected: %v", verdict.Findings)
	}
	if !verdict.HasCode(FindingFromOversigned) {
		t.Fatalf("expected informational %s", FindingFromOversigned)
	}

	attacker := injectHeaderAfterFirstFrom(baseMessage(), "From: ceo@attacker.example\r\n")
	attacker = withHeader(attacker, sig)
	verdict2, err := newTestVerifier(t, store).VerifyMessage(attacker)
	if err != nil {
		t.Fatal(err)
	}
	if verdict2.Accepted {
		t.Fatal("oversigning must detect the injected From header")
	}
	if !verdict2.HasCode(FindingMultipleFrom) {
		t.Fatalf("expected %s", FindingMultipleFrom)
	}
	if !verdict2.HasCode(FindingSignatureMismatch) && !verdict2.HasCode(FindingExtraFromUnsigned) {
		t.Fatalf("expected a signature/coverage failure, got %+v", verdict2.Findings)
	}
}

func TestBodyLengthTruncationInjection(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	opt := defaultSignOptions()
	opt.bodyCanon = canonRelaxed
	// Sign only the first canonical line via l=; everything after it is
	// unsigned and must be flagged when present on the wire.
	full := canonBodyLen(baseMessage(), canonRelaxed)
	opt.bodyLength = full - int64(len("Amount: 100 USD\r\n"))

	sig, err := signDKIM(baseMessage(), keys, opt)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	// Even the untouched message already contains bytes beyond the signed l=
	// prefix, so truncation injection must already be reported.
	if verdict.Accepted || !verdict.HasCode(FindingBodyLengthInjection) {
		t.Fatalf("l= shorter than body must flag truncation injection: %+v", verdict.Findings)
	}

	injected := append(append([]byte{}, baseMessage()...),
		[]byte("\r\nURGENT: wire funds to account 9999 immediately.\r\n")...)
	verdict2, err := newTestVerifier(t, store).VerifyMessage(withHeader(injected, sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict2.Accepted {
		t.Fatal("trailing content hidden by l= must be rejected as truncation injection")
	}
	if !verdict2.HasCode(FindingBodyLengthInjection) {
		t.Fatalf("expected %s, got %v", FindingBodyLengthInjection, verdict2.Findings)
	}
	if !verdict2.DKIM[0].Truncated {
		t.Fatal("DKIMResult.Truncated must be set when l= hides content")
	}
}

func TestBodyLengthExactThenAppendedInjection(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	// Sign the full canonical body with an exact l= boundary.
	opt := defaultSignOptions()
	opt.bodyCanon = canonRelaxed
	opt.bodyLength = canonBodyLen(baseMessage(), canonRelaxed)
	sig, err := signDKIM(baseMessage(), keys, opt)
	if err != nil {
		t.Fatal(err)
	}

	original := withHeader(baseMessage(), sig)
	verdict, err := newTestVerifier(t, store).VerifyMessage(original)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Accepted || verdict.DKIM[0].Truncated {
		t.Fatalf("exact l= boundary on the untouched body must accept: %+v", verdict.Findings)
	}

	// Attacker keeps the signed prefix byte-identical and appends a phishing
	// block; the original signature still verifies cryptographically.
	injected := append(append([]byte{}, baseMessage()...),
		[]byte("\r\nFrom the CEO: reply with your banking credentials now.\r\n")...)
	verdict2, err := newTestVerifier(t, store).VerifyMessage(withHeader(injected, sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict2.Accepted {
		t.Fatal("appended content behind an exact l= boundary must be rejected")
	}
	if !verdict2.HasCode(FindingBodyLengthInjection) || !verdict2.DKIM[0].Truncated {
		t.Fatalf("expected truncation injection flag: %+v", verdict2.Findings)
	}
}

func TestExpiredAndInvalidTimeRange(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	expired := defaultSignOptions()
	expired.expires = fixedNow.Add(-time.Hour)
	sig, err := signDKIM(baseMessage(), keys, expired)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted || !verdict.HasCode(FindingSignatureExpired) {
		t.Fatalf("expired signature must reject: %+v", verdict.Findings)
	}

	badRange := defaultSignOptions()
	badRange.created = fixedNow
	badRange.expires = fixedNow.Add(-time.Hour)
	sig2, err := signDKIM(baseMessage(), keys, badRange)
	if err != nil {
		t.Fatal(err)
	}
	verdict2, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig2))
	if err != nil {
		t.Fatal(err)
	}
	if verdict2.Accepted || !verdict2.HasCode(FindingInvalidTimeRange) {
		t.Fatalf("x<=t must reject: %+v", verdict2.Findings)
	}
}

func TestTamperedBodyFailsBodyHash(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	sig, err := signDKIM(baseMessage(), keys, defaultSignOptions())
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(baseMessage()), "100 USD", "9999 USD", 1)
	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader([]byte(tampered), sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted || !verdict.HasCode(FindingBodyHashMismatch) {
		t.Fatalf("tampered body must fail bh check: %+v", verdict.Findings)
	}
}

func TestBareLFNormalization(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	sig, err := signDKIM(baseMessage(), keys, defaultSignOptions())
	if err != nil {
		t.Fatal(err)
	}
	bareLF := strings.ReplaceAll(string(withHeader(baseMessage(), sig)), "\r\n", "\n")
	verdict, err := newTestVerifier(t, store).VerifyMessage([]byte(bareLF))
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Accepted {
		t.Fatalf("bare LF message must verify after CRLF normalization: %v", verdict.Findings)
	}
}

func TestLTagWithSimpleBodyRejected(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	keys.store(store, "example.com", "sel", algRSASHA256)

	opt := defaultSignOptions()
	opt.headerCanon = canonRelaxed
	opt.bodyCanon = canonSimple
	opt.bodyLength = 10
	sig, err := signDKIM(baseMessage(), keys, opt)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, store).VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted || !verdict.HasCode(FindingLTagSimpleCanon) {
		t.Fatalf("l= with simple body must be a permerror: %+v", verdict.Findings)
	}
}

func TestMissingPublicKeyRejected(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	// Sign with real material but verify against an empty store.
	sig, err := signDKIM(baseMessage(), keys, defaultSignOptions())
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, NewMemoryKeyStore()).
		VerifyMessage(withHeader(baseMessage(), sig))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted || !verdict.HasCode(FindingKeyUnavailable) {
		t.Fatalf("missing local key must reject without network access: %+v", verdict.Findings)
	}
}

func TestMalformedSignatureRejected(t *testing.T) {
	bad := withHeader(baseMessage(),
		"DKIM-Signature: v=1; a=rsa-sha256; this-is-not-a-tag; b=AA==\r\n")
	verdict, err := newTestVerifier(t, NewMemoryKeyStore()).VerifyMessage(bad)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted || !verdict.HasCode(FindingMalformedSignature) {
		t.Fatalf("malformed signature must be flagged: %+v", verdict.Findings)
	}
}

func TestMissingFromHeaderRejected(t *testing.T) {
	noFrom := []byte("To: bob@example.org\r\n" +
		"Subject: hi\r\n" +
		"\r\n" +
		"body\r\n")
	verdict, err := newTestVerifier(t, NewMemoryKeyStore()).VerifyMessage(noFrom)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Accepted || !verdict.HasCode(FindingMissingFrom) {
		t.Fatalf("message without From must be rejected: %+v", verdict.Findings)
	}
}
