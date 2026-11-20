package netsec_test

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	netsec "example.org/netsec/q0511"
)

// ExampleVerify shows the gateway-side flow: parse a lock file, resolve the
// actual .nupkg hashes and verify every entry.
func ExampleVerify() {
	lockJSON := `{
	  "version": 2,
	  "dependencies": {
	    "net8.0": {
	      "App.Core": {
	        "type": "Direct",
	        "requested": "[1.0.0, 2.0.0)",
	        "resolved": "1.2.0",
	        "contentHash": "PLACEHOLDER",
	        "dependencies": {"Logging": "0.9.0"}
	      },
	      "Logging": {
	        "type": "Transitive",
	        "requested": "[0.9.0, )",
	        "resolved": "0.9.0",
	        "contentHash": "PLACEHOLDER2"
	      }
	    }
	  }
	}`

	// In a real gateway these hashes come from the downloaded .nupkg files,
	// e.g. via netsec.NupkgHashFunc over the local package cache.
	hashOf := func(payload string) string {
		sum := sha512.Sum512([]byte(payload))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	actual := map[string]string{
		"app.core@1.2.0": hashOf("app.nupkg"),
		"logging@0.9.0":  hashOf("logging.nupkg"),
	}
	lockJSON = strings.Replace(lockJSON, "PLACEHOLDER2", actual["logging@0.9.0"], 1)
	lockJSON = strings.Replace(lockJSON, "PLACEHOLDER", actual["app.core@1.2.0"], 1)

	lf, err := netsec.ParseLockFile(strings.NewReader(lockJSON))
	if err != nil {
		panic(err)
	}

	err = netsec.Verify(lf, netsec.Options{
		ActualHash: func(id, version string) (string, bool) {
			h, ok := actual[strings.ToLower(id)+"@"+version]
			return h, ok
		},
		RootDependencies: map[string][]string{"net8.0": {"App.Core"}},
	})
	fmt.Println(err)
	// Output: <nil>
}

func ExampleVerifyErrors() {
	lf := &netsec.LockFile{
		Version: 2,
		Dependencies: map[string]map[string]netsec.LockEntry{
			"net8.0": {
				"App.Core": {
					Type:        netsec.TypeDirect,
					Requested:   "[1.0.0,2.0.0)",
					Resolved:    "2.0.0",
					ContentHash: "AAAA",
				},
			},
		},
	}
	err := netsec.Verify(lf, netsec.Options{
		ActualHash: func(string, string) (string, bool) { return "AAAA", true },
	})
	var verrs netsec.VerifyErrors
	if as, ok := err.(netsec.VerifyErrors); ok {
		verrs = as
	}
	fmt.Println(verrs.HasCode(netsec.ErrVersionOutOfRange))
	// Output: true
}
