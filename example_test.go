package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0619"
)

func ExampleCheck() {
	processes := []netsec.Process{
		{PID: 100, SigningID: "com.example.photoapp", TeamID: "TEAMAAAAAA"},
		{
			PID:             200,
			ParentPID:       100,
			SigningID:       "com.example.photoapp.helper",
			TeamID:          "TEAMAAAAAA",
			AppBundleHelper: true,
		},
	}
	table := []netsec.Authorization{
		{
			Service:  "kTCCServicePhotos",
			ClientID: "com.example.photoapp",
			TeamID:   "TEAMAAAAAA",
			Allowed:  true,
		},
	}

	decision := netsec.Check(processes, table, 200, "kTCCServicePhotos")
	fmt.Println(decision.Allowed, decision.ResponsiblePID)
	// Output: true 100
}
