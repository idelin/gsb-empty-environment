package netsec_test

import (
	"fmt"

	"example.org/netsec/q0365"
)

func ExampleValidate() {
	selfReferencing := `(a\1)`
	legalBackreference := `(a)\1`

	fmt.Println(netsec.Validate(selfReferencing) != nil)
	fmt.Println(netsec.Validate(legalBackreference) == nil)
	// Output:
	// true
	// true
}
