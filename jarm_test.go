package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const (
	testKey      = "super-secret-shared-key"
	testIssuer   = "https://auth.example.com"
	testAudience = "client-123"
	testState    = "state-abc"
)

var fixedNow = time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)

func newTestSigner() *Signer {
	return &Signer{
		Key:      []byte(testKey),
		Issuer:   testIssuer,
		Audience: testAudience,
		now:      func() time.Time { return fixedNow },
	}
}

func newTestVerifier() *Verifier {
	return &Verifier{
		Key:           []byte(testKey),
		Issuer:        testIssuer,
		Audience:      testAudience,
		ExpectedState: testState,
		now:           func() time.Time { return fixedNow },
	}
}

func TestJWTModeIgnoresOuterCodeAndState(t *testing.T) {
	signer := newTestSigner()
	token, err := signer.SignResponse(map[string]string{
		"code":  "inner-jwt-code",
		"state": testState,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	callback, err := url.Parse("https://client.example.com/cb?code=outer-attacker-code&state=outer-state&response=" + token)
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}

	response, err := newTestVerifier().ParseCallback(callback, "jwt")
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}

	if got := response.Code(); got != "inner-jwt-code" {
		t.Fatalf("Code() = %q, want inner JWT code", got)
	}
	if got := response.State(); got != testState {
		t.Fatalf("State() = %q, want %q", got, testState)
	}
	if _, ok := response.Get("response"); ok {
		t.Fatal("outer response token must not be exposed as a result parameter")
	}
	if _, ok := response.Parameters["code"]; !ok {
		t.Fatal("JWT code claim must be present in parameters")
	}
	if response.Issuer != testIssuer || response.Audience != testAudience {
		t.Fatalf("registered claims not recorded: %+v", response)
	}
}

func TestCompositeJWTModes(t *testing.T) {
	signer := newTestSigner()
	token, err := signer.SignResponse(map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	for _, mode := range []string{"jwt", "query.jwt", "form_post.jwt", "fragment.jwt"} {
		callback, err := url.Parse("https://client.example.com/cb?code=outer&response=" + token)
		if err != nil {
			t.Fatalf("parse callback: %v", err)
		}
		response, err := newTestVerifier().ParseCallback(callback, mode)
		if err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if response.Mode != mode || response.Code() != "inner-code" {
			t.Fatalf("mode %s produced %+v", mode, response)
		}
	}
}

func TestQueryModeReadsOuterParameters(t *testing.T) {
	callback, err := url.Parse("https://client.example.com/cb?code=plain-code&state=" + testState)
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}

	verifier := newTestVerifier()
	response, err := verifier.ParseCallback(callback, "query")
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}
	if response.Code() != "plain-code" || response.State() != testState {
		t.Fatalf("unexpected query response: %+v", response)
	}
	if response.Issuer != "" {
		t.Fatalf("query mode must not populate JWT claims: %+v", response)
	}
}

func TestJWTModeMissingResponse(t *testing.T) {
	callback, _ := url.Parse("https://client.example.com/cb?code=outer-code")
	_, err := newTestVerifier().ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrMissingResponse) {
		t.Fatalf("err = %v, want ErrMissingResponse", err)
	}
}

func TestWrongKeyRejected(t *testing.T) {
	token, err := newTestSigner().SignResponse(map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	verifier := newTestVerifier()
	verifier.Key = []byte("a-different-key")
	callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
	_, err = verifier.ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("err = %v, want ErrInvalidSignature", err)
	}
}

func TestIssuerMismatch(t *testing.T) {
	token, err := newTestSigner().SignResponse(map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	verifier := newTestVerifier()
	verifier.Issuer = "https://evil.example.com"
	callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
	_, err = verifier.ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Fatalf("err = %v, want ErrIssuerMismatch", err)
	}
}

func TestAudienceMismatch(t *testing.T) {
	token, err := newTestSigner().SignResponse(map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	verifier := newTestVerifier()
	verifier.Audience = "other-client"
	callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
	_, err = verifier.ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
}

func TestExpiryValidation(t *testing.T) {
	token, err := newTestSigner().SignResponse(map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(-time.Second).UTC())
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
	_, err = newTestVerifier().ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}

	verifier := newTestVerifier()
	verifier.Leeway = 30 * time.Second
	_, err = verifier.ParseCallback(callback, "jwt")
	if err != nil {
		t.Fatalf("token within leeway should verify: %v", err)
	}
}

