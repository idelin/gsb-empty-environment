package ionfilter_test

import (
	"fmt"

	"example.org/netsec/q0425/ionfilter"
)

// This is the shape an ingestion reader uses: a service controlled trusted
// prefix carrying the local symbol table, followed by uploaded user bytes.
func Example() {
	trustedHeader := []byte(`$ion_symbol_table::{symbols:["audit"]}`)
	uploadedRecord := []byte(`audit::{user_id: 42, role: "admin"}`)

	filter := ionfilter.New(
		[]string{"audit"},
		ionfilter.TrustedSegment(trustedHeader),
		ionfilter.UserSegment(uploadedRecord),
	)
	if err := filter.Validate(); err != nil {
		fmt.Println("rejected")
		return
	}
	fmt.Println("accepted")
	// Output: accepted
}

func ExampleValidate_rejectDirectiveInUserData() {
	uploaded := []byte(`$ion_symbol_table::{symbols:["evil_role"]} evil_role::1`)
	err := ionfilter.Validate(uploaded, false, []string{"audit"})

	var violation *ionfilter.Violation
	fmt.Println(err != nil, asViolation(err, &violation) == ionfilter.KindSymbolTable)
	// Output: true true
}

func asViolation(err error, target **ionfilter.Violation) ionfilter.Kind {
	if err == nil {
		return ""
	}
	if v, ok := err.(*ionfilter.Violation); ok {
		*target = v
		return v.Kind
	}
	return ""
}
