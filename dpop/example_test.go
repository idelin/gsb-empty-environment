package dpop_test

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

// This example shows how a gateway host wires the DPoP middleware in front of
// a protected handler, with a rotating nonce manager and a pinned base URL.
func ExampleVerifier_middleware() {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}

	nonceManager, err := dpop.NewNonceManager(secret, time.Hour)
	if err != nil {
		panic(err)
	}

	htuFunc, err := dpop.BaseURLHTU("https://api.example.com")
	if err != nil {
		panic(err)
	}

	verifier, err := dpop.NewVerifier(dpop.Options{
		NonceManager: nonceManager,
		HTUFunc:      htuFunc,
		ClockSkew:    time.Minute,
		ProofTTL:     5 * time.Minute,
		ReplayWindow: 10 * time.Minute,
		// Wrap ParseJWTTokenClaims with real JWT signature/introspection
		// validation in production:
		// Inspector: myTokenValidator,
	})
	if err != nil {
		panic(err)
	}

	protected := verifier.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := dpop.FromContext(r.Context())
		fmt.Fprintf(w, "verified key thumbprint %s", claims.JKT)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/orders", nil)
	protected.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	fmt.Println(rec.Header().Get("WWW-Authenticate"))
	// Output:
	// 401
	// DPoP realm="dpop", error="invalid_token", error_description="missing Authorization header"
}
