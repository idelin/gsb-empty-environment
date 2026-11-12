package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0289"
)

func ExampleMintShards() {
	key := []byte("host-hmac-key")
	parent := netsec.ParentAuth{
		Accessible: netsec.Box{Lo: []int64{0, 0}, Hi: []int64{4, 4}},
		Denied: []netsec.Box{
			{Lo: []int64{1, 1}, Hi: []int64{3, 3}},
		},
	}

	tokens, err := netsec.MintShards(key, parent, 4)
	if err != nil {
		panic(err)
	}

	for _, token := range tokens {
		region := token.Region()

		// A worker retry may ask for the original region again, but asking
		// for anything larger is rejected.
		if err := token.VerifyFor(key, parent, region); err != nil {
			panic(err)
		}
		fmt.Printf("shard %v..%v\n", region.Lo, region.Hi)
	}

	// Output:
	// shard [0 0]..[1 4]
	// shard [3 0]..[4 4]
	// shard [1 0]..[3 1]
	// shard [1 3]..[3 4]
}
