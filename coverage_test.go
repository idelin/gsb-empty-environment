package netsec

import (
	"bytes"
	"strings"
	"testing"
)

func int64ptr(v int64) *int64 { return &v }

func mustParse(t *testing.T, header string) Signature {
	t.Helper()
	sig, err := ParseDKIMSignature(header)
	if err != nil {
		t.Fatalf("ParseDKIMSignature(%q): %v", header, err)
	}
	return sig
}

func signatureHeader(domain, selector, algorithm, canon string, length *int64) string {
	header := "DKIM-Signature: v=1; a=" + algorithm + "; c=" + canon +
		"; d=" + domain + "; s=" + selector + "; h=from:to:subject; bh=YWJj; b=YWJj"
	if length != nil {
		header += "; l=" + itoa(*length)
	}
	return header
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return string(digits[i:])
}

func canonicalLen(t *testing.T, body []byte, mode CanonMode) int64 {
	t.Helper()
	got, err := CanonicalizeBody(body, mode)
	if err != nil {
		t.Fatalf("CanonicalizeBody: %v", err)
	}
	return int64(len(got))
}

func TestCanonicalizationRFCExamples(t *testing.T) {
	body := []byte(" C \r\nD \t E\r\n\r\n\r\n")
	simple, relaxed, err := CanonicalizeBoth(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(simple) != " C \r\nD \t E\r\n" {
		t.Fatalf("simple = %q", simple)
	}
	if string(relaxed) != " C\r\nD E\r\n" {
		t.Fatalf("relaxed = %q", relaxed)
	}
}

func TestCanonicalizationTrailingEmptyLinesAndNoFinalCRLF(t *testing.T) {
	tests := []struct {
		body    string
		simple  string
		relaxed string
	}{
		{"", "", ""},
		{"\r\n\r\n", "", ""},
		{"abc", "abc\r\n", "abc\r\n"},
		{"abc\r\n\r\n\r\n", "abc\r\n", "abc\r\n"},
		{"abc\r\n \t\r\n", "abc\r\n \t\r\n", "abc\r\n"},
	}
	for _, tt := range tests {
		simple, relaxed, err := CanonicalizeBoth([]byte(tt.body))
		if err != nil {
			t.Fatal(err)
		}
		if string(simple) != tt.simple {
			t.Errorf("body %q simple = %q, want %q", tt.body, simple, tt.simple)
		}
		if string(relaxed) != tt.relaxed {
			t.Errorf("body %q relaxed = %q, want %q", tt.body, relaxed, tt.relaxed)
		}
	}
}

func TestSplitCRLF(t *testing.T) {
	receiver := NewBodyReceiver()
	for _, chunk := range []string{"Hel", "lo\r", "\nWor", "ld\r", "\n\r", "\n"} {
		if _, err := receiver.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	views, err := receiver.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(views.Simple.Canonical); got != "Hello\r\nWorld\r\n" {
		t.Fatalf("simple = %q", got)
	}
	if got := string(views.Relaxed.Canonical); got != "Hello\r\nWorld\r\n" {
		t.Fatalf("relaxed = %q", got)
	}
}

func TestTailHTMLIsNotAuthenticated(t *testing.T) {
	prefix := "Hello\r\n"
	body := []byte(prefix + "<script>alert(1)</script>\r\n")
	l := int64(len(prefix))
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated || result.Decision == DecisionAuthenticated {
		t.Fatalf("tail HTML was marked fully authenticated: %+v", result)
	}
	if result.Decision != DecisionPartial {
		t.Fatalf("decision = %q", result.Decision)
	}
	cov := result.Signatures[0]
	if cov.CoveredCanonicalBytes != l || cov.CanonicalBodyBytes == l {
		t.Fatalf("unexpected coverage: %+v", cov)
	}
	if cov.RawCovered.End != l {
		t.Fatalf("raw coverage end = %d, want %d", cov.RawCovered.End, l)
	}
}

func TestMIMEBoundaryContinuationIsNotAuthenticated(t *testing.T) {
	prefix := "Preamble\r\n"
	body := []byte(prefix + "--boundary\r\nContent-Type: text/html\r\n\r\n<b>tail</b>\r\n")
	l := int64(len(prefix))
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "relaxed/relaxed", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated {
		t.Fatal("MIME boundary continuation was marked fully authenticated")
	}
	if !strings.Contains(result.Signatures[0].Reason, "canonical body bytes") {
		t.Fatalf("reason = %q", result.Signatures[0].Reason)
	}
}

func TestBlankLineBeforeVisibleTailIsNotEnough(t *testing.T) {
	prefix := "Hello\r\n"
	body := []byte(prefix + "\r\n<script>alert(1)</script>\r\n")
	l := int64(len(prefix))
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))
	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated {
		t.Fatal("blank line plus visible tail was marked full")
	}
	if result.Signatures[0].TailOnlyDiscarded {
		t.Fatal("visible tail was treated as discarded")
	}
}

