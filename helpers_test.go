package netsec

import (
	"net/http"
	"net/url"
	"testing"
)

const testRedirectURI = "https://sp.example.com/oauth/callback"

type loginSetup struct {
	tx       *Transaction
	verifier string
}

// beginLogin runs the relying-party side of starting a login at one provider.
func beginLogin(t *testing.T, mgr *Manager, fp *FakeProvider, sessionID string) loginSetup {
	t.Helper()

	verifier, err := NewCodeVerifier()
	if err != nil {
		t.Fatalf("code verifier: %v", err)
	}
	tx, err := mgr.Begin(BeginParams{
		BrowserSessionID:    sessionID,
		Issuer:              fp.Issuer(),
		CodeChallenge:       S256Challenge(verifier),
		CodeChallengeMethod: "S256",
		RedirectURI:         testRedirectURI,
		Exchanger:           fp.NewExchanger(),
		Validator:           fp.NewValidator(),
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	return loginSetup{tx: tx, verifier: verifier}
}

// followAuthorize performs the provider redirect and parses the callback
// parameters the browser would arrive with.
func followAuthorize(t *testing.T, fp *FakeProvider, tx *Transaction) (code, state, iss string) {
	t.Helper()

	client := fp.HTTPClient()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Get(fp.AuthorizeURL(testRedirectURI, tx))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", resp.StatusCode)
	}

	parsed, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	q := parsed.Query()
	return q.Get("code"), q.Get("state"), q.Get("iss")
}

func callbackFor(setup loginSetup, code, state, iss, sessionID string) Callback {
	return Callback{
		State:            state,
		Code:             code,
		Issuer:           iss,
		BrowserSessionID: sessionID,
		CodeVerifier:     setup.verifier,
		RedirectURI:      testRedirectURI,
	}
}