func TestStateMismatchUsesInnerClaimOnly(t *testing.T) {
	token, err := newTestSigner().SignResponse(map[string]string{
		"code":  "inner-code",
		"state": "inner-state",
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	callback, _ := url.Parse("https://client.example.com/cb?state=" + testState + "&response=" + token)
	_, err = newTestVerifier().ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("err = %v, want ErrStateMismatch based on inner claim", err)
	}
}

func TestNoneAlgorithmRejected(t *testing.T) {
	header := base64URLEncode([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64URLEncode([]byte(`{"iss":"` + testIssuer + `","aud":"` + testAudience + `","exp":` +
		strconv.FormatInt(fixedNow.Add(time.Minute).Unix(), 10) + `,"state":"` + testState + `","code":"inner-code"}`))
	token := header + "." + payload + "."

	callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
	_, err := newTestVerifier().ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestSignCallbackURLRoundTrip(t *testing.T) {
	signer := newTestSigner()
	redirect, _ := url.Parse("https://client.example.com/cb")
	signed, err := signer.SignCallbackURL(redirect, map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignCallbackURL: %v", err)
	}

	response, err := newTestVerifier().ParseCallback(signed, "jwt")
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}
	if response.Code() != "inner-code" || response.State() != testState {
		t.Fatalf("round trip lost parameters: %+v", response)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	if _, err := (&Signer{}).SignResponse(nil, time.Time{}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Signer err = %v, want ErrInvalidConfiguration", err)
	}
	callback, _ := url.Parse("https://client.example.com/cb?response=abc.def.ghi")
	if _, err := (&Verifier{}).ParseCallback(callback, "jwt"); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Verifier err = %v, want ErrInvalidConfiguration", err)
	}
}

func TestEmptyResponseModeRejected(t *testing.T) {
	callback, _ := url.Parse("https://client.example.com/cb")
	if _, err := newTestVerifier().ParseCallback(callback, ""); !errors.Is(err, ErrUnsupportedResponseMode) {
		t.Fatalf("err = %v, want ErrUnsupportedResponseMode", err)
	}
}

func TestParseRequestAndAudienceArray(t *testing.T) {
	signer := newTestSigner()
	token, err := signer.SignResponse(map[string]string{
		"code":  "inner-code",
		"state": testState,
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}

	request, err := http.NewRequest(http.MethodGet, "https://client.example.com/cb?code=outer&response="+token, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	response, err := newTestVerifier().ParseRequest(request, "jwt")
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if response.Code() != "inner-code" {
		t.Fatalf("Code() = %q, want inner-code", response.Code())
	}
}

func TestMalformedTokenRejected(t *testing.T) {
	for _, token := range []string{"not-a-jwt", "a.b", "a.b.c.d", "###.###.###"} {
		callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
		if _, err := newTestVerifier().ParseCallback(callback, "jwt"); err == nil {
			t.Fatalf("token %q unexpectedly accepted", token)
		}
	}
}

func TestMissingExpiryClaim(t *testing.T) {
	header := base64URLEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64URLEncode([]byte(`{"iss":"` + testIssuer + `","aud":"` + testAudience +
		`","state":"` + testState + `","code":"inner-code"}`))
	signingInput := header + "." + payload
	mac := hmacForTest(signingInput)
	token := signingInput + "." + base64URLEncode(mac)

	callback, _ := url.Parse("https://client.example.com/cb?response=" + token)
	_, err := newTestVerifier().ParseCallback(callback, "jwt")
	if !errors.Is(err, ErrMissingClaim) {
		t.Fatalf("err = %v, want ErrMissingClaim", err)
	}
}

func TestDefaultTTLAndSingleStringAudienceHelper(t *testing.T) {
	signer := newTestSigner()
	signer.DefaultTTL = 0
	token, err := signer.SignResponse(map[string]string{"code": "c", "state": testState}, time.Time{})
	if err != nil {
		t.Fatalf("SignResponse: %v", err)
	}
	response, err := newTestVerifier().ParseCallback(mustParse("https://client.example.com/cb?response="+token), "jwt")
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}
	if response.Expiry.IsZero() || !response.Expiry.After(fixedNow) {
		t.Fatalf("default TTL not applied: %+v", response.Expiry)
	}
}

func TestClaimStringAndGetHelpers(t *testing.T) {
	response := &AuthorizationResponse{Parameters: map[string]string{"code": "abc", "empty": ""}}
	if value, ok := response.Get("missing"); ok || value != "" {
		t.Fatalf("Get missing = %q, %v", value, ok)
	}
	if value, ok := response.Get("code"); !ok || value != "abc" {
		t.Fatalf("Get code = %q, %v", value, ok)
	}
}

func mustParse(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

func hmacForTest(signingInput string) []byte {
	mac := hmac.New(sha256.New, []byte(testKey))
	mac.Write([]byte(signingInput))
	return mac.Sum(nil)
}
