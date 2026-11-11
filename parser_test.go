package imapsync

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

// chunkReader delivers data in fixed-size pieces to simulate literals and
// lines arriving split across many underlying reads.
type chunkReader struct {
	data []byte
	n    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.n
	if n > len(r.data) {
		n = len(r.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func parseOne(t *testing.T, raw string, chunk int) *Response {
	t.Helper()
	p := newParser(&chunkReader{data: []byte(raw), n: chunk})
	resp, err := p.readResponse()
	if err != nil {
		t.Fatalf("readResponse(%q): %v", raw, err)
	}
	return resp
}

func TestLiteralSpanningReads(t *testing.T) {
	raw := "* 1 FETCH (UID 7 FLAGS (\\Seen) BODY[HEADER] {11}\r\nhello world)\r\n"
	for _, chunk := range []int{1, 2, 3, 7, 4096} {
		resp := parseOne(t, raw, chunk)
		if resp.Tag != "*" || resp.Seq != 1 || resp.Type != "FETCH" {
			t.Fatalf("chunk=%d: bad header: %+v", chunk, resp)
		}
		it, ok := parseFetch(resp)
		if !ok || it.UID != 7 {
			t.Fatalf("chunk=%d: bad fetch item: %+v", chunk, it)
		}
		if !reflect.DeepEqual(it.Flags, []string{"\\Seen"}) {
			t.Fatalf("chunk=%d: bad flags: %v", chunk, it.Flags)
		}
		lst := resp.Fields[0].(List)
		if lst[4] != "BODY[HEADER]" {
			t.Fatalf("chunk=%d: section atom mangled: %v", chunk, lst[4])
		}
		if lst[5] != "hello world" {
			t.Fatalf("chunk=%d: bad literal: %q", chunk, lst[5])
		}
	}
}

func TestLiteralContainingCRLF(t *testing.T) {
	raw := "* 4 FETCH (BODY[] {4}\r\na\r\nb)\r\n"
	resp := parseOne(t, raw, 3)
	lst := resp.Fields[0].(List)
	if lst[0] != "BODY[]" {
		t.Fatalf("section atom mangled: %v", lst[0])
	}
	if lst[1] != "a\r\nb" {
		t.Fatalf("literal with embedded CRLF: %q", lst[1])
	}
}

func TestQuotedStringsAndRespCode(t *testing.T) {
	raw := "* OK [UIDVALIDITY 1000] \"quoted \\\"text\\\"\" ready\r\n"
	resp := parseOne(t, raw, 5)
	if resp.Type != "OK" {
		t.Fatalf("bad type: %q", resp.Type)
	}
	code, rest := respCode(resp.Fields)
	if len(code) != 2 || code[0] != "UIDVALIDITY" {
		t.Fatalf("bad resp code: %v", code)
	}
	if v, ok := toUint(code[1]); !ok || v != 1000 {
		t.Fatalf("bad uidvalidity: %v", code[1])
	}
	if len(rest) != 2 || rest[0] != `quoted "text"` || rest[1] != "ready" {
		t.Fatalf("bad rest: %v", rest)
	}
}

func TestNestedLists(t *testing.T) {
	raw := "* 3 FETCH (ENVELOPE (\"subj\" ((\"Name\" NIL \"user\" \"host.com\")) NIL))\r\n"
	resp := parseOne(t, raw, 4)
	env := resp.Fields[0].(List)[1].(List)
	if env[0] != "subj" {
		t.Fatalf("bad subject: %v", env[0])
	}
	addr := env[1].(List)[0].(List)
	want := List{"Name", "NIL", "user", "host.com"}
	if !reflect.DeepEqual(addr, want) {
		t.Fatalf("bad address: %v want %v", addr, want)
	}
}

func TestSearchResponse(t *testing.T) {
	resp := parseOne(t, "* SEARCH 101 102 103\r\n", 2)
	if resp.Type != "SEARCH" || len(resp.Fields) != 3 {
		t.Fatalf("bad search: %+v", resp)
	}
	for i, want := range []uint64{101, 102, 103} {
		if got, ok := toUint(resp.Fields[i]); !ok || got != want {
			t.Fatalf("field %d: got %v want %d", i, resp.Fields[i], want)
		}
	}
}

func TestUnbalancedList(t *testing.T) {
	p := newParser(strings.NewReader("* 1 FETCH (UID 7\r\n"))
	if _, err := p.readResponse(); err == nil {
		t.Fatal("expected error for unbalanced list")
	}
}
