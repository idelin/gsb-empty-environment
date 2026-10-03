package invindex_test

import (
	"fmt"

	"invindex"
)

func ExampleSegment_And() {
	b := invindex.NewBuilder()
	docs := []string{
		"database storage engine",
		"database theory and relational algebra",
		"network attached storage",
		"database and storage systems",
	}
	for i, d := range docs {
		b.AddDocument(uint64(i), d)
	}
	seg := b.Build()
	for _, m := range seg.And("database", "storage") {
		fmt.Printf("doc=%d freqs=%v\n", m.DocID, m.Freqs)
	}
	// Output:
	// doc=0 freqs=[1 1]
	// doc=3 freqs=[1 1]
}
