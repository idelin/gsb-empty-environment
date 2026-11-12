package rar_test

import (
	"fmt"

	"example.org/netsec/q0346/rar"
)

func ExampleSatisfy_prefixSpoofingRejected() {
	granted := []rar.Detail{{
		Type:      "account_access",
		Locations: []string{"https://api.example.com/accounts/1"},
		Actions:   []string{"read"},
	}}

	// The attacker appends a path segment. Prefix matching would accept it;
	// byte-exact matching rejects it.
	requested := []rar.Detail{{
		Type:      "account_access",
		Locations: []string{"https://api.example.com/accounts/1/../1/admin"},
		Actions:   []string{"read"},
	}}

	err := rar.Satisfy(requested, granted)
	violation := rar.AsViolation(err)
	fmt.Println(violation.Field)
	// Output: locations
}

func ExampleRefreshAllowed() {
	original := []rar.Detail{rar.NewDetailWithIdentifier(
		"payment_initiation",
		[]string{"https://api.example.com/payments"},
		[]string{"read"},
		nil,
		"acct-1",
	)}

	// The refresh tries to broaden the granted actions.
	refreshed := []rar.Detail{rar.NewDetailWithIdentifier(
		"payment_initiation",
		[]string{"https://api.example.com/payments"},
		[]string{"read", "create"},
		nil,
		"acct-1",
	)}

	violation := rar.AsViolation(rar.RefreshAllowed(refreshed, original))
	fmt.Println(violation.Field)
	// Output: actions
}

func ExampleParseDetails() {
	details, err := rar.ParseDetails([]byte(`[
		{"type":"account_access","actions":["read"]},
		{"type":"payment_initiation","identifier":"acct-1"}
	]`))
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	for _, detail := range details {
		identifier, ok := detail.IdentifierValue()
		fmt.Printf("%s identifier-present=%v identifier=%q\n", detail.Type, ok, identifier)
	}
	// Output:
	// account_access identifier-present=false identifier=""
	// payment_initiation identifier-present=true identifier="acct-1"
}

func ExampleSatisfy_unknownType() {
	err := rar.Satisfy(
		[]rar.Detail{{Type: "fund_transfer"}},
		[]rar.Detail{rar.NewDetail("account_access", nil, []string{"read"}, nil)},
	)
	fmt.Println(rar.AsViolation(err).Field)
	// Output: type
}
