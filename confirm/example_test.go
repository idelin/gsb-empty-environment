package confirm_test

import (
	"errors"
	"fmt"
	"time"

	"example.org/netsec/q0063/confirm"
)

func ExampleVerifier() {
	secret := []byte("shared-token-secret-32-bytes-ok")
	store := confirm.NewMemoryStore()
	store.Provision("alice", secret)
	v := confirm.NewVerifier(store)

	tx := confirm.Transaction{
		Amount:   "8888.00",
		Currency: "CNY",
		Payee:    "6222021234567890123",
		Nonce:    "0123456789abcdef0123456789abcdef",
	}
	at := time.Unix(1_700_000_000, 0)

	// The token computes its answer over the server-issued challenge.
	challenge, _ := confirm.Challenge(tx)
	code, _ := v.GenerateResponse(secret, 0, challenge, at)

	err := v.Verify(confirm.Request{
		UserID: "alice", Tx: tx, Counter: 0, Response: code, Now: at,
	})
	fmt.Println("first verification:", err == nil)

	err = v.Verify(confirm.Request{
		UserID: "alice", Tx: tx, Counter: 1, Response: code, Now: at,
	})
	fmt.Println("replay rejected:", errors.Is(err, confirm.ErrChallengeReplay))

	// Output:
	// first verification: true
	// replay rejected: true
}
