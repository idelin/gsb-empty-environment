package netsec_test

import (
	"errors"
	"fmt"

	"example.org/netsec/q0093"
)

func ExampleValidateRedirectURI() {
	stored := "https://app.example/callback?state=abc&scope=openid"

	fmt.Println(netsec.ValidateRedirectURI(stored, stored) == nil)

	err := netsec.ValidateRedirectURI(stored, "https://app.example/callback?scope=openid&state=abc")
	fmt.Println(errors.Is(err, netsec.ErrRedirectURIMismatch))

	err = netsec.ValidateRedirectURI(stored, "https://app.example@attacker.example/callback?state=abc&scope=openid")
	fmt.Println(errors.Is(err, netsec.ErrRedirectURIUserInfo))

	// Output:
	// true
	// true
	// true
}
