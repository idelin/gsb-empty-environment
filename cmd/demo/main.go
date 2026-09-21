// Command demo writes a two-schema-version record stream, builds and persists
// a digest-protected random-access index, then reads records back by ordinal.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"example.com/recordio/recordio"
)

func main() {
	dir, err := os.MkdirTemp("", "recordio-demo-")
	must(err)
	defer os.RemoveAll(dir)

	dataPath := filepath.Join(dir, "events.bin")
	indexPath := filepath.Join(dir, "events.idx")

	// Schema v1: fields 1=name, 2=age.
	// Schema v2 adds field 3=email; old readers keep working by never
	// requesting unknown field IDs.
	var stream bytes.Buffer
	_, err = recordio.WriteStream(&stream,
		recordio.EncodeRecord(1, []recordio.Field{
			{ID: 1, Data: []byte("alice")}, {ID: 2, Data: []byte("30")},
		}),
		recordio.EncodeRecord(1, nil), // valid empty record
		recordio.EncodeRecord(2, []recordio.Field{
			{ID: 1, Data: []byte("bob")}, {ID: 2, Data: []byte("31")}, {ID: 3, Data: []byte("bob@example.com")},
		}),
	)
	must(err)
	must(os.WriteFile(dataPath, stream.Bytes(), 0o600))

	f, err := os.Open(dataPath)
	must(err)
	defer f.Close()

	idx, err := recordio.BuildIndex(f, 0)
	must(err)

	idxFile, err := os.Create(indexPath)
	must(err)
	must(idx.Save(idxFile))
	must(idxFile.Close())

	// Reload index from disk and prove it belongs to this exact source.
	stored, err := os.Open(indexPath)
	must(err)
	loaded, err := recordio.LoadIndex(stored)
	must(err)
	must(stored.Close())

	digest, err := recordio.DigestOf(bytes.NewReader(stream.Bytes()))
	must(err)
	must(loaded.VerifySource(digest))

	fmt.Printf("source: %s (%d bytes), sha256=%x\n", dataPath, stream.Len(), digest)
	fmt.Printf("index : %s, %d records\n", indexPath, loaded.Len())
	for i, off := range loaded.Offsets {
		fmt.Printf("  record #%d at byte offset %d\n", i, off)
	}

	for _, n := range []int{0, 1, 2} {
		rec, err := recordio.ReadRecordAt(f, loaded, n, 0)
		must(err)
		name, _ := rec.FieldByID(1)
		age, hasAge := rec.FieldByID(2)
		email, hasEmail := rec.FieldByID(3)
		fmt.Printf("record #%d schema=v%d name=%q", n, rec.SchemaVersion, name)
		if hasAge {
			fmt.Printf(" age=%s", age)
		}
		if hasEmail {
			fmt.Printf(" email=%s", email) // only present on v2
		}
		fmt.Println()
	}

	// Tamper with one source byte: the stored index must refuse to match.
	tampered := bytes.Clone(stream.Bytes())
	tampered[len(tampered)-1] ^= 0xFF
	badDigest := sha256.Sum256(tampered)
	if err := loaded.VerifySource(badDigest); err != nil {
		fmt.Printf("tampered source correctly rejected: %v\n", err)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
