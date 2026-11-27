package h2guard_test

import (
	"bytes"
	"fmt"

	"formal.local/w28-9d3198391f/h2guard"
)

// minimalRequestBlock is a hand-encoded HPACK block:
// 0x82 -> :method GET, 0x87 -> :scheme https, 0x84 -> :path /,
// 0x01 0x0b ... -> literal :authority example.com (H=0).
func minimalRequestBlock() []byte {
	authority := "example.com"
	block := []byte{0x82, 0x87, 0x84, 0x01, byte(len(authority))}
	return append(block, []byte(authority)...)
}

func ExampleNewParser() {
	block := minimalRequestBlock()
	var wire bytes.Buffer
	wire.Write(h2guard.BuildFrame(h2guard.FrameHeaders,
		h2guard.FlagEndHeaders|h2guard.FlagEndStream, 1, block))

	parser := h2guard.NewParser(nil)
	err := parser.Run(&wire, func(ev *h2guard.Event) {
		switch ev.Type {
		case h2guard.EventHeaders:
			fmt.Printf("headers: %s %s%s\n",
				ev.Headers.Method, ev.Headers.Authority, ev.Headers.Path)
		}
	}, func(se *h2guard.StreamError) {
		// Host would write h2guard.BuildRSTStream(se.StreamID, se.Code).
		fmt.Printf("stream reset: stream=%d code=0x%x\n", se.StreamID, se.Code)
	})
	fmt.Println("clean run:", err == nil)

	// Output:
	// headers: GET example.com/
	// clean run: true
}

func ExampleParser_interleavingTriggersGoAway() {
	block := minimalRequestBlock()

	parser := h2guard.NewParser(nil)

	// HEADERS without END_HEADERS opens a header block...
	open := &h2guard.Frame{
		Type: h2guard.FrameHeaders, Flags: 0, StreamID: 1,
		Payload: block[:2],
	}
	if _, err := parser.Handle(open); err != nil {
		panic(err)
	}

	// ...any frame other than same-stream CONTINUATION is a PROTOCOL_ERROR.
	interloper := &h2guard.Frame{
		Type: h2guard.FrameSettings, Flags: h2guard.FlagAck, StreamID: 0,
	}
	_, err := parser.Handle(interloper)
	if ce, ok := h2guard.AsConnError(err); ok {
		fmt.Printf("fatal code: 0x%x\n", ce.Code)
	}
	fmt.Println("goaway available:", parser.GoAwayFrame() != nil)

	// Output:
	// fatal code: 0x1
	// goaway available: true
}
