package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestArrayBulkWithCRLFAndInjectedCommandIsOneArgument(t *testing.T) {
	f := New("GET")

	// The bulk string's declared byte length includes two CRLF pairs and a
	// complete RESP-looking command. It is argument data, never a new request.
	input := "*2\r\n$3\r\nGET\r\n$9\r\na\r\nPING\r\n\r\n"
	frames := feedByteByByte(t, f, input)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	frame := frames[0]
	if !frame.Forwarded {
		t.Fatalf("GET was rejected: %s", frame.Reason)
	}
	if got, want := string(frame.Request), input; got != want {
		t.Fatalf("canonical request = %q, want %q", got, want)
	}

	commands := decodeCanonical(t, frame.Request)
	if len(commands) != 1 {
		t.Fatalf("backend would see %d commands, want 1", len(commands))
	}
	if got, want := string(commands[0][0]), "GET"; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if got, want := string(commands[0][1]), "a\r\nPING\r\n"; got != want {
		t.Fatalf("argument = %q, want %q", got, want)
	}
}

func TestChineseArgumentUsesByteLength(t *testing.T) {
	f := New("GET")
	input := "*2\r\n$3\r\nGET\r\n$6\r\n中文\r\n"

	frames := feedByteByByte(t, f, input)
	if len(frames) != 1 || !frames[0].Forwarded {
		t.Fatalf("expected one forwarded frame, got %+v", frames)
	}
	if got, want := string(frames[0].Request), input; got != want {
		t.Fatalf("canonical request = %q, want %q", got, want)
	}

	commands := decodeCanonical(t, frames[0].Request)
	if got, want := string(commands[0][1]), "中文"; got != want {
		t.Fatalf("argument = %q, want %q", got, want)
	}
}

func TestLengthPrefixByteMismatchIsRejected(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"multi-byte too short", "*2\r\n$3\r\nGET\r\n$4\r\n中文\r\n"},
		{"single multi-byte character too short", "*2\r\n$3\r\nGET\r\n$2\r\n中\r\n"},
		{"payload longer than prefix without CRLF", "*2\r\n$3\r\nGET\r\n$2\r\nabc\r\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := New("GET")
			frames, err := f.Process([]byte(tc.input))
			if err == nil {
				t.Fatal("expected fatal framing error")
			}
			if len(frames) != 1 {
				t.Fatalf("got %d frames, want one synthetic error", len(frames))
			}
			if frames[0].Forwarded || frames[0].Request != nil {
				t.Fatalf("malformed request was forwarded: %+v", frames[0])
			}
			if got := string(frames[0].Reply); !strings.HasPrefix(got, "-ERR ") {
				t.Fatalf("reply = %q, want Redis error reply", got)
			}
		})
	}
}

