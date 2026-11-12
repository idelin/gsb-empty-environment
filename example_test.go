package netsec_test

import (
	"fmt"

	"example.org/netsec/q0508"
)

func ExampleDispatcher() {
	d := netsec.NewDispatcher(
		netsec.WithSettings(true, true),
		netsec.WithHandler(func(streamID uint64, payload []byte) {
			fmt.Printf("stream %d: %s\n", streamID, payload)
		}),
	)

	// The CONNECT request on stream 4 prepared its datagram channel and
	// installed the handler above.
	d.OpenConnectStream(4)
	if err := d.HandleQuarterStreamID(1, []byte("hello")); err != nil {
		fmt.Println(err)
	}

	// Output:
	// stream 4: hello
}
