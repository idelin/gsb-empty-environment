package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"

	fragmentreassembly "fragmentreassembly"
)

type randomChunks struct {
	data []byte
	rng  *rand.Rand
}

func (r *randomChunks) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.rng.Intn(len(p)) + 1
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func main() {
	message := []byte("The quick brown fox jumps over the lazy dog.")
	descriptions := []struct {
		offset uint32
		end    uint32
	}{
		{34, 44},
		{0, 16},
		{16, 35},
	}

	var wire bytes.Buffer
	for _, part := range descriptions {
		frag := fragmentreassembly.Fragment{
			Header: fragmentreassembly.Header{
				MessageID: 9001,
				TotalSize: uint32(len(message)),
				Offset:    part.offset,
			},
			Payload: message[part.offset:part.end],
		}
		frame, err := frag.Marshal()
		if err != nil {
			panic(err)
		}
		wire.Write(frame)
	}

	r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{
		MaxMessageSize: 128,
		GlobalBytes:    4096,
	})
	if err != nil {
		panic(err)
	}

	reader := &randomChunks{data: wire.Bytes(), rng: rand.New(rand.NewSource(7))}
	fmt.Printf("message id=9001, %d bytes, fragments sent in reverse-ish order\n", len(message))
	for i := 1; ; i++ {
		frag, err := fragmentreassembly.ReadFrame(reader, r.Allow)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			panic(err)
		}
		result, err := r.Add(frag)
		if err != nil {
			panic(err)
		}
		fmt.Printf("frame %d: offset=%2d length=%2d complete=%v duplicate=%v\n", i, frag.Offset, len(frag.Payload), result.Complete, result.Duplicate)
		if result.Complete {
			fmt.Printf("reassembled: %s\n", result.Data)
		}
	}
}
