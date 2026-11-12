package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func readFiltered(t *testing.T, n *Normalizer, req *Request, raw string) (string, error) {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(raw))
	out, err := n.ReadResponse(br, req)
	if err != nil {
		return "", err
	}
	if _, err := br.Peek(1); err == nil {
		t.Fatal("response reader did not consume the whole response")
	}
	return string(out), nil
}

func TestMultigetDropsForeignTenantEntries(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, err := n.NormalizeText([]byte("get alpha beta\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw := "VALUE t1:alpha 0 1\r\na\r\n" +
		"VALUE t2:secret 0 6\r\nsecret\r\n" +
		"VALUE t1:beta 7 2 9\r\nhi\r\n" +
		"END\r\n"
	got, err := readFiltered(t, n, req, raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "VALUE alpha 0 1\r\na\r\nVALUE beta 7 2 9\r\nhi\r\nEND\r\n"
	if got != want {
		t.Fatalf("filtered response:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "secret") {
		t.Fatal("foreign tenant value leaked")
	}
}

func TestMultigetAllForeignStillEnds(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("get nowhere\r\n"))
	raw := "VALUE t2:secret 0 6\r\nsecret\r\nEND\r\n"
	got, err := readFiltered(t, n, req, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "END\r\n" {
		t.Fatalf("filtered response = %q", got)
	}
}

func TestValueResponseLengthMustMatch(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("get alpha\r\n"))
	_, err := readFiltered(t, n, req, "VALUE t1:alpha 0 2\r\nabc\r\nEND\r\n")
	if !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("bad VALUE length error = %v", err)
	}
}

func TestStatsItemKeysAreFiltered(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("stats cachedump 1 10\r\n"))
	raw := "STAT items:1:number 1\r\n" +
		"ITEM t2:secret [2 b; 0 s]\r\n" +
		"ITEM t1:visible [3 b; 0 s]\r\n" +
		"END\r\n"
	got, err := readFiltered(t, n, req, raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "STAT items:1:number 1\r\nITEM visible [3 b; 0 s]\r\nEND\r\n"
	if got != want {
		t.Fatalf("filtered stats:\n got %q\nwant %q", got, want)
	}
}

func TestSingleResponsePassThrough(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("set k 0 0 1\r\nx\r\n"))
	got, err := readFiltered(t, n, req, "STORED\r\n")
	if err != nil || got != "STORED\r\n" {
		t.Fatalf("single response = %q, %v", got, err)
	}
}

func TestNoreplyResponseIsNotRead(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("delete k noreply\r\n"))
	br := bufio.NewReader(bytes.NewReader(nil))
	out, err := n.ReadResponse(br, req)
	if err != nil || len(out) != 0 {
		t.Fatalf("noreply response = %q, %v", out, err)
	}
}

func TestErrorResponsesPassThrough(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("get alpha\r\n"))
	got, err := readFiltered(t, n, req, "SERVER_ERROR out of memory\r\n")
	if err != nil || got != "SERVER_ERROR out of memory\r\n" {
		t.Fatalf("error response = %q, %v", got, err)
	}
}

func TestUnsolicitedValueOnSingleResponseIsRejected(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("set key 0 0 1\r\nx\r\n"))
	_, err := readFiltered(t, n, req, "VALUE t1:other 0 1\r\ny\r\n")
	if !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("unsolicited VALUE error = %v", err)
	}
}

func TestMultigetRequiresEnd(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	req, _ := n.NormalizeText([]byte("get alpha\r\n"))
	_, err := readFiltered(t, n, req, "VALUE t1:alpha 0 1\r\na\r\n")
	if !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("missing END error = %v", err)
	}
}