func TestInlineCommandsAreReencodedAsBulkStrings(t *testing.T) {
	f := New("GET")

	t.Run("UTF-8 argument", func(t *testing.T) {
		frames := feedByteByByte(t, f, "GET 中文\r\n")
		if len(frames) != 1 || !frames[0].Forwarded {
			t.Fatalf("expected forwarded frame, got %+v", frames)
		}
		want := "*2\r\n$3\r\nGET\r\n$6\r\n中文\r\n"
		if got := string(frames[0].Request); got != want {
			t.Fatalf("canonical request = %q, want %q", got, want)
		}
	})

	t.Run("escaped CRLF inside quotes", func(t *testing.T) {
		frames := feedByteByByte(t, f, "GET \"a\\r\\nPING\"\r\n")
		if len(frames) != 1 || !frames[0].Forwarded {
			t.Fatalf("expected forwarded frame, got %+v", frames)
		}
		want := "*2\r\n$3\r\nGET\r\n$7\r\na\r\nPING\r\n"
		if got := string(frames[0].Request); got != want {
			t.Fatalf("canonical request = %q, want %q", got, want)
		}
		commands := decodeCanonical(t, frames[0].Request)
		if len(commands) != 1 || string(commands[0][1]) != "a\r\nPING" {
			t.Fatalf("backend saw %#v", commands)
		}
	})

	t.Run("RESP fragment is ordinary argument data", func(t *testing.T) {
		frames := feedByteByByte(t, f, "GET \"$4\\r\\nPING\"\r\n")
		if len(frames) != 1 || !frames[0].Forwarded {
			t.Fatalf("expected forwarded frame, got %+v", frames)
		}
		want := "*2\r\n$3\r\nGET\r\n$8\r\n$4\r\nPING\r\n"
		if got := string(frames[0].Request); got != want {
			t.Fatalf("canonical request = %q, want %q", got, want)
		}
	})

	t.Run("hex escaped CRLF", func(t *testing.T) {
		frames := feedByteByByte(t, f, "GET \"a\\x0d\\x0aPING\"\r\n")
		if len(frames) != 1 || !frames[0].Forwarded {
			t.Fatalf("expected forwarded frame, got %+v", frames)
		}
		if got, want := string(frames[0].Request), "*2\r\n$3\r\nGET\r\n$7\r\na\r\nPING\r\n"; got != want {
			t.Fatalf("canonical request = %q, want %q", got, want)
		}
	})

	t.Run("raw CRLF starts the next request", func(t *testing.T) {
		frames := feedByteByByte(t, f, "GET foo\r\nPING\r\n")
		if len(frames) != 2 {
			t.Fatalf("got %d frames, want 2", len(frames))
		}
		if !frames[0].Forwarded {
			t.Fatalf("GET rejected: %s", frames[0].Reason)
		}
		if frames[1].Forwarded {
			t.Fatalf("PING was forwarded")
		}
		assertBackendCommands(t, frames, "GET")
	})
}

func TestShortLengthPrefixCannotSmuggleFollowingCommand(t *testing.T) {
	f := New("GET")
	// The array declares two elements and a two-byte argument. PING is thus a
	// separate inline request, not part of the GET request; it must be
	// rejected rather than forwarded after the bulk payload.
	input := "*2\r\n$3\r\nGET\r\n$2\r\nab\r\nPING\r\n"
	frames := feedByteByByte(t, f, input)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if !frames[0].Forwarded || frames[1].Forwarded {
		t.Fatalf("unexpected forwarding decisions: %+v", frames)
	}
	assertBackendCommands(t, frames, "GET")
}

func TestPipelineKeepsRejectedReplyInOriginalPosition(t *testing.T) {
	f := New("GET")
	pipeline := "" +
		"*2\r\n$3\r\nGET\r\n$6\r\n中文\r\n" +
		"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n" +
		"*2\r\n$3\r\nGET\r\n$9\r\na\r\nPING\r\n\r\n"

	for _, chunk := range chunkPatterns(len(pipeline)) {
		frames := feedWithPattern(t, f, pipeline, chunk)
		if len(frames) != 3 {
			t.Fatalf("chunks %v: got %d frames, want 3", chunk, len(frames))
		}
		if !frames[0].Forwarded || frames[1].Forwarded || !frames[2].Forwarded {
			t.Fatalf("chunks %v: wrong forwarding decisions: %+v", chunk, frames)
		}
		if got := string(frames[1].Reply); got != "-ERR command not allowed\r\n" {
			t.Fatalf("chunks %v: local reply = %q", chunk, got)
		}
		assertBackendCommands(t, frames, "GET", "GET")

		// A forwarding loop queues one response slot per request. Backend
		// replies fill only forwarded slots; the local error occupies the slot
		// of the rejected SET in the middle.
		slots := responseSlots(frames, []string{"+reply-1\r\n", "+reply-2\r\n"})
		want := []string{"+reply-1\r\n", "-ERR command not allowed\r\n", "+reply-2\r\n"}
		if !equalStrings(slots, want) {
			t.Fatalf("chunks %v: response slots = %q, want %q", chunk, slots, want)
		}
	}
}

