package netsec_test

import (
	"context"
	"fmt"

	pushadmit "example.org/netsec/q0137"
)

func ExampleAdmission() {
	source, err := pushadmit.ParseSource("https://a.example")
	if err != nil {
		panic(err)
	}

	admission := pushadmit.NewAdmission(7,
		pushadmit.WithAllocator(func(context.Context, *pushadmit.Push) error {
			return nil
		}),
		pushadmit.WithPushHandler(func(push *pushadmit.Push) {
			fmt.Printf("%s %s\n", push.Source(), push.Request().Path)
		}),
	)
	defer admission.Close("example complete")

	if _, err := admission.RegisterParent(pushadmit.ParentSpec{
		StreamID:             1,
		ConnectionGeneration: 7,
		AuthorizedSources:    []pushadmit.Source{source},
		Context:              context.Background(),
	}); err != nil {
		panic(err)
	}

	if _, err := admission.AdmitPromise(pushadmit.PromiseSpec{
		ParentStreamID:       1,
		PromisedStreamID:     2,
		ConnectionGeneration: 7,
		Headers: []pushadmit.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":authority", Value: "a.example"},
			{Name: ":path", Value: "/asset.js"},
		},
	}); err != nil {
		panic(err)
	}

	// Output:
	// https://a.example /asset.js
}
