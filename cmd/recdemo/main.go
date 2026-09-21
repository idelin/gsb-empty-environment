// Command recdemo demonstrates the recstream package: it writes records,
// decodes them from arbitrary byte chunks, builds a random-access index,
// performs verified reads, and shows how corruption is rejected.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	rec "recstream"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	dir, err := os.MkdirTemp("", "recdemo-")
	must(err)
	defer os.RemoveAll(dir)
	srcPath := filepath.Join(dir, "stream.bin")
	idxPath := filepath.Join(dir, "stream.index")

	// 1) Write a small stream, including an empty record.
	f, err := os.Create(srcPath)
	must(err)
	w := rec.NewWriter(f)
	records := []*rec.Record{
		rec.NewRecord(), // empty record
		rec.NewRecord(rec.NewVarint(1, 150)),
		rec.NewRecord(
			rec.NewVarint(1, 42),
			rec.NewFixed32(2, 7),
			rec.NewBytes(3, []byte("hello")),
			rec.NewFixed64(99, 0xcafef00d), // newer field an old reader skips
		),
	}
	for i, r := range records {
		off, size, err := w.Write(r)
		must(err)
		fmt.Printf("write record %d at offset %d (frame %d bytes)\n", i, off, size)
	}
	must(f.Close())

	// 2) Sequential decode.
	data, err := os.ReadFile(srcPath)
	must(err)
	dec := rec.NewDecoder(bytes.NewReader(data))
	fmt.Println("\nsequential decode:")
	for i := 0; ; i++ {
		r, err := dec.Next()
		if err != nil {
			break
		}
		fmt.Printf("  record %d: %d field(s)\n", i, len(r.Fields))
	}

	// 3) Incremental decode in 1-byte chunks (arbitrary chunking).
	fmt.Println("\nchunk decode (one byte per feed):")
	cd := rec.NewChunkDecoder()
	var n int
	for _, b := range data {
		rs, err := cd.Feed([]byte{b})
		mustFeed(err)
		n += len(rs)
	}
	must(cd.Finalize())
	fmt.Printf("  reassembled %d complete records\n", n)

	// 4) Build and persist an index.
	sf, err := os.Open(srcPath)
	must(err)
	idx, err := rec.BuildIndex(sf)
	must(err)
	must(sf.Close())
	must(rec.SaveIndexFile(idxPath, idx))
	fmt.Printf("\nindex: %d records, source %d bytes, digest %x…\n",
		idx.Count(), idx.SourceSize, idx.SourceDigest[:8])

	idx2, err := rec.LoadIndexFile(idxPath)
	must(err)

	// 5) Verified random access.
	srcFile, rr, err := rec.OpenAndVerify(srcPath, idx2)
	must(err)
	defer srcFile.Close()
	r, err := rr.ReadRecord(2)
	must(err)
	if v, ok := r.Get(1); ok {
		x, _ := v.Varint()
		fmt.Printf("random read record 2, field 1 (varint) = %d\n", x)
	}
	if v, ok := r.Get(99); ok { // field added by a newer writer
		x, _ := v.Fixed64()
		fmt.Printf("  forward-compatible field 99 = 0x%x\n", x)
	}

	// 6) Tamper one byte: digest mismatch and boundary failure.
	bad := append([]byte(nil), data...)
	bad[idx2.Entries[1].Offset+1] ^= 0xff
	badPath := filepath.Join(dir, "tampered.bin")
	must(os.WriteFile(badPath, bad, 0o644))
	bf, err := os.Open(badPath)
	must(err)
	if err := idx2.VerifySource(bf); err != nil {
		fmt.Printf("\ntampered file rejected by source summary: %v\n", err)
	}
	bf.Close()
}

func mustFeed(err error) {
	if err != nil && err != rec.ErrNeedMore {
		must(err)
	}
}
