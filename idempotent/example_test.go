package idempotent_test

import (
	"fmt"

	"example.org/netsec/q0533/idempotent"
)

func ExampleValidator() {
	validator := idempotent.NewValidator()

	req := idempotent.WriteRequest{
		ProducerID:    1001,
		Partition:     3,
		ProducerEpoch: 0,
		FirstSequence: 0,
		RecordCount:   2,
	}

	result, err := validator.Append(req)
	if err != nil {
		fmt.Println("rejected:", idempotent.RejectReasonOf(err))
		return
	}
	fmt.Printf("appended base=%d last=%d duplicate=%v\n", result.BaseOffset, result.LastOffset, result.Duplicate)

	// Retrying the exact same batch returns the original offsets.
	retry, err := validator.Append(req)
	if err != nil {
		fmt.Println("rejected:", idempotent.RejectReasonOf(err))
		return
	}
	fmt.Printf("retry    base=%d last=%d duplicate=%v\n", retry.BaseOffset, retry.LastOffset, retry.Duplicate)

	// A stale epoch from a zombie producer is fenced.
	zombie := req
	zombie.ProducerEpoch = -1
	_, err = validator.Append(zombie)
	fmt.Println("zombie  :", idempotent.RejectReasonOf(err))

	// Output:
	// appended base=0 last=1 duplicate=false
	// retry    base=0 last=1 duplicate=true
	// zombie  : fenced_epoch
}
