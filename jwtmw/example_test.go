package jwtmw_test

import (
	"fmt"
	"net/http"
	"time"

	"example.org/offlinecli/jwtmw"
)

// ExampleMiddleware shows how to mount the verifier on a net/http gateway.
// It uses an in-process handler instead of a live JWKS URL only so the
// example stays self-contained.
func Example_gatewayUsage() {
	// In production this is the identity provider's JWKS HTTPS endpoint.
	jwksURI := "https://issuer.example/.well-known/jwks.json"

	verifier, err := jwtmw.New(jwtmw.Config{
		JWKSURI:            jwksURI,
		CacheTTL:           10 * time.Minute,
		GracePeriod:        time.Minute,
		MinRefreshInterval: 30 * time.Second,
		Issuer:             "https://issuer.example",
		Audience:           "api-gateway",
		Leeway:             5 * time.Second,
	})
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/protected", verifier.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := jwtmw.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "missing claims", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "hello %s", claims.Subject)
	})))

	_ = http.Server{
		Addr:    ":8080",
		Handler: mux,
	}
	fmt.Println("middleware mounted")
	// Output: middleware mounted
}
