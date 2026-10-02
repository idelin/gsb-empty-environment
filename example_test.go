package hotring_test

import (
	"fmt"

	"hotring"
)

// Example demonstrates the full hot-standby lifecycle: serve traffic on
// the active ring, inject a scale-in change into the standby ring, warm
// up the migration report, then atomically flip.
func Example() {
	controller := hotring.NewController([]string{
		"cache-a", "cache-b", "cache-c", "cache-d",
	}, 128)

	key := "user:42"
	before, _ := controller.Route(key)
	fmt.Printf("before flip: %s -> %s\n", key, before)

	// Background: node cache-d is being decommissioned (scale-in).
	controller.PrepareStandby([]string{"cache-a", "cache-b", "cache-c"})
	report, _ := controller.Warmup([]string{key, "user:1", "user:2", "user:7"})
	fmt.Printf("warmup: %d/%d sampled keys will migrate\n",
		report.MovedKeys, report.SampledKeys)

	// Atomic pointer flip: all traffic switches in one store.
	if _, err := controller.Flip(); err != nil {
		panic(err)
	}
	after, _ := controller.Route(key)
	fmt.Printf("after flip:  %s -> %s (migrated=%v)\n", key, after, before != after)
}
