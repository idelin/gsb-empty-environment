package arc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeSig stands in for DKIM cryptography: a signature's b= tag is the hex
// SHA-256 digest of the exact covered header set the signer saw. Swapping,
// inserting or deleting any covered header therefore breaks verification,
// which is precisely the attack this package must detect.
func fakeDigest(covered []Header) string {
	h := sha256.New()
	for _, c := range covered {
		value := c.Value
		// DKIM semantics: the signer's own b= tag is canonicalized as empty.
		if c.Kind == AMS || c.Kind == AS {
			value = setTag(value, "b", "")
		}
		fmt.Fprintf(h, "%s: %s\n", c.Kind.String(), value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fakeVerify(h Header, covered []Header) error {
	want := h.Tag("b")
	if want == "" {
		return errors.New("signature header has no b= tag")
	}
	if got := fakeDigest(covered); got != want {
		return fmt.Errorf("digest mismatch: want %s got %s", want, got)
	}
	return nil
}

func fakeVerifiers() Verifiers {
	return Verifiers{
		VerifySeal:             fakeVerify,
		VerifyMessageSignature: fakeVerify,
	}
}

func tagValue(v, tag string) string {
	for _, part := range strings.Split(v, ";") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(part[:eq]), tag) {
			return strings.TrimSpace(part[eq+1:])
		}
	}
	return ""
}

func setTag(v, tag, val string) string {
	prefix := tag + "="
	parts := strings.Split(v, ";")
	for idx, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq >= 0 && strings.EqualFold(strings.TrimSpace(part[:eq]), tag) {
			parts[idx] = " " + prefix + val
			return strings.Join(parts, ";")
		}
	}
	return v + "; " + prefix + val
}

func cvFor(i int) string {
	if i == 1 {
		return "none"
	}
	return "pass"
}

// renderChain builds a syntactically valid chain of n instances, then signs
// every AMS and AS with the fake scheme in sealing order.
func renderChain(n int) []RawHeader {
	var raws []RawHeader
	for i := 1; i <= n; i++ {
		raws = append(raws,
			RawHeader{"ARC-Authentication-Results", fmt.Sprintf("i=%d; spf=pass smtp.from=example%d.com", i, i)},
			RawHeader{"ARC-Message-Signature", fmt.Sprintf("i=%d; a=rsa-sha256; d=example%d.com; b=unsigned", i, i)},
			RawHeader{"ARC-Seal", fmt.Sprintf("i=%d; a=rsa-sha256; cv=%s; b=unsigned", i, cvFor(i))},
		)
	}
	resign(raws)
	return raws
}

// resign recomputes every b= tag. Signing order matters: AS(i) may only seal
// headers that already existed, so AMS are signed first and seals are then
// produced bottom-up.
func resign(raws []RawHeader) {
	parsed, err := ParseHeaders(raws)
	if err != nil {
		panic(err)
	}
	n := 0
	for _, h := range parsed {
		if h.Instance > n {
			n = h.Instance
		}
	}
	for idx := range raws {
		kind, _ := kindByName[strings.ToLower(strings.TrimSpace(raws[idx].Name))]
		if kind != AMS {
			continue
		}
		var i int
		fmt.Sscanf(tagValue(raws[idx].Value, "i"), "%d", &i)
		raws[idx].Value = setTag(raws[idx].Value, "b", fakeDigest(AMSCoveredHeaders(parsed, i)))
	}
	parsed, _ = ParseHeaders(raws)
	for i := 1; i <= n; i++ {
		for idx := range raws {
			kind, _ := kindByName[strings.ToLower(strings.TrimSpace(raws[idx].Name))]
			var num int
			fmt.Sscanf(tagValue(raws[idx].Value, "i"), "%d", &num)
			if kind == AS && num == i {
				raws[idx].Value = setTag(raws[idx].Value, "b", fakeDigest(SealCoveredHeaders(parsed, i)))
			}
		}
	}
}

