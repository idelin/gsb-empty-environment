package digestauth_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"example.org/netsec/q0131/digestauth"
)

// parseDigestHeader extracts the quoted/raw digest parameters from an
// Authorization header value. A real gateway would use a full parser.
func parseDigestHeader(header string) map[string]string {
	out := map[string]string{}
	rest := strings.TrimPrefix(header, "Digest ")
	for _, part := range strings.Split(rest, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[key] = strings.Trim(value, "\"")
	}
	return out
}

// This example shows how a gateway wires the state package into an HTTP
// handler: parse nc, mint nonces on 401, and authenticate every request.
func ExampleState_gateway() {
	state := digestauth.NewState([]byte("persistent-server-hmac-key"))
	defer state.Stop()

	policy := digestauth.Policy{
		Realm:     "gateway.example.org",
		QOP:       "auth",
		Algorithm: "MD5",
		MaxCount:  1 << 20,
		Window:    64,
	}

	verifier := digestauth.NewRFC2617Verifier(
		func(username, realm string) (string, error) {
			// Return HA1 = H(username:realm:password) from the credential store.
			return "", fmt.Errorf("unknown user %s", username)
		},
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Digest ") {
			nonce, err := state.Issue(policy)
			if err != nil {
				http.Error(w, "nonce failure", http.StatusInternalServerError)
				return
			}
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Digest realm=%q, qop="auth", nonce=%q, algorithm=%s`,
					policy.Realm, nonce, policy.Algorithm))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		params := parseDigestHeader(header)
		nc, err := digestauth.ParseNC(params["nc"])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = state.Authenticate(r.Context(), verifier, digestauth.Request{
			Policy:   policy,
			Username: params["username"],
			Nonce:    params["nonce"],
			CNonce:   params["cnonce"],
			NC:       nc,
			Method:   r.Method,
			URI:      r.URL.RequestURI(),
			Response: params["response"],
		})
		switch {
		case err == nil:
			fmt.Fprintln(w, "ok")
		case errors.Is(err, digestauth.ErrCounterWrapped):
			http.Error(w, "stale: request a new nonce", http.StatusUnauthorized)
		default:
			http.Error(w, err.Error(), http.StatusUnauthorized)
		}
	})

	_ = handler
	log.Print("digest handler ready")
	// Output:
}

// A custom verifier can plug in any signature scheme while still reusing the
// atomic nonce/counter machinery.
func ExampleVerifierFunc() {
	custom := digestauth.VerifierFunc(func(_ context.Context, e digestauth.Evidence) error {
		if e.Username == "" || e.Realm == "" || e.URI == "" {
			return fmt.Errorf("incomplete evidence")
		}
		return nil
	})
	_ = custom
}
