package transcript_test

import (
	"fmt"

	"example.org/netsec/uid1271/transcript"
)

// ExampleBuilder shows the intended host flow: phased fields, a chunked
// large field, an unknown optional frame, and a digest available only
// after Close.
func ExampleBuilder() {
	b := transcript.New()

	// Phase 0: challenge fields.
	_ = b.WriteUint(1, 42)
	_ = b.WriteBytes(2, []byte("client-nonce"))

	// Phase 1: response with a streamed large field.
	_ = b.BeginPhase(1)
	fw, _ := b.BeginField(3, 13)
	_, _ = fw.Write([]byte("chunk-"))
	_, _ = fw.Write([]byte("payload"))
	_ = fw.Close()

	// An unknown optional field is fed in as a validated raw frame and
	// still contributes to the digest.
	unknown := []byte{1, 9, 1, 5, 'h', 'e', 'l', 'l', 'o'}
	if err := b.WriteRawFrame(unknown); err != nil {
		fmt.Println("raw frame rejected:", err)
	}

	// Digest is unavailable before close.
	if _, err := b.Digest(); err != nil {
		fmt.Println(err)
	}

	d, err := b.Close()
	if err != nil {
		fmt.Println("close failed:", err)
	}
	fmt.Printf("fields=%d phase=%d len=%d sum64=%x\n",
		d.Fields, d.Phases, d.EncodedLen, d.Sum[:8])
	// Output:
	// transcript: digest unavailable until close
	// fields=4 phase=1 len=52 sum64=fcc3ca3de754c7b2
}
