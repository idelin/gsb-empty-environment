package netsec_test

import (
	"errors"
	"fmt"

	netsec "example.org/netsec/q0485"
)

func ExampleManager_Authenticate() {
	manager, err := netsec.NewManager(netsec.DefaultConfig())
	if err != nil {
		panic(err)
	}

	issued, err := manager.Authenticate(netsec.LoginRequest{
		UserID:      "alice",
		TenantID:    "tenant-a",
		DeviceID:    "device-1",
		AuthLevel:   1,
		Roles:       []string{"member"},
		Permissions: []string{"read"},
	})
	if err != nil {
		panic(err)
	}

	elevated, err := manager.Elevate(issued.ID, "device-1", netsec.Elevation{
		AuthLevel:   2,
		Roles:       []string{"admin"},
		Permissions: []string{"write"},
	})
	if err != nil {
		panic(err)
	}

	_, err = manager.Validate(issued.ID, "device-1")

	fmt.Println(issued.Authenticated, issued.Generation)
	fmt.Println(elevated.Generation, elevated.AuthLevel)
	fmt.Println(issued.ID == elevated.ID)
	fmt.Println(errors.Is(err, netsec.ErrSessionInvalid))

	// Output:
	// true 1
	// 2 2
	// false
	// true
}
