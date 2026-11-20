package keyepoch_test

import (
	"fmt"

	"prescreen.local/ddd-10524-v2/keyepoch"
)

// fixedAuth accepts every well-tagged frame, demonstrating a host-supplied
// authenticator instead of the scripted default adapter.
type fixedAuth struct{ secret []byte }

func (a fixedAuth) Authenticate(f keyepoch.Frame) keyepoch.AuthResult {
	if keyepoch.VerifyFrame(f, a.secret) {
		return keyepoch.AuthAccepted
	}
	return keyepoch.AuthRejected
}

// Example shows the importable lifecycle: request an update, survive an
// ambiguous write by message identity, then verify the history via replay.
func Example() {
	cfg := keyepoch.Config{
		Role:        "host",
		ReadSecret:  []byte("host read secret"),
		WriteSecret: []byte("host write secret"),
		Budget:      3,
		WindowSize:  16,
		Auth:        fixedAuth{secret: []byte("host read secret")},
	}
	engine, err := keyepoch.NewEngine(cfg)
	if err != nil {
		panic(err)
	}

	if _, err := engine.RequestUpdate(); err != nil {
		panic(err)
	}
	updateID := "host-m1"

	// First write answer is ambiguous: the queue must freeze without a second
	// KeyUpdate being produced.
	if _, err := engine.SetWrite(updateID, keyepoch.WriteUnknown); err != nil {
		panic(err)
	}
	if _, err := engine.Pump(); err != nil {
		panic(err)
	}
	fmt.Printf("after ambiguous write: epoch=%d attempts=%d\n",
		engine.WriteEpoch(), len(engine.SendAttempts()))

	// Identity lookup proves the single original frame did commit.
	if _, err := engine.SetLookup(updateID, keyepoch.WriteCommitted); err != nil {
		panic(err)
	}
	if _, err := engine.ResolveWrite(updateID); err != nil {
		panic(err)
	}
	fmt.Printf("after identity resolve: epoch=%d attempts=%d status=%s\n",
		engine.WriteEpoch(), len(engine.SendAttempts()), engine.Status())

	// An independent replay must reproduce every canonical summary.
	_, replayed, err := keyepoch.Replay(cfg, engine.Events())
	if err != nil {
		panic(err)
	}
	original := engine.Summaries()
	match := true
	for i := range original {
		if original[i].Canonical() != replayed[i].Canonical() {
			match = false
		}
	}
	fmt.Printf("replay identical=%t\n", match)
	// Output:
	// after ambiguous write: epoch=0 attempts=1
	// after identity resolve: epoch=1 attempts=1 status=running
	// replay identical=true
}