func mustParse(t *testing.T, raws []RawHeader) []Header {
	t.Helper()
	parsed, err := ParseHeaders(raws)
	if err != nil {
		t.Fatalf("ParseHeaders failed: %v", err)
	}
	return parsed
}

func validate(t *testing.T, raws []RawHeader) *Result {
	t.Helper()
	res, err := ValidateChain(raws, fakeVerifiers())
	if err != nil {
		t.Fatalf("ValidateChain failed: %v", err)
	}
	return res
}

func hasFailure(res *Result, target error) bool {
	for _, f := range res.Failures {
		if errors.Is(f, target) {
			return true
		}
	}
	return false
}

func dropKind(raws []RawHeader, kind string, instance int) []RawHeader {
	out := make([]RawHeader, 0, len(raws))
	for _, r := range raws {
		if strings.EqualFold(r.Name, kind) && tagValue(r.Value, "i") == fmt.Sprint(instance) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func mutate(raws []RawHeader, kind string, instance int, fn func(string) string) {
	for idx := range raws {
		if strings.EqualFold(raws[idx].Name, kind) && tagValue(raws[idx].Value, "i") == fmt.Sprint(instance) {
			raws[idx].Value = fn(raws[idx].Value)
		}
	}
}

func tagOf(raws []RawHeader, kind string, instance int) string {
	for _, r := range raws {
		if strings.EqualFold(r.Name, kind) && tagValue(r.Value, "i") == fmt.Sprint(instance) {
			return r.Value
		}
	}
	panic("header not found")
}

func TestValidChainsPass(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5} {
		res := validate(t, renderChain(n))
		if res.Status != Pass {
			t.Fatalf("n=%d: status = %s, failures = %v", n, res.Status, res.Failures)
		}
		if res.Instances != n {
			t.Fatalf("n=%d: Instances = %d", n, res.Instances)
		}
	}
}

func TestMissingInstanceGap(t *testing.T) {
	// One header of an instance removed: the slot exists but is incomplete.
	raws := dropKind(renderChain(3), "ARC-Seal", 2)
	res := validate(t, raws)
	if res.Status != Fail || !hasFailure(res, ErrMissingInstance) {
		t.Fatalf("partial slot: got status=%s failures=%v, want Fail with ErrMissingInstance", res.Status, res.Failures)
	}

	// Whole instance removed: 1, 3 is not a continuous sequence.
	raws = renderChain(3)
	for _, kind := range []string{"ARC-Seal", "ARC-Message-Signature", "ARC-Authentication-Results"} {
		raws = dropKind(raws, kind, 2)
	}
	res = validate(t, raws)
	if res.Status != Fail || !hasFailure(res, ErrMissingInstance) {
		t.Fatalf("gap: got status=%s failures=%v", res.Status, res.Failures)
	}
}

func TestDuplicateHeader(t *testing.T) {
	raws := renderChain(2)
	raws = append(raws, RawHeader{"ARC-Seal", tagOf(raws, "ARC-Seal", 1)})
	res := validate(t, raws)
	if res.Status != Fail || !hasFailure(res, ErrDuplicateHeader) {
		t.Fatalf("got status=%s failures=%v, want Fail with ErrDuplicateHeader", res.Status, res.Failures)
	}
}

func TestCVMisplacement(t *testing.T) {
	tests := []struct {
		name     string
		instance int
		cv       string
	}{
		{"first seal must be none", 1, "pass"},
		{"first seal cannot be fail", 1, "fail"},
		{"later seal cannot be none", 2, "none"},
		{"later seal cannot be garbage", 2, "bogus"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raws := renderChain(3)
			mutate(raws, "ARC-Seal", tc.instance, func(v string) string {
				return setTag(v, "cv", tc.cv)
			})
			res := validate(t, raws)
			if res.Status != Fail || !hasFailure(res, ErrInvalidCV) {
				t.Fatalf("got status=%s failures=%v, want Fail with ErrInvalidCV", res.Status, res.Failures)
			}
		})
	}
}

