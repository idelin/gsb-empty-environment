package revoke_test

import (
	"fmt"

	"example.org/domainreissue/q017/revoke"
)

// Example shows a messaging host binding frames to an authentication
// generation and observing that logout revokes every frame that had not
// started its sensitive callback.
func Example() {
	dispatcher := revoke.New(revoke.NewGoroutineExecutor())

	gen, err := dispatcher.Login()
	if err != nil {
		panic(err)
	}

	receipt, _ := dispatcher.Submit(gen, "frame-1", func(revoke.AuthContext) {
		fmt.Println("sensitive callback ran")
	})

	decision := receipt.Decision()
	fmt.Println("authorized:", decision.Authorized, "reason:", decision.Reason)

	confirmation, err := dispatcher.Logout()
	if err != nil {
		panic(err)
	}
	fmt.Println("logged out generation:", confirmation.GenID,
		"at barrier point:", confirmation.BarrierPoint)

	// Output:
	// sensitive callback ran
	// authorized: true reason: authorized
	// logged out generation: 1 at barrier point: 2
}
