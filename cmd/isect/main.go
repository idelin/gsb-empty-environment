// Command isect intersects several sorted ID lists stored as
// independent blocks, emits a checkable proof, and verifies it.
//
// Usage: isect [-block N] file1 file2 [file3 ...]
//
// Each input file contains one decimal ID per line (duplicates and
// unordered entries are normalized away). The intersection, the
// per-block evidence (with skip justifications), and the verification
// outcome are printed.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"isect/intersect"
)

func readIDs(path string) ([]uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ids []uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: bad ID %q: %v", path, line, err)
		}
		ids = append(ids, id)
	}
	return ids, sc.Err()
}

func main() {
	blockSize := flag.Int("block", 4, "IDs per block")
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: isect [-block N] file1 file2 [file3 ...]")
		os.Exit(2)
	}

	var lists [][]intersect.Block
	for _, path := range flag.Args() {
		ids, err := readIDs(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		norm := intersect.Normalize(ids)
		var blocks []intersect.Block
		for i := 0; i < len(norm); i += *blockSize {
			end := i + *blockSize
			if end > len(norm) {
				end = len(norm)
			}
			blocks = append(blocks, intersect.NewBlock(norm[i:end]))
		}
		if len(blocks) == 0 {
			blocks = append(blocks, intersect.NewBlock(nil))
		}
		lists = append(lists, blocks)
		fmt.Printf("list %d: %s -> %d unique IDs in %d block(s)\n", len(lists)-1, path, len(norm), len(blocks))
	}

	result, proof, err := intersect.Intersect(lists)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println("\nevidence:")
	for _, ev := range proof.Evidence {
		s := ev.Summary
		fmt.Printf("  list %d block %d: [%d..%d] n=%d hash=%x... -> %s (%s)\n",
			ev.ListIndex, ev.BlockIndex, s.Min, s.Max, s.Count, s.Hash[:4], ev.Disposition, ev.Reason)
	}

	fmt.Printf("\nintersection (%d IDs): %v\n", len(result), result)

	if err := intersect.Verify(lists, proof); err != nil {
		fmt.Println("verification: FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("verification: OK (proof checked against block contents)")
}