func TestCVFailFailsChain(t *testing.T) {
	raws := renderChain(3)
	mutate(raws, "ARC-Seal", 2, func(v string) string {
		return setTag(v, "cv", "fail")
	})
	// Re-sign so the only defect is the policy-required cv=fail verdict.
	resign(raws)
	res := validate(t, raws)
	if res.Status != Fail || !hasFailure(res, ErrInvalidCV) {
		t.Fatalf("got status=%s failures=%v, want Fail with ErrInvalidCV", res.Status, res.Failures)
	}
}

func TestSwapAttacksBreakSignatures(t *testing.T) {
	t.Run("AAR payload swapped between instances", func(t *testing.T) {
		raws := renderChain(3)
		v1 := tagOf(raws, "ARC-Authentication-Results", 1)
		v2 := tagOf(raws, "ARC-Authentication-Results", 2)
		mutate(raws, "ARC-Authentication-Results", 1, func(string) string { return setTag(v2, "i", "1") })
		mutate(raws, "ARC-Authentication-Results", 2, func(string) string { return setTag(v1, "i", "2") })
		res := validate(t, raws)
		if res.Status != Fail || !hasFailure(res, ErrSealVerification) {
			t.Fatalf("got status=%s failures=%v, want seal verification failure", res.Status, res.Failures)
		}
	})

	t.Run("seal from another instance grafted in", func(t *testing.T) {
		raws := renderChain(3)
		foreign := tagOf(raws, "ARC-Seal", 1)
		mutate(raws, "ARC-Seal", 2, func(string) string { return setTag(setTag(foreign, "i", "2"), "cv", "pass") })
		res := validate(t, raws)
		if res.Status != Fail || !hasFailure(res, ErrSealVerification) {
			t.Fatalf("got status=%s failures=%v, want seal verification failure", res.Status, res.Failures)
		}
	})

	t.Run("latest AMS tampered after sealing", func(t *testing.T) {
		raws := renderChain(3)
		mutate(raws, "ARC-Message-Signature", 3, func(v string) string {
			return setTag(v, "d", "attacker.example")
		})
		res := validate(t, raws)
		if res.Status != Fail || !hasFailure(res, ErrMessageSignatureVerification) {
			t.Fatalf("got status=%s failures=%v, want AMS verification failure", res.Status, res.Failures)
		}
	})

	t.Run("extra forged instance inserted beyond N", func(t *testing.T) {
		raws := renderChain(2)
		raws = append(raws,
			RawHeader{"ARC-Authentication-Results", "i=3; spf=fail smtp.from=evil.example"},
			RawHeader{"ARC-Message-Signature", "i=3; a=rsa-sha256; d=evil.example; b=forged"},
			RawHeader{"ARC-Seal", "i=3; a=rsa-sha256; cv=pass; b=forged"},
		)
		res := validate(t, raws)
		if res.Status != Fail {
			t.Fatalf("got status=%s failures=%v, want Fail", res.Status, res.Failures)
		}
		if !hasFailure(res, ErrSealVerification) || !hasFailure(res, ErrMessageSignatureVerification) {
			t.Fatalf("forged instance: failures=%v", res.Failures)
		}
	})

	t.Run("AAR deletion leaves an incomplete instance", func(t *testing.T) {
		raws := dropKind(renderChain(3), "ARC-Authentication-Results", 2)
		res := validate(t, raws)
		if res.Status != Fail || !hasFailure(res, ErrMissingInstance) {
			t.Fatalf("got status=%s failures=%v, want structural failure", res.Status, res.Failures)
		}
	})
}

func TestShuffledHeaderOrderStillValidates(t *testing.T) {
	raws := renderChain(3)
	shuffled := []RawHeader{
		raws[8], raws[3], raws[0], raws[6], raws[1], raws[4], raws[2], raws[7], raws[5],
	}
	res := validate(t, shuffled)
	if res.Status != Pass {
		t.Fatalf("shuffled order: status=%s failures=%v", res.Status, res.Failures)
	}
}

