// Command demo walks through the crash-consistency acceptance scenario:
//
//	1. create a DB, add two L0 SSTables (committed via the manifest);
//	2. start an L0 -> L1 compaction, write the merged output file;
//	3. simulate a crash BEFORE the VersionEdit is committed;
//	4. reopen: the manifest is replayed, the orphan SSTable is deleted,
//	   and the pre-compaction version is restored;
//	5. retry the compaction to completion and reopen once more to show
//	   the committed state is durable.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"

	"versionset-demo/versionset"
)

func listDir(dir string) []string {
	entries, _ := ioutil.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func show(vs *versionset.VersionSet, dir string) {
	cur := vs.Current()
	for l := 0; l < versionset.NumLevels; l++ {
		for _, f := range cur.Files[l] {
			fmt.Printf("    L%d: %06d.sst keys[%s..%s] size=%d crc=%08x\n",
				l, f.Number, f.Smallest, f.Largest, f.Size, f.Checksum)
		}
	}
	fmt.Printf("    disk: %v\n", listDir(dir))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func addL0(vs *versionset.VersionSet, smallest, largest, data string) versionset.FileMetadata {
	meta, err := vs.WriteSST(smallest, largest, []byte(data))
	must(err)
	must(vs.LogAndApply(&versionset.VersionEdit{
		NewFiles: []versionset.NewFileEntry{{Level: 0, Meta: meta}},
	}))
	return meta
}

func main() {
	dir, err := ioutil.TempDir("", "versionset-demo-")
	must(err)
	defer os.RemoveAll(dir)
	fmt.Printf("DB dir: %s\n\n", dir)

	fmt.Println("[1] Open DB, add two L0 SSTables (each committed atomically)")
	vs, err := versionset.Open(dir)
	must(err)
	m1 := addL0(vs, "a", "c", "rows: a,b,c")
	m2 := addL0(vs, "d", "f", "rows: d,e,f")
	show(vs, dir)

	fmt.Println("\n[2] Start L0 -> L1 compaction: write merged output file")
	orphan, err := vs.WriteSST("a", "f", []byte("rows: a,b,c,d,e,f"))
	must(err)
	fmt.Printf("    wrote %06d.sst (not yet committed to manifest)\n", orphan.Number)
	show(vs, dir)

	fmt.Println("\n[3] *** CRASH *** process dies before LogAndApply commits the edit")
	fmt.Println("    (no Close, no manifest append for the new file)")

	fmt.Println("\n[4] Restart: replay MANIFEST, drop orphan, restore pre-compaction version")
	vs2, err := versionset.Open(dir)
	must(err)
	show(vs2, dir)
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%06d.sst", orphan.Number))); os.IsNotExist(err) {
		fmt.Printf("    orphan %06d.sst removed by recovery\n", orphan.Number)
	}
	must(vs2.VerifyChecksums())
	fmt.Println("    checksums verified OK")

	fmt.Println("\n[5] Retry compaction, commit, restart again: new version is durable")
	out, err := vs2.CompactL0ToL1([]uint64{m1.Number, m2.Number}, "a", "f", []byte("rows: a,b,c,d,e,f"))
	must(err)
	fmt.Printf("    committed L1 output %06d.sst\n", out.Number)
	must(vs2.Close())
	vs3, err := versionset.Open(dir)
	must(err)
	defer vs3.Close()
	show(vs3, dir)
	must(vs3.VerifyChecksums())
	fmt.Println("    checksums verified OK")
}
