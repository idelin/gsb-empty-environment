package framepolicy_test

import (
	"fmt"
	"net/http"
	"net/url"

	"example.org/netsec/q0556/framepolicy"
)

// ExampleDecide shows how a response egress evaluates a nested embedding
// request against the response's framing headers.
func ExampleDecide() {
	respHeader := http.Header{}
	respHeader.Add("Content-Security-Policy", "frame-ancestors https://*.shop.example")
	respHeader.Add("X-Frame-Options", "DENY") // ignored: frame-ancestors wins

	self, _ := url.Parse("https://api.example.internal/v1/pay")
	parent, _ := url.Parse("https://checkout.shop.example/order")
	top, _ := url.Parse("https://portal.shop.example/home")

	decision := framepolicy.Decide(respHeader, &framepolicy.Options{
		SelfURL:   self,
		Ancestors: []*url.URL{top, parent},
	})
	fmt.Println(decision == framepolicy.Allow)
	// Output: true
}
