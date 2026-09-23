// Command dzdemo demonstrates the dzblock format end to end:
// dictionary-bound independent blocks, random-access decoding,
// corrupt-block resynchronisation and wrong-dictionary rejection.
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"

	"dzblock"
)

var dictionary = []byte(`{"level":"info","msg":"request completed","status":200,"path":"/api/v1/users"}
{"level":"warn","msg":"request slow","status":200,"path":"/api/v1/orders"}
{"level":"error","msg":"request failed","status":500,"path":"/api/v1/users"}
`)

func makeBlock(tag string, lines int) []byte {
	var b bytes.Buffer
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, `{"level":"info","msg":"request completed","status":200,"path":"/api/v1/users","worker":%q,"seq":%d}`, tag, i)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func main() {
	// 1. Encode a stream of independent blocks against one dictionary.
	blocks := [][]byte{
		makeBlock("alpha", 120),
		makeBlock("beta", 120),
		makeBlock("gamma", 120),
	}
	stream := dzblock.AppendStreamHeader(nil, dictionary)
	rawTotal := 0
	for _, b := range blocks {
		var err error
		stream, err = dzblock.AppendBlock(stream, dictionary, b)
		if err != nil {
			log.Fatal(err)
		}
		rawTotal += len(b)
	}
	fmt.Printf("encoded: %d blocks, %d raw bytes -> %d stream bytes (%.1f%%)\n",
		len(blocks), rawTotal, len(stream), 100*float64(len(stream))/float64(rawTotal))

	// 2. Sequential decode with a bounded output quota.
	rd, err := dzblock.NewReader(bytes.NewReader(stream), dictionary, 1<<20)
	if err != nil {
		log.Fatal(err)
	}
	n := 0
	for {
		b, err := rd.ReadBlock()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		if !bytes.Equal(b, blocks[n]) {
			log.Fatalf("block %d round-trip mismatch", n)
		}
		n++
	}
	fmt.Printf("decoded: %d blocks, all round-trip checks passed\n", n)

	// 3. Random access: decode only block 2, no state from block 1.
	off := dzblock.StreamHeaderSize
	hdr1, err := dzblock.ParseBlockHeader(stream[off:])
	if err != nil {
		log.Fatal(err)
	}
	off += dzblock.BlockHeaderSize + int(hdr1.EncLen)
	hdr2, err := dzblock.ParseBlockHeader(stream[off:])
	if err != nil {
		log.Fatal(err)
	}
	payload := stream[off+dzblock.BlockHeaderSize : off+dzblock.BlockHeaderSize+int(hdr2.EncLen)]
	only2, err := dzblock.DecodeBlock(dictionary, hdr2, payload, dzblock.MaxBlockRaw)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("random access: block 2 decoded standalone, matches original: %v\n",
		bytes.Equal(only2, blocks[1]))

	// 4. Corrupt block 2's payload; block 3 must still be locatable.
	stream[dzblock.StreamHeaderSize+dzblock.BlockHeaderSize+int(hdr1.EncLen)+dzblock.BlockHeaderSize+5] ^= 0xFF
	rd, err = dzblock.NewReader(bytes.NewReader(stream), dictionary, 1<<20)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := rd.ReadBlock(); err != nil {
		log.Fatalf("block 1 should decode: %v", err)
	}
	_, err = rd.ReadBlock()
	fmt.Printf("corrupt block 2 rejected: %v\n", err)
	if err := rd.Resync(); err != nil {
		log.Fatal(err)
	}
	b3, err := rd.ReadBlock()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("resync: block 3 recovered after corrupt block 2, matches: %v\n",
		bytes.Equal(b3, blocks[2]))

	// 5. A stream opened with the wrong dictionary is rejected.
	if _, err := dzblock.NewReader(bytes.NewReader(stream), []byte("wrong"), 1<<20); err != nil {
		fmt.Printf("wrong dictionary rejected: %v\n", err)
	}
}