func TestNestingDepthIsBoundedAndStreamStaysSynchronised(t *testing.T) {
	f, err := NewFilter([]string{"GET"}, WithMaxDepth(4))
	if err != nil {
		t.Fatal(err)
	}

	deep := "$3\r\nGET\r\n"
	for i := 0; i < 64; i++ {
		deep = "*1\r\n" + deep
	}
	deep += "*2\r\n$3\r\nGET\r\n$1\r\nk\r\n"

	frames, err := f.Process([]byte(deep))
	if err != nil {
		t.Fatalf("depth rejection should not be fatal: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if frames[0].Forwarded || frames[0].Reason != ReasonDepthExceeded {
		t.Fatalf("deep request: %+v", frames[0])
	}
	if !frames[1].Forwarded {
		t.Fatalf("request after deep value was rejected: %s", frames[1].Reason)
	}
	assertBackendCommands(t, frames, "GET")
}

func TestRejectedRequestInsideMULTI(t *testing.T) {
	pipeline := "" +
		"*1\r\n$5\r\nMULTI\r\n" +
		"*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n" +
		"*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n" +
		"*1\r\n$4\r\nEXEC\r\n"

	t.Run("MULTI itself is not configured", func(t *testing.T) {
		f := New("GET")
		frames := feedWithPattern(t, f, pipeline, []int{1, 3, 2, 7})
		if len(frames) != 4 {
			t.Fatalf("got %d frames, want 4", len(frames))
		}
		if frames[0].Forwarded || frames[1].Forwarded || !frames[2].Forwarded || frames[3].Forwarded {
			t.Fatalf("unexpected forwarding decisions: %+v", frames)
		}
		assertBackendCommands(t, frames, "GET")

		slots := responseSlots(frames, []string{"+queued-get\r\n"})
		want := []string{
			"-ERR command not allowed\r\n",
			"-ERR command not allowed\r\n",
			"+queued-get\r\n",
			"-ERR command not allowed\r\n",
		}
		if !equalStrings(slots, want) {
			t.Fatalf("response slots = %q, want %q", slots, want)
		}
	})

	t.Run("MULTI and EXEC allowed but inner SET rejected", func(t *testing.T) {
		f := New("multi", "GET", "exec")
		frames := feedWithPattern(t, f, pipeline, []int{2, 1, 5})
		if len(frames) != 4 {
			t.Fatalf("got %d frames, want 4", len(frames))
		}
		if !frames[0].Forwarded || frames[1].Forwarded || !frames[2].Forwarded || !frames[3].Forwarded {
			t.Fatalf("unexpected forwarding decisions: %+v", frames)
		}
		assertBackendCommands(t, frames, "MULTI", "GET", "EXEC")

		slots := responseSlots(frames, []string{"+OK\r\n", "+QUEUED\r\n", "*1\r\n+value\r\n"})
		want := []string{"+OK\r\n", "-ERR command not allowed\r\n", "+QUEUED\r\n", "*1\r\n+value\r\n"}
		if !equalStrings(slots, want) {
			t.Fatalf("response slots = %q, want %q", slots, want)
		}
	})
}

func TestCommandAllowListIsCaseInsensitive(t *testing.T) {
	f := New("gEt", "MuLtI", "ExEc")
	input := "*3\r\n$3\r\nGeT\r\n$3\r\nFoo\r\n$4\r\na\r\nb\r\n\r\n"

	frames := feedByteByByte(t, f, input)
	if len(frames) != 1 || !frames[0].Forwarded {
		t.Fatalf("GET variant rejected: %+v", frames)
	}
	if frames[0].Command != "get" {
		t.Fatalf("command = %q, want get", frames[0].Command)
	}
	assertBackendCommands(t, frames, "GET")
}

func TestForbiddenReplyDoesNotEchoAttackerBytes(t *testing.T) {
	f := New("GET")
	input := "*3\r\n$3\r\nSET\r\n$9\r\na\r\nPING\r\n\r\n$1\r\nv\r\n"

	frames, err := f.Process([]byte(input))
	if err != nil {
		t.Fatalf("policy rejection should not be fatal: %v", err)
	}
	if len(frames) != 1 || frames[0].Forwarded {
		t.Fatalf("unexpected frame: %+v", frames)
	}
	if got, want := string(frames[0].Reply), "-ERR command not allowed\r\n"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	if bytes.Contains(frames[0].Reply, []byte("PING")) {
		t.Fatal("reply echoes attacker-controlled data")
	}
}

func TestTruncatedInputAtFlushIsRejected(t *testing.T) {
	f := New("GET")
	stream := f.NewStream()
	frames, err := stream.Feed([]byte("*2\r\n$3\r\nGET\r\n$1\r"))
	if err != nil {
		t.Fatalf("partial request should wait for more data: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("got %d frames for incomplete request", len(frames))
	}
	tail, err := stream.Flush()
	if err == nil {
		t.Fatal("expected error for truncated request")
	}
	if len(tail) != 1 || tail[0].Forwarded {
		t.Fatalf("flush frames = %+v", tail)
	}
}

func TestEmptyInlineLineProducesNoFrame(t *testing.T) {
	f := New("GET")
	frames, err := f.Process([]byte("  \t\r\n*1\r\n$3\r\nGET\r\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(frames) != 1 || !frames[0].Forwarded {
		t.Fatalf("frames = %+v", frames)
	}
	assertBackendCommands(t, frames, "GET")
}

func TestLimits(t *testing.T) {
	t.Run("bulk length", func(t *testing.T) {
		f, err := NewFilter([]string{"GET"}, WithMaxBulkLength(3))
		if err != nil {
			t.Fatal(err)
		}
		overflow, err := f.Process([]byte("*2\r\n$3\r\nGET\r\n$4\r\nabcd\r\n"))
		if err == nil {
			t.Fatal("expected fatal bulk-limit error")
		}
		if len(overflow) != 1 || overflow[0].Forwarded {
			t.Fatalf("frames = %+v", overflow)
		}
		if overflow[0].Reason != ReasonTooLarge {
			t.Fatalf("reason = %q", overflow[0].Reason)
		}
	})

	t.Run("inline length", func(t *testing.T) {
		f, err := NewFilter([]string{"GET"}, WithMaxInlineLength(8))
		if err != nil {
			t.Fatal(err)
		}
		frames, err := f.Process([]byte("GET 0123456789\r\n"))
		if err != nil {
			t.Fatalf("a complete overlong inline line is a policy rejection: %v", err)
		}
		if len(frames) != 1 || frames[0].Forwarded || frames[0].Reason != ReasonTooLarge {
			t.Fatalf("frames = %+v", frames)
		}
	})

	t.Run("argument count", func(t *testing.T) {
		f, err := NewFilter([]string{"GET"}, WithMaxArguments(2))
		if err != nil {
			t.Fatal(err)
		}
		frames, err := f.Process([]byte("*3\r\n$3\r\nGET\r\n$1\r\na\r\n$1\r\nb\r\n"))
		if err != nil {
			t.Fatalf("complete request should be rejected, not fatal: %v", err)
		}
		if len(frames) != 1 || frames[0].Forwarded || frames[0].Reason != ReasonTooManyArguments {
			t.Fatalf("frames = %+v", frames)
		}
	})
}

func feedByteByByte(t *testing.T, f *Filter, input string) []Frame {
	t.Helper()
	return feedWithPattern(t, f, input, []int{1})
}

func feedWithPattern(t *testing.T, f *Filter, input string, pattern []int) []Frame {
	t.Helper()
	stream := f.NewStream()
	var frames []Frame
	data := []byte(input)
	pos := 0
	i := 0
	for pos < len(data) {
		size := pattern[i%len(pattern)]
		if size <= 0 {
			t.Fatalf("invalid chunk size %d", size)
		}
		if remaining := len(data) - pos; size > remaining {
			size = remaining
		}
		got, err := stream.Feed(data[pos : pos+size])
		if err != nil {
			t.Fatalf("Feed(%d..%d): %v", pos, pos+size, err)
		}
		frames = append(frames, got...)
		pos += size
		i++
	}
	tail, err := stream.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return append(frames, tail...)
}

func chunkPatterns(length int) [][]int {
	return [][]int{
		{1},
		{length},
		{2, 3, 5, 7, 11},
		{13, 1, 64, 2},
	}
}

func responseSlots(frames []Frame, backendReplies []string) []string {
	var slots []string
	backend := 0
	for _, frame := range frames {
		if frame.Forwarded {
			slots = append(slots, backendReplies[backend])
			backend++
		} else {
			slots = append(slots, string(frame.Reply))
		}
	}
	return slots
}

func assertBackendCommands(t *testing.T, frames []Frame, want ...string) {
	t.Helper()
	var backend []byte
	for _, frame := range frames {
		if !frame.Forwarded {
			continue
		}
		backend = append(backend, frame.Request...)
	}
	requests := decodeCanonical(t, backend)
	if len(requests) != len(want) {
		t.Fatalf("backend received %d requests, want %d: %#v", len(requests), len(want), requests)
	}
	for i := range want {
		if got := string(requests[i][0]); !strings.EqualFold(got, want[i]) {
			t.Fatalf("backend request %d command = %q, want %q", i, got, want[i])
		}
	}
}

// decodeCanonical is an independent strict decoder used to prove that the
// bytes handed to the backend contain exactly the generated bulk arrays and
// no smuggled requests.
func decodeCanonical(t *testing.T, raw []byte) [][][]byte {
	t.Helper()
	var requests [][][]byte
	pos := 0
	for pos < len(raw) {
		if raw[pos] != '*' {
			t.Fatalf("backend byte %d: expected array prefix, got %q", pos, raw[pos])
		}
		line, next := canonicalLine(t, raw, pos)
		count, err := strconv.Atoi(line)
		if err != nil || count < 1 {
			t.Fatalf("backend array count %q invalid", line)
		}
		pos = next
		args := make([][]byte, 0, count)
		for i := 0; i < count; i++ {
			if pos >= len(raw) || raw[pos] != '$' {
				t.Fatalf("backend byte %d: expected bulk prefix", pos)
			}
			line, next = canonicalLine(t, raw, pos)
			length, err := strconv.Atoi(line)
			if err != nil || length < 0 {
				t.Fatalf("backend bulk length %q invalid", line)
			}
			pos = next
			if pos+length+2 > len(raw) {
				t.Fatalf("backend bulk payload truncated at %d", pos)
			}
			args = append(args, append([]byte(nil), raw[pos:pos+length]...))
			pos += length
			if raw[pos] != '\r' || raw[pos+1] != '\n' {
				t.Fatalf("backend bulk at %d is not CRLF terminated", pos)
			}
			pos += 2
		}
		requests = append(requests, args)
	}
	return requests
}

func canonicalLine(t *testing.T, raw []byte, pos int) (string, int) {
	t.Helper()
	end := bytes.Index(raw[pos+1:], []byte("\r\n"))
	if end < 0 {
		t.Fatalf("backend header at %d is truncated", pos)
	}
	end += pos + 1
	return string(raw[pos+1 : end]), end + 2
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestProtocolErrorType(t *testing.T) {
	f := New("GET")
	_, err := f.Process([]byte("*2\r\n$3\r\nGET\r\n$1\r\nab"))
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("error = %v, want *ProtocolError", err)
	}
	if protocolErr.Reason != ReasonProtocol {
		t.Fatalf("reason = %q", protocolErr.Reason)
	}
}

func ExampleFilter() {
	f := New("GET")
	frames, _ := f.Process([]byte("GET foo\r\nSET key value\r\n"))
	for i, frame := range frames {
		if frame.Forwarded {
			fmt.Printf("slot %d: forward %q\n", i, frame.Request)
		} else {
			fmt.Printf("slot %d: local %q\n", i, frame.Reply)
		}
	}
	// Output:
	// slot 0: forward "*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n"
	// slot 1: local "-ERR command not allowed\r\n"
}
