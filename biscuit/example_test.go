package biscuit_test

import (
	"fmt"

	"prescreen.local/ddd-reczz28zozlo8l2e/biscuit"
)

func Example() {
	authority, err := biscuit.ParseBlock(`
user("alice")
right("alice", "write")
check if user("alice")
`)
	if err != nil {
		panic(err)
	}

	// 衰减块试图追加一个 authority 同名的 right 事实给自己提权。
	attenuated, err := biscuit.ParseBlock(`
right("alice", "read")
check if right("alice", "read")
`)
	if err != nil {
		panic(err)
	}

	policies, err := biscuit.ParsePolicies(`
allow if right($user, "read")
deny if true
`)
	if err != nil {
		panic(err)
	}

	authorizer, err := biscuit.New([]*biscuit.Block{authority, attenuated}, policies)
	if err != nil {
		panic(err)
	}
	result := authorizer.Authorize()
	fmt.Println(result.Decision, result.Reason)
	// Output: deny deny_policy
}

func ExampleParsePolicies() {
	authority, _ := biscuit.ParseBlock(`right("alice", "read")`)
	policies, _ := biscuit.ParsePolicies(`
deny if revoked($user)
allow if right($user, "read")
`)
	authorizer, _ := biscuit.New([]*biscuit.Block{authority}, policies)
	fmt.Println(authorizer.Authorize().Decision)
	// Output: allow
}
