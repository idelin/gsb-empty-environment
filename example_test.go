package netsec_test

import (
	"fmt"

	"example.org/netsec/q0369"
)

func ExampleRouter_Route() {
	router, err := netsec.NewRouter(netsec.Config{
		PrivilegedLabels:       []string{"privileged"},
		PrivilegedCapabilities: []netsec.Capability{"secrets.read"},
		Repositories: []netsec.Repository{{
			ID:                "acme/app",
			Trust:             netsec.TrustPrivileged,
			ProtectedBranches: []string{"main"},
		}},
	})
	if err != nil {
		panic(err)
	}
	if err := router.RegisterExecutor(netsec.Executor{
		ID:           "prod-a",
		Domain:       netsec.DomainProduction,
		Labels:       []string{"linux", "privileged"},
		Capabilities: []netsec.Capability{"build", "secrets.read"},
	}); err != nil {
		panic(err)
	}

	placement, err := router.Route(netsec.Job{
		ID:                   "run-1",
		Repository:           "acme/app",
		Source:               netsec.SourceRepository,
		Ref:                  "refs/heads/main",
		Labels:               []string{"linux", "privileged"},
		RequiredCapabilities: []netsec.Capability{"build", "secrets.read"},
		Queue:                "deploy",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(placement.ExecutorID, placement.Domain, placement.Queue)
	// Output: prod-a production queue/production/deploy
}
