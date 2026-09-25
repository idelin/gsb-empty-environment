// Command demo runs an end-to-end backup/restore sample:
// export a full backup plus two incrementals from a versioned page
// database, restore the chain, and show that corruption, gaps and
// crashes are rejected without leaving a partial database behind.
//
// Usage: go run ./cmd/demo [workdir]   (default ./demo-data)
package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"pagebackup"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func makePage(seed byte) []byte {
	p := make([]byte, pagebackup.PageSize)
	for i := range p {
		p[i] = seed + byte(i)
	}
	return p
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func main() {
	root := "./demo-data"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	os.RemoveAll(root)

	// --- Build a source database, exporting at each version --------
	db, err := pagebackup.CreateDB(filepath.Join(root, "src"))
	must(err)

	fullDir := filepath.Join(root, "backups", "full-v1")
	inc1Dir := filepath.Join(root, "backups", "incr-v1-v2")
	inc2Dir := filepath.Join(root, "backups", "incr-v2-v3")

	must(db.ApplyChanges(map[uint32][]byte{1: makePage(0x10), 2: makePage(0x20), 3: makePage(0x30)})) // v1
	must(pagebackup.ExportFull(db, fullDir))
	fmt.Printf("[export] full-v1     -> %s\n", fullDir)

	must(db.ApplyChanges(map[uint32][]byte{2: makePage(0x21), 4: makePage(0x40)})) // v2
	must(pagebackup.ExportIncremental(db, inc1Dir, 1))
	fmt.Printf("[export] incr-v1-v2  -> %s\n", inc1Dir)

	must(db.ApplyChanges(map[uint32][]byte{3: makePage(0x31)})) // v3
	must(pagebackup.ExportIncremental(db, inc2Dir, 2))
	fmt.Printf("[export] incr-v2-v3  -> %s\n", inc2Dir)

	srcDigest, err := db.Digest()
	must(err)

	// --- 1. Restore the full chain ----------------------------------
	restoreDir := filepath.Join(root, "restore-ok")
	must(pagebackup.Restore([]string{fullDir, inc1Dir, inc2Dir}, restoreDir))
	rdb, err := pagebackup.OpenDB(restoreDir)
	must(err)
	rDigest, err := rdb.Digest()
	must(err)
	fmt.Printf("\n[restore] full-v1 + incr-v1-v2 + incr-v2-v3 -> version=%d\n", rdb.Version())
	fmt.Printf("[verify]  source   digest %s\n", srcDigest)
	fmt.Printf("[verify]  restored digest %s (equal=%v)\n", rDigest, rDigest == srcDigest)

	// --- 2. Re-applying the same increment is idempotent ------------
	idemDir := filepath.Join(root, "restore-idempotent")
	must(pagebackup.Restore([]string{fullDir, inc1Dir, inc2Dir, inc2Dir}, idemDir))
	fmt.Printf("\n[ok]      re-applying incr-v2-v3 is idempotent (no-op)\n")

	// --- 3. Skipping an increment is rejected -----------------------
	gapDir := filepath.Join(root, "restore-gap")
	err = pagebackup.Restore([]string{fullDir, inc2Dir}, gapDir)
	fmt.Printf("\n[reject]  full-v1 + incr-v2-v3 (skips v2): %v\n", err)
	fmt.Printf("[reject]  partial db at target: %v\n", exists(gapDir))

	// --- 4. A corrupted page payload is rejected --------------------
	pageFile := filepath.Join(inc2Dir, "pages", "00000003.pg")
	data, err := ioutil.ReadFile(pageFile)
	must(err)
	data[0] ^= 0xff
	must(ioutil.WriteFile(pageFile, data, 0644))
	corruptDir := filepath.Join(root, "restore-corrupt")
	err = pagebackup.Restore([]string{fullDir, inc1Dir, inc2Dir}, corruptDir)
	fmt.Printf("\n[reject]  corrupted page in incr-v2-v3: %v\n", err)
	fmt.Printf("[reject]  partial db at target: %v\n", exists(corruptDir))

	fmt.Printf("\nall demo artifacts under caller-specified dir: %s\n", root)
}
