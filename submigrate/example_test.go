package submigrate_test

import (
	"crypto/sha256"
	"fmt"

	"prescreen.local/ddd-10567-v3/submigrate"
)

// ExampleMigrate shows the end-to-end importable flow: a host commits two
// partitions, an old reader presents receipts, the service merges the streams
// and a third party verifies the resulting cursor with public keys only.
func ExampleService_Migrate() {
	host := submigrate.NewHost([]byte("example-host"))
	service := submigrate.NewService([]byte("example-service"))

	sumA := sha256.Sum256([]byte("a"))
	sumB := sha256.Sum256([]byte("b"))

	p1 := []submigrate.LogEntry{
		{Visible: host.Visible(submigrate.EventID("evt-a"), sumA)},
		host.Hidden([]byte("a hidden event never reaches subscribers")),
		{Visible: host.Visible(submigrate.EventID("evt-b"), sumB)},
	}
	p2 := []submigrate.LogEntry{
		{Visible: host.Visible(submigrate.EventID("evt-b"), sumB)}, // same stable identity
	}

	root1, vis1 := host.BuildPartition("p1", 1, p1)
	root2, vis2 := host.BuildPartition("p2", 1, p2)
	roots := []*submigrate.PartitionRoot{root1, root2}
	entries := map[submigrate.PartitionID][]submigrate.LogEntry{
		"p1": p1,
		"p2": p2,
	}

	const reader = submigrate.ReaderID("reader-1")
	stream := host.Merge(vis1, vis2)
	receipts := []*submigrate.Receipt{
		host.IssueReceipt(reader, "p1", 1, 2, 1), // 1 confirmed, evt-b is diff
		host.IssueReceipt(reader, "p2", 1, 1, 0), // nothing confirmed yet
	}

	result := service.Migrate(&submigrate.MigrateRequest{
		Reader:   reader,
		Roots:    roots,
		Entries:  entries,
		Receipts: receipts,
		Stream:   stream,
	})

	verifier := submigrate.NewCursorVerifier()
	fmt.Println(result.Status)
	fmt.Println(len(result.Notifications), len(result.Diff))
	fmt.Println(verifier.VerifyCursor(result.Cursor, roots))
	// Output:
	// resumable
	// 2 1
	// true
}