func TestQuotedPrintableSoftBreakIsNotDecoded(t *testing.T) {
	body := []byte("A=\r\nB\r\n")
	l := int64(len("A=\r\n"))
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated {
		t.Fatal("quoted-printable continuation was marked fully authenticated")
	}
	if got := result.Signatures[0].RawCovered.End; got != 4 {
		t.Fatalf("raw coverage end = %d, want 4", got)
	}
}

func TestBlankTailOnlyIsSafeForCompatibility(t *testing.T) {
	body := []byte("Hello\r\n\r\n\r\n")
	l := canonicalLen(t, body, CanonSimple)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FullBodyAuthenticated || result.Decision != DecisionAuthenticated {
		t.Fatalf("blank tail should be safe: %+v", result)
	}
	cov := result.Signatures[0]
	if !cov.TailOnlyDiscarded || cov.RawCovered.End != int64(len("Hello\r\n")) {
		t.Fatalf("unexpected blank-tail mapping: %+v", cov)
	}
	if result.Authenticated == nil || len(result.Authenticated) != 1 {
		t.Fatalf("authenticated range map = %+v", result.Authenticated)
	}
}

func TestHighTrustForbidsBodyLength(t *testing.T) {
	body := []byte("Hello\r\n\r\n")
	l := canonicalLen(t, body, CanonSimple)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyHighTrust)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated || result.Decision != DecisionRejected {
		t.Fatalf("high trust accepted l=: %+v", result)
	}
	if result.Signatures[0].PolicyAllowed {
		t.Fatal("l= was not rejected by high-trust policy")
	}
}

func TestLegalFullCoverage(t *testing.T) {
	body := []byte("Hello\r\nWorld\r\n")
	l := canonicalLen(t, body, CanonSimple)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FullBodyAuthenticated || result.Decision != DecisionAuthenticated {
		t.Fatalf("legal full coverage rejected: %+v", result)
	}
	if result.Signatures[0].RawCovered.End != int64(len(body)) {
		t.Fatalf("raw end = %d, want %d", result.Signatures[0].RawCovered.End, len(body))
	}

	noLength := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", nil))
	result, err = AnalyzeBody(body, []SignatureVerification{{Signature: noLength, Verified: true}}, PolicyHighTrust)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FullBodyAuthenticated {
		t.Fatalf("full signature rejected by high trust: %+v", result)
	}
}

func TestRelaxedWhitespaceMapping(t *testing.T) {
	body := []byte("A  B\r\n")
	l := int64(3)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "relaxed/relaxed", &l))

	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	cov := result.Signatures[0]
	if string(result.Signatures[0].Domain) != "example.com" {
		t.Fatal("domain lost")
	}
	if cov.RawCovered.End != 4 {
		t.Fatalf("relaxed raw end = %d, want 4", cov.RawCovered.End)
	}
	if result.FullBodyAuthenticated {
		t.Fatal("relaxed prefix was marked full")
	}
}

func TestMultipleSignaturesPreserveCoverage(t *testing.T) {
	body := []byte("Hello\r\nWorld\r\n")
	longFailed := int64(len(body))
	shortValid := canonicalLen(t, body[:7], CanonSimple)
	headers := []SignatureVerification{
		{Header: signatureHeader("example.com", "same", "rsa-sha256", "simple/simple", &longFailed), Verified: false},
		{Header: signatureHeader("example.com", "same", "rsa-sha256", "simple/simple", &shortValid), Verified: true},
	}

	result, err := AnalyzeBody(body, headers, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Signatures) != 2 {
		t.Fatalf("signatures = %d", len(result.Signatures))
	}
	if result.Signatures[0].Domain != "example.com" || result.Signatures[0].Selector != "same" {
		t.Fatalf("first signature identity lost: %+v", result.Signatures[0])
	}
	if result.Signatures[0].Verified || result.Signatures[0].FullyCoversBody {
		t.Fatal("failed long signature became full")
	}
	if result.FullBodyAuthenticated {
		t.Fatal("shorter valid signature overrode longer failed signature")
	}
	if len(result.Authenticated) != 1 {
		t.Fatalf("authenticated map = %+v", result.Authenticated)
	}
}

