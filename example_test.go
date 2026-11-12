package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	netsec "example.org/netsec/q0137"
)

// ExampleController shows how a proxy host wires the admission controller into
// its HTTP/2 frame loop: bind each authenticated client request, then admit
// each PUSH_PROMISE before any resource is exposed to the business layer.
func ExampleController() {
	controller := netsec.NewController(func(resource netsec.PushResource) {
		// This is the only place business logic ever observes pushed data,
		// and every resource here is authorized by its own parent context.
		fmt.Printf("push %d -> %s%s\n",
			resource.PromisedStreamID,
			resource.Request.Origin,
			resource.Request.Path)
	})

	origin, err := netsec.ParseOrigin("https://app.example")
	if err != nil {
		log.Fatal(err)
	}

	// Client request stream 1 authenticated for app.example.
	if err := controller.BindParent(netsec.ParentRequest{
		StreamID:   1,
		Authorized: []netsec.Origin{origin},
	}); err != nil {
		log.Fatal(err)
	}

	// A PUSH_PROMISE arriving on stream 2 referencing parent stream 1.
	admission, err := controller.AdmitPromise(netsec.PushPromise{
		ParentStreamID:   1,
		PromisedStreamID: 2,
		Headers: []netsec.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":authority", Value: "app.example"},
			{Name: ":path", Value: "/app.js"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// A foreign-origin promise is rejected before it can reserve anything.
	_, err = controller.AdmitPromise(netsec.PushPromise{
		ParentStreamID:   1,
		PromisedStreamID: 4,
		Headers: []netsec.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":authority", Value: "evil.example"},
			{Name: ":path", Value: "/steal"},
		},
	})
	if !errors.Is(err, netsec.ErrOriginNotAuthorized) {
		log.Fatalf("expected rejection, got %v", err)
	}

	if err := admission.Deliver(context.Background()); err != nil {
		log.Fatal(err)
	}

	// Physical reconnect: old stream IDs can no longer be replayed.
	controller.AdvanceGeneration()

	// Output:
	// push 2 -> https://app.example:443/app.js
}
