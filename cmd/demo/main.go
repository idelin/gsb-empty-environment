package main

import (
	"bytes"
	"fmt"
	"log"

	"runlzv2"
)

func main() {
	sample := []byte("hello hello hello " +
		string(bytes.Repeat([]byte("A"), 100)) +
		" tail tail tail")

	// Offline optimal mode.
	offline, err := runlzv2.Encode(sample)
	if err != nil {
		log.Fatal(err)
	}
	back, err := runlzv2.Decode(offline, 1<<20)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("offline: input=%d encoded=%d roundtrip=%v\n",
		len(sample), len(offline), bytes.Equal(back, sample))

	// Online bounded-buffer mode with explicit flushes.
	var stream bytes.Buffer
	enc, err := runlzv2.NewEncoderBuffer(&stream, 32)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := enc.Write(sample[:20]); err != nil {
		log.Fatal(err)
	}
	if err := enc.Flush(); err != nil { // explicit boundary, not stream end
		log.Fatal(err)
	}
	if _, err := enc.Write(sample[20:]); err != nil {
		log.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		log.Fatal(err)
	}
	online := stream.Bytes()
	back2, err := runlzv2.Decode(online, 1<<20)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("online:  input=%d encoded=%d roundtrip=%v gap=%d bytes (buffer=32, 1 flush)\n",
		len(sample), len(online), bytes.Equal(back2, sample), len(online)-len(offline))

	// Empty input distinctions.
	emptyOffline, _ := runlzv2.Encode(nil)
	var emptyStream bytes.Buffer
	e2 := runlzv2.NewEncoder(&emptyStream)
	if err := e2.Flush(); err != nil { // one explicit empty flush frame
		log.Fatal(err)
	}
	if err := e2.Close(); err != nil { // final frame
		log.Fatal(err)
	}
	fmt.Printf("empty:   offline-bytes=%d (no frame) stream-bytes=%d (flush+final frames)\n",
		len(emptyOffline), emptyStream.Len())
}
