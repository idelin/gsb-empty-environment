// Command logrecover opens a logstore directory, repairs it, and prints
// the repair range.
//
//	usage: logrecover <dir>
package main

import (
	"fmt"
	"os"

	"logstore"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: logrecover <dir>")
		os.Exit(2)
	}
	store, rep, err := logstore.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "logrecover:", err)
		os.Exit(1)
	}
	defer store.Close()

	fmt.Printf("data:  %d -> %d bytes", rep.DataSizeBefore, rep.DataSizeAfter)
	if rep.DataTruncated {
		fmt.Printf(" (torn tail truncated at %d)", rep.DataSizeAfter)
	}
	fmt.Println()

	fmt.Printf("index: %d bytes on disk, %d well-formed entries, %d kept after data verification",
		rep.IndexSizeBefore, rep.IndexEntriesBefore, rep.IndexEntriesKept)
	if rep.IndexTruncatedBytes > 0 {
		fmt.Printf(" (truncated %d tail bytes)", rep.IndexTruncatedBytes)
	}
	if rep.IndexRewritten {
		fmt.Printf(" (rebuilt from scratch)")
	}
	fmt.Println()

	if rep.RebuiltEntries > 0 || rep.DataTruncated || rep.IndexTruncatedBytes > 0 {
		fmt.Printf("repair range: data offsets [%d, %d), %d index entries rebuilt\n",
			rep.RebuildStart, rep.RebuildEnd, rep.RebuiltEntries)
	}

	for _, c := range rep.Corrupt {
		if c.Length > 0 {
			fmt.Printf("CORRUPT record at offset %d, length %d: %s\n", c.Offset, c.Length, c.Reason)
		} else {
			fmt.Printf("CORRUPT record at offset %d: %s\n", c.Offset, c.Reason)
		}
	}

	if rep.Clean() {
		fmt.Println("clean: no repair needed")
	}
	fmt.Printf("records: %d\n", store.Len())
}