func TestFoldedHeaderParsesAndVerifies(t *testing.T) {
	raws := renderChain(2)
	parsedBefore := mustParse(t, raws)
	raws[0].Value = strings.Replace(raws[0].Value, ";", ";\r\n ", 1)
	parsedAfter := mustParse(t, raws)
	if parsedAfter[0].Value != parsedBefore[0].Value {
		t.Fatalf("fold not unfolded:\n%q\n%q", parsedAfter[0].Value, parsedBefore[0].Value)
	}
	res := validate(t, raws)
	if res.Status != Pass {
		t.Fatalf("folded header: status=%s failures=%v", res.Status, res.Failures)
	}
}

func TestEmptyAndMalformedInput(t *testing.T) {
	res, err := ValidateChain(nil, fakeVerifiers())
	if err != nil || res.Status != NoChain {
		t.Fatalf("nil input: res=%v err=%v", res, err)
	}

	if _, err := ValidateChain(nil, Verifiers{}); err == nil {
		t.Fatal("missing verifiers must return an error")
	}

	bad := []RawHeader{{"ARC-Seal", "a=rsa-sha256; cv=none; b=abcd"}}
	if _, err := ParseHeaders(bad); !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("missing i= tag: err=%v, want ErrMalformedHeader", err)
	}

	bad = []RawHeader{{"ARC-Seal", "i=0; a=rsa-sha256; cv=none"}}
	if _, err := ParseHeaders(bad); !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("zero instance: err=%v, want ErrMalformedHeader", err)
	}

	bad = []RawHeader{{"ARC-Message-Signature", "i=1; garbage-without-eq; b=abcd"}}
	if _, err := ParseHeaders(bad); !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("malformed tag: err=%v, want ErrMalformedHeader", err)
	}
}

func TestNonARCHeadersIgnored(t *testing.T) {
	raws := []RawHeader{
		{"Received", "from mail.example.com"},
		{"From", "alice@example.com"},
	}
	raws = append(raws, renderChain(2)...)
	res := validate(t, raws)
	if res.Status != Pass {
		t.Fatalf("status=%s failures=%v", res.Status, res.Failures)
	}
}

func TestParseEdgeCasesAndErrorWrapping(t *testing.T) {
	// AAR may carry the leading free-form authres token without an '='.
	aar := []RawHeader{{"ARC-Authentication-Results", "i=1; spf=pass smtp.mailfrom=alice@example.com"}}
	parsed, err := ParseHeaders(aar)
	if err != nil || len(parsed) != 1 || parsed[0].Instance != 1 {
		t.Fatalf("AAR with authres token: parsed=%v err=%v", parsed, err)
	}

	// Repeated tag inside one header is malformed.
	if _, err := ParseHeaders([]RawHeader{{"ARC-Seal", "i=1; i=2; cv=none"}}); !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("duplicate tag: err=%v", err)
	}

	// ValidateChain must surface the same parse error, wrapped with the name.
	_, err = ValidateChain([]RawHeader{{"ARC-Message-Signature", "cv=none; b=abcd"}}, fakeVerifiers())
	var pe *ParseError
	if !errors.As(err, &pe) || !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("ValidateChain parse error: err=%v", err)
	}
	if !strings.Contains(err.Error(), "ARC-Message-Signature") {
		t.Fatalf("parse error text missing header name: %v", err)
	}

	// ValidationError wraps sentinels and carries verifier detail through.
	raws := renderChain(2)
	mutate(raws, "ARC-Message-Signature", 2, func(v string) string {
		return setTag(v, "d", "attacker.example")
	})
	res := validate(t, raws)
	var ve *ValidationError
	found := false
	for _, f := range res.Failures {
		if errors.Is(f, ErrMessageSignatureVerification) {
			ve, found = f, true
		}
	}
	if !found || ve.Detail == nil {
		t.Fatalf("failures = %#v", res.Failures)
	}
	if !strings.Contains(ve.Error(), "i=2") {
		t.Fatalf("error text missing instance: %v", ve)
	}

	if AS.String() != "ARC-Seal" || AMS.String() != "ARC-Message-Signature" || AAR.String() != "ARC-Authentication-Results" {
		t.Fatal("unexpected Kind.String values")
	}
}
