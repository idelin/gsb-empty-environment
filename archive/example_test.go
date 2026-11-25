package archive_test

import (
	"context"
	"crypto/sha256"
	"fmt"

	"prescreen.local/ps-920c63cc2f/archive"
)

func ExampleReadBatch() {
	blocks := []archive.Block{{ID: 1, Size: 8, Codec: "raw"}}
	nodes := []archive.Node{archive.NewRawNode(0, 1)}
	strategy := sha256.Sum256([]byte("raw"))
	m, err := archive.NewManifest(1, "raw", archive.Digest(strategy), 0, 8, nodes, blocks)
	if err != nil {
		panic(err)
	}
	grant := archive.NewGrant("tenant-a", archive.Interval{Start: 2, End: 6})

	ctx := context.Background()
	decode := func(ctx context.Context, b archive.Block) ([]byte, error) {
		return []byte("ABCDEFGH"), nil
	}
	batch := []archive.Request{{Start: 2, Length: 4}}
	plan, result, err := archive.ReadBatch(ctx, m, grant, batch, archive.DefaultPlanOptions(1), nil, decode)
	if err != nil {
		panic(err)
	}
	audit, err := archive.CheckPlan(m, grant, batch, plan)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s decodes=%d returned=%d\n", result.Data[0], audit.DecodeBlocks, audit.ReturnBytes)
	// Output: CDEF decodes=1 returned=4
}
