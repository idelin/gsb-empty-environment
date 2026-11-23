package destruct_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"prescreen.local/ps-310314f1df/destruct"
)

func ExampleCoordinator() {
	ctx := context.Background()
	disk := destruct.NewMemDisk()
	store := destruct.NewFaultStore(destruct.NewMemStore(disk))

	pub, priv, _ := ed25519.GenerateKey(nil)
	spec := destruct.ReplicaSpec{
		ID:         "kms-primary",
		Location:   "/var/lib/secret/keys/primary",
		Generation: 1,
		PublicKey:  pub,
	}
	replica := destruct.NewFakeReplica(spec.ID, spec.Location, spec.Generation, priv)

	coord, err := destruct.New(destruct.Config{
		Store: store,
		Erasers: func(_ context.Context, s destruct.ReplicaSpec) (destruct.Eraser, error) {
			return replica, nil
		},
	})
	if err != nil {
		panic(err)
	}

	vol, err := coord.CreateVolume(ctx, "vol-123", "payments", []destruct.ReplicaSpec{spec})
	if err != nil {
		panic(err)
	}

	lease, err := coord.GrantLease(ctx, vol.ID, spec.ID, 0)
	_ = lease
	// A non-positive TTL is rejected before any record is written.
	fmt.Println("zero ttl rejected:", err != nil)

	lease, err = coord.GrantLease(ctx, vol.ID, spec.ID, 60_000_000_000)
	if err != nil {
		panic(err)
	}

	result, err := coord.Destroy(ctx, vol.ID)
	if err != nil {
		panic(err)
	}
	fmt.Println("complete:", result.Complete, "state:", result.State)

	// Sealing is permanent: no new leases, no return to usable.
	_, err = coord.GrantLease(ctx, vol.ID, spec.ID, 60_000_000_000)
	fmt.Println("lease after destroy:", errors.Is(err, destruct.ErrVolumeDestroyed))

	// Repeating destruction is idempotent.
	_, err = coord.Destroy(ctx, vol.ID)
	fmt.Println("repeat destroy:", errors.Is(err, destruct.ErrVolumeDestroyed))

	_ = lease
	// Output:
	// zero ttl rejected: true
	// complete: true state: destroyed
	// lease after destroy: true
	// repeat destroy: true
}
