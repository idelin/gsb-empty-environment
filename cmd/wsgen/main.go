// Command wsgen writes a small demo client-to-server WebSocket stream to
// stdout: a fragmented text message carrying the word "secret" across two
// fragments with an interleaved ping, followed by a close frame. Pipe it into
// wsprobe to see the parser in action:
//
//	go run ./cmd/wsgen | go run ./cmd/wsprobe -needle secret -chunk 5
package main

import (
	"bufio"
	"os"
)

var mask = [4]byte{0x37, 0xfa, 0x21, 0x3d}

func frame(fin bool, op byte, payload []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0, 0x80 | byte(len(payload))}
	out = append(out, mask[:]...)
	for i, c := range payload {
		out = append(out, c^mask[i%4])
	}
	return out
}

func main() {
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	w.Write(frame(false, 0x1, []byte("sec")))     // text, fragment 1
	w.Write(frame(true, 0x9, []byte("hi")))       // interleaved ping
	w.Write(frame(true, 0x0, []byte("ret!")))     // final continuation
	w.Write(frame(true, 0x8, []byte{0x03, 0xe8})) // close, status 1000
}
