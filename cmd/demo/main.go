// Demo: segmented range coding with verifiable model checkpoints.
package main

import (
	"bytes"
	"fmt"
	"log"

	rc "rangecoder"
)

func main() {
	parts := [][]byte{
		bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 8),
		bytes.Repeat([]byte("pack my box with five dozen liquor jugs. "), 8),
		bytes.Repeat([]byte("how vexingly quick daft zebras jump! "), 8),
	}

	// Encode three segments; snapshot the model at each boundary.
	enc := rc.NewEncoder()
	var streams, checkpoints [][]byte
	for i, p := range parts {
		enc.Encode(p)
		streams = append(streams, enc.FinishSegment())
		if i+1 < len(parts) {
			checkpoints = append(checkpoints, enc.Snapshot())
		}
	}

	raw := 0
	for i, p := range parts {
		raw += len(p)
		fmt.Printf("segment %d: raw=%4d bytes  coded=%4d bytes\n", i, len(p), len(streams[i]))
	}
	coded := 0
	for _, s := range streams {
		coded += len(s)
	}
	fmt.Printf("total: raw=%d coded=%d ratio=%.2f  checkpoint=%d bytes each\n",
		raw, coded, float64(coded)/float64(raw), len(checkpoints[0]))

	// Sequential decode: the model carries across segment boundaries.
	dec := rc.NewDecoder(streams[0])
	for i, s := range streams {
		if i > 0 {
			dec.NextSegment(s)
		}
		got, err := dec.ReadAll()
		if err != nil || !bytes.Equal(got, parts[i]) {
			log.Fatalf("sequential decode segment %d failed: %v", i, err)
		}
	}
	fmt.Println("sequential decode: all segments OK")

	// Random access: decode the last segment from its checkpoint only.
	rdec, err := rc.NewDecoderFromCheckpoint(checkpoints[len(checkpoints)-1], streams[2])
	if err != nil {
		log.Fatal(err)
	}
	got, err := rdec.ReadAll()
	if err != nil || !bytes.Equal(got, parts[2]) {
		log.Fatalf("random-access decode failed: %v", err)
	}
	fmt.Println("random-access decode of segment 2 from checkpoint: OK")

	// Corrupt segment 0 completely; segment 2 must still decode.
	for i := range streams[0] {
		streams[0][i] ^= 0xFF
	}
	dec2, _ := rc.NewDecoderFromCheckpoint(checkpoints[len(checkpoints)-1], streams[2])
	got2, err := dec2.ReadAll()
	if err != nil || !bytes.Equal(got2, parts[2]) {
		log.Fatalf("segment 2 polluted by segment 0 corruption: %v", err)
	}
	fmt.Println("segment 0 corrupted -> segment 2 still decodes: OK")

	// A checkpoint with the wrong version is rejected.
	bad := append([]byte(nil), checkpoints[0]...)
	bad[4], bad[5] = 0, 99 // version field
	if _, err := rc.NewDecoderFromCheckpoint(bad, streams[1]); err != nil {
		fmt.Printf("checkpoint with version 99 rejected: %v\n", err)
	}
	// A checkpoint with a flipped payload bit is rejected.
	bad2 := append([]byte(nil), checkpoints[0]...)
	bad2[len(bad2)-1] ^= 0x01
	if _, err := rc.NewDecoderFromCheckpoint(bad2, streams[1]); err != nil {
		fmt.Printf("checkpoint with flipped bit rejected: %v\n", err)
	}
}
