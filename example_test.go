package netsec_test

import (
	"context"
	"fmt"
	"time"

	netsec "example.org/netsec/q0276"
)

func Example() {
	// Environment-injected credentials outrank task credentials, which in
	// turn outrank instance-metadata credentials.
	graph, err := netsec.NewTrustGraph(
		[]netsec.SourceID{"instance", "task", "env"},
		[]netsec.TrustEdge{
			{Higher: "env", Lower: "task"},
			{Higher: "task", Lower: "instance"},
		},
	)
	if err != nil {
		panic(err)
	}

	instance := netsec.NewMutableSource("instance")
	task := netsec.NewMutableSource("task")
	env := netsec.NewMutableSource("env")
	instance.Set(netsec.Credential{
		Identity: "instance-session", Tenant: "tenant-a", Audience: "api/ingest",
	})
	task.Set(netsec.Credential{
		Identity: "task-session", Tenant: "tenant-a", Audience: "api/ingest",
	})
	env.Set(netsec.Credential{
		Identity: "env-session", Tenant: "tenant-a", Audience: "api/ingest",
	})

	selector, err := netsec.NewSelector(graph, []netsec.Source{instance, task, env})
	if err != nil {
		panic(err)
	}

	request := netsec.Request{
		Tenant:   "tenant-a",
		Audience: "api/ingest",
		Now:      time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
	}
	selection, err := selector.Refresh(context.Background(), request)
	if err != nil {
		panic(err)
	}
	fmt.Println("selected:", selection.Credential.Source, selection.Credential.Identity)

	// An incomparable source cannot silently win; the caller gets a conflict.
	peer := netsec.NewMutableSource("peer")
	peer.Set(netsec.Credential{
		Identity: "peer-session", Tenant: "tenant-a", Audience: "api/ingest",
	})
	if _, err := netsec.NewSelector(graph, []netsec.Source{peer}); err != nil {
		fmt.Println("rejected unknown source:", err)
	}

	// Output:
	// selected: env env-session
	// rejected unknown source: netsec: unknown credential source: "peer"
}
