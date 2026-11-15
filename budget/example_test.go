package budget_test

import (
	"context"
	"errors"
	"fmt"
	"io"

	"example.org/netsec/q-019/budget"
)

func ExampleUnfold() {
	stream := func(name string, payload []byte) *budget.Node {
		data := append([]byte(nil), payload...)
		return &budget.Node{
			Name:         name,
			DeclaredSize: 1, // untrusted metadata; real payload is larger
			Decode:       func(_ context.Context, w io.Writer) error { _, err := w.Write(data); return err },
		}
	}

	tree := &budget.Node{
		Name: "attachment",
		Children: []*budget.Node{
			stream("part-1", []byte("hello")),
			stream("part-2", []byte("world")),
		},
	}

	limits := budget.Limits{
		MaxTotalBytes:  64,
		MaxDepth:       4,
		MaxLeaves:      16,
		MaxLiveBuffers: 2,
	}

	result, err := budget.Unfold(context.Background(), limits, tree)
	if err != nil {
		fmt.Println("unfold failed:", errors.Is(err, budget.ErrTotalBytesExceeded))
		return
	}

	fmt.Println("leaves:", len(result.Leaves))
	fmt.Println("bytes:", result.TotalBytes)
	fmt.Println("first:", string(result.Leaves[0].Data))
	// Output:
	// leaves: 2
	// bytes: 10
	// first: hello
}