func TestBodyLengthBeyondCanonicalBodyFails(t *testing.T) {
	body := []byte("Hello\r\n")
	l := int64(len(body) + 1)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))
	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.Signatures[0].FullyCoversBody || result.Signatures[0].AuthenticatedPrefix {
		t.Fatalf("oversized l= was accepted: %+v", result.Signatures[0])
	}
	if !strings.Contains(result.Signatures[0].Reason, "exceeds") {
		t.Fatalf("reason = %q", result.Signatures[0].Reason)
	}
}

func TestIncompleteStreamNeverAuthenticates(t *testing.T) {
	body := []byte("Hello\r\nWorld\r\n")
	l := int64(len(body))
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))
	receiver := NewBodyReceiver()
	if _, err := receiver.Write(body[:8]); err != nil {
		t.Fatal(err)
	}
	result, err := AssessViews(receiver.Snapshot(), []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated || result.Decision != DecisionIncomplete {
		t.Fatalf("incomplete stream authenticated: %+v", result)
	}
}

func TestZeroLengthSignatureDoesNotCoverVisibleBody(t *testing.T) {
	body := []byte("<html>tail</html>\r\n")
	l := int64(0)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))
	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated || result.Decision != DecisionPartial {
		t.Fatalf("l=0 accepted visible body: %+v", result)
	}
	if result.Signatures[0].RawCovered.End != 0 {
		t.Fatalf("l=0 raw end = %d", result.Signatures[0].RawCovered.End)
	}
}

func TestRelaxedFullCoverageWithWhitespaceAndBlankTail(t *testing.T) {
	body := []byte("A  B \t\r\n\r\n")
	l := canonicalLen(t, body, CanonRelaxed)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "relaxed/relaxed", &l))
	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FullBodyAuthenticated {
		t.Fatalf("relaxed full coverage rejected: %+v", result)
	}
}

func TestPartialCRLFCoverage(t *testing.T) {
	body := []byte("Hello\r\n")
	l := int64(6)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "simple/simple", &l))
	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if result.FullBodyAuthenticated {
		t.Fatal("partial CRLF was marked full")
	}
	if got := result.Signatures[0].RawCovered.End; got != 6 {
		t.Fatalf("raw end = %d, want 6", got)
	}
	if result.Signatures[0].TailOnlyDiscarded {
		t.Fatal("uncovered LF was treated as discarded")
	}
}

func TestRelaxedWhitespaceOnlyTailIsSafe(t *testing.T) {
	body := []byte("A B\r\n \t\r\n")
	l := canonicalLen(t, body, CanonRelaxed)
	sig := mustParse(t, signatureHeader("example.com", "s1", "rsa-sha256", "relaxed/relaxed", &l))
	result, err := AnalyzeBody(body, []SignatureVerification{{Signature: sig, Verified: true}}, PolicyCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FullBodyAuthenticated {
		t.Fatalf("relaxed whitespace-only tail rejected: %+v", result)
	}
	if !result.Signatures[0].TailOnlyDiscarded {
		t.Fatal("whitespace-only tail not recognized as discarded")
	}
}

func TestChunkBoundariesMatchBufferedCanonicalization(t *testing.T) {
	body := []byte("A \t B\r\n\r\nC\r\n\r\n\r\n")
	bufferedSimple, bufferedRelaxed, err := CanonicalizeBoth(body)
	if err != nil {
		t.Fatal(err)
	}
	for split := 0; split <= len(body); split++ {
		receiver := NewBodyReceiver()
		if _, err := receiver.Write(body[:split]); err != nil {
			t.Fatal(err)
		}
		if _, err := receiver.Write(body[split:]); err != nil {
			t.Fatal(err)
		}
		views, err := receiver.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(views.Simple.Canonical, bufferedSimple) {
			t.Fatalf("split %d simple = %q, want %q", split, views.Simple.Canonical, bufferedSimple)
		}
		if !bytes.Equal(views.Relaxed.Canonical, bufferedRelaxed) {
			t.Fatalf("split %d relaxed = %q, want %q", split, views.Relaxed.Canonical, bufferedRelaxed)
		}
		if views.RawLength != int64(len(body)) {
			t.Fatalf("split %d raw length = %d", split, views.RawLength)
		}
	}
}
