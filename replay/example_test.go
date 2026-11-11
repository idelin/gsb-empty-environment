package replay_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"example.org/netsec/uid1262/replay"
)

func ExampleCache_Accept() {
	ctx := context.Background()
	storage := replay.NewMemoryStorage()
	cache, err := replay.New(replay.Config{
		Storage:         storage,
		PastWindow:      5 * time.Minute,
		FutureTolerance: 5 * time.Minute,
	})
	if err != nil {
		panic(err)
	}
	defer cache.Close()

	digest := sha256.Sum256([]byte("der-encoded-service-ticket"))
	key := replay.Key{
		ClientPrincipal:  "alice@REALM",
		ServicePrincipal: "host/server@REALM",
		TicketDigest:     digest[:],
		ClientTime:       time.Now(),
		Sequence:         42,
	}

	// Call Accept only after cryptographic validation of the ticket succeeds.
	if err := cache.Accept(ctx, key); err != nil {
		panic(err)
	}
	err = cache.Accept(ctx, key)
	fmt.Println(errors.Is(err, replay.ErrReplay))
	// Output: true
}
