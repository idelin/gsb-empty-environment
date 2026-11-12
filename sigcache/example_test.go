package sigcache_test

import (
	"fmt"
	"time"

	"example.org/domainreissue/q016/sigcache"
)

func ExampleVerifier() {
	keyring := sigcache.NewKeyring()
	keyring.Add("key-1", []byte("a-long-random-shared-secret"))

	// The host trusts its wall clock within 250 ms.
	verifier, err := sigcache.New(sigcache.Config{
		Keyring: keyring,
		Clock:   sigcache.SystemClock{Uncertainty: 250 * time.Millisecond},
	})
	if err != nil {
		panic(err)
	}

	expiry := time.Now().Add(10 * time.Minute)
	token, err := sigcache.Sign(
		"key-1",
		[]byte("a-long-random-shared-secret"),
		"download.example.org",
		"sha256:abcdef",
		expiry,
	)
	if err != nil {
		panic(err)
	}

	// First delivery computes the HMAC; later deliveries reuse the cached
	// verification but always re-check the trusted time interval.
	req := sigcache.Request{
		Token:        token,
		Audience:     "download.example.org",
		ObjectDigest: "sha256:abcdef",
		AgeHeader:    "42", // untrusted, diagnostics only
	}
	receipt, err := verifier.Authorize(req)
	if err != nil {
		panic(err)
	}
	receipt2, err := verifier.Authorize(req)
	if err != nil {
		panic(err)
	}

	fmt.Println(receipt.CacheHit, receipt2.CacheHit)
	// Output: false true
}
