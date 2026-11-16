package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q-022"
)

func ExampleHost() {
	host := netsec.NewHost()
	root, err := host.InstallRoot(1, []byte("host-owned root material"))
	if err != nil {
		panic(err)
	}

	context := netsec.Context{
		Tenant:    []byte("tenant-a"),
		Purpose:   netsec.PurposeAuthentication,
		Version:   1,
		Direction: netsec.DirectionSend,
		Epoch:     3,
	}

	handle, err := host.Derive(root, context, 32)
	if err != nil {
		panic(err)
	}
	err = handle.Use(func(key *netsec.Key) error {
		fmt.Println(len(key.HMAC([]byte("message"))))
		return nil
	})
	if err != nil {
		panic(err)
	}

	// Output:
	// 32
}
