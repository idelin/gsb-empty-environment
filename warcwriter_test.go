package warcwriter

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/base32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	base := time.Date(2026, 11, 11, 8, 30, 0, 0, time.UTC)
	n := 0
	var mu sync.Mutex
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := base.Add(time.Duration(n) * time.Second)
		n++
		return t
	}
}

func makeCapture(method, host, path, body string) Capture {
	request := "GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\nUser-Agent: test\r\n\r\n"
	if method != "GET" {
		request = method + " " + path + " HTTP/1.1\r\nHost: " + host + "\r\n\r\n" + body
	}
	response := "HTTP/1.1 200 OK\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\n\r\n" + body
	return Capture{
		Request:  []byte(request),
		Response: []byte(response),
		Metadata: map[string]string{"fetchTime": "100ms", "crawler": "test"},
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func countGzipMembers(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	count := 0
	next := gzipMembers(f)
	for {
		gz, err := next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("gzip member: %v", err)
		}
		if _, err := io.Copy(io.Discard, gz); err != nil {
			t.Fatalf("drain member: %v", err)
		}
		count++
	}
	return count
}

func TestWriteAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Options{Dir: dir, Prefix: "crawl", Now: fixedClock()})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	cap1 := makeCapture("GET", "example.com", "/a", "hello world payload")
	res1, err := w.WriteCapture(cap1)
	if err != nil {
		t.Fatalf("WriteCapture 1: %v", err)
	}
	if res1.IsRevisit {
		t.Fatal("first capture must not be a revisit")
	}

	// Same payload body, different URL -> revisit.
	cap2 := makeCapture("GET", "example.org", "/b", "hello world payload")
	res2, err := w.WriteCapture(cap2)
	if err != nil {
		t.Fatalf("WriteCapture 2: %v", err)
	}
	if !res2.IsRevisit {
		t.Fatal("duplicate payload must be rewritten as revisit")
	}

	// Different payload -> fresh response record.
	cap3 := makeCapture("GET", "example.net", "/c", "a totally different body")
	res3, err := w.WriteCapture(cap3)
	if err != nil {
		t.Fatalf("WriteCapture 3: %v", err)
	}
	if res3.IsRevisit {
		t.Fatal("unique payload must not be a revisit")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Each capture writes request + response/revisit + metadata, plus one
	// warcinfo: 1 + 3*3 = 10 independent gzip members.
	warcFile := filepath.Join(dir, res1.WARCFile)
	if got := countGzipMembers(t, warcFile); got != 10 {
		t.Fatalf("gzip members = %d, want 10", got)
	}

	entries, err := ReadCDXJ(filepath.Join(dir, "crawl.cdxj"))
	if err != nil {
		t.Fatalf("ReadCDXJ: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("cdxj entries = %d, want 3", len(entries))
	}
	if !entries[1].Revisit || entries[1].RefersTo != res1.ResponseID {
		t.Fatalf("second entry must revisit first response: %+v", entries[1])
	}

	reader := NewReader(dir)
	defer reader.Close()

	respRec, err := reader.Open(entries[0])
	if err != nil {
		t.Fatalf("Open response: %v", err)
	}
	if respRec.Type != TypeResponse || respRec.PayloadDigest != res1.PayloadKey {
		t.Fatalf("unexpected response record: %+v", respRec)
	}
	if !bytes.Equal(respRec.Body, cap1.Response) {
		t.Fatal("response body mismatch")
	}

	revisitRec, err := reader.Open(entries[1])
	if err != nil {
		t.Fatalf("Open revisit: %v", err)
	}
	if revisitRec.Type != TypeRevisit {
		t.Fatalf("type = %s, want revisit", revisitRec.Type)
	}
	if revisitRec.Profile != RevisitProfileIdenticalPayload {
		t.Fatalf("profile = %s", revisitRec.Profile)
	}
	if revisitRec.RefersTo != res1.ResponseID {
		t.Fatalf("RefersTo = %s, want %s", revisitRec.RefersTo, res1.ResponseID)
	}
	if !strings.HasPrefix(string(revisitRec.Body), "HTTP/1.1 200 OK") {
		t.Fatal("revisit block must retain the HTTP status line and headers")
	}
	if bytes.Contains(revisitRec.Body, []byte("hello world payload")) {
		t.Fatal("revisit block must not contain the duplicated payload")
	}

	// The request and metadata records are linked via WARC-Concurrent-To.
	allRecords := readAllRecords(t, warcFile)
	var requestCount, metadataCount int
	for _, rec := range allRecords {
		switch rec.Type {
		case TypeRequest:
			requestCount++
		case TypeMetadata:
			metadataCount++
			if rec.ConcurrentTo == "" {
				t.Fatal("metadata record missing WARC-Concurrent-To")
			}
		case TypeResponse, TypeRevisit:
			if rec.ConcurrentTo == "" {
				t.Fatalf("%s record missing WARC-Concurrent-To", rec.Type)
			}
		}
	}
	if requestCount != 3 || metadataCount != 3 {
		t.Fatalf("requests=%d metadata=%d, want 3/3", requestCount, metadataCount)
	}
}

func TestRollingBySize(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Options{
		Dir:          dir,
		Prefix:       "roll",
		MaxFileBytes: 1, // forces a roll before every capture
		Now:          fixedClock(),
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := w.WriteCapture(makeCapture("GET", "example.com", "/"+itoa(i), "body-"+itoa(i))); err != nil {
			t.Fatalf("WriteCapture %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "roll-*.warc.gz"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("rolled files = %d, want 3", len(matches))
	}

	entries, err := ReadCDXJ(filepath.Join(dir, "roll.cdxj"))
	if err != nil {
		t.Fatalf("ReadCDXJ: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Filename] = true
	}
	if len(seen) != 3 {
		t.Fatalf("index references %d files, want 3", len(seen))
	}

	// Random reads work across rolled files.
	reader := NewReader(dir)
	defer reader.Close()
	for _, entry := range entries {
		rec, err := reader.Open(entry)
		if err != nil {
			t.Fatalf("Open %s offset %d: %v", entry.Filename, entry.Offset, err)
		}
		if rec.RecordID != entry.ID {
			t.Fatalf("record id %s != index %s", rec.RecordID, entry.ID)
		}
	}
}

func TestReopenWriterKeepsDedup(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Dir: dir, Prefix: "reopen", Now: fixedClock()}

	w1, err := NewWriter(opts)
	if err != nil {
		t.Fatalf("NewWriter 1: %v", err)
	}
	first, err := w1.WriteCapture(makeCapture("GET", "a.example", "/1", "shared body bytes"))
	if err != nil {
		t.Fatalf("WriteCapture: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	w2, err := NewWriter(opts)
	if err != nil {
		t.Fatalf("NewWriter 2: %v", err)
	}
	second, err := w2.WriteCapture(makeCapture("GET", "b.example", "/2", "shared body bytes"))
	if err != nil {
		t.Fatalf("WriteCapture duplicate: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}

	if !second.IsRevisit || second.ResponseID == first.ResponseID {
		t.Fatal("reopened writer must revisit the earlier payload with a new record id")
	}
}

func TestTamperedRecordFailsDigestCheck(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Options{Dir: dir, Prefix: "tamper", Now: fixedClock()})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.WriteCapture(makeCapture("GET", "example.com", "/x", "original body")); err != nil {
		t.Fatalf("WriteCapture: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := ReadCDXJ(filepath.Join(dir, "tamper.cdxj"))
	if err != nil {
		t.Fatalf("ReadCDXJ: %v", err)
	}

	// Corrupt the indexed gzip member in place by flipping a byte in its
	// inflated payload and recompressing only that member. Recompute the
	// member bounds because compression may change its size.
	warcPath := filepath.Join(dir, entries[0].Filename)
	corrupted, newLength := corruptIndexedMember(t, warcPath, entries[0].Offset)
	if err := os.WriteFile(warcPath, corrupted, 0o644); err != nil {
		t.Fatalf("rewrite corrupted warc: %v", err)
	}
	entries[0].Length = newLength

	reader := NewReader(dir)
	defer reader.Close()
	if _, err := reader.Open(entries[0]); err == nil {
		t.Fatal("expected digest verification error for tampered record")
	} else if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("error = %v, want digest mismatch", err)
	}
}

func TestLabeledSHA1Vector(t *testing.T) {
	sum := sha1.Sum(nil)
	want := "sha1:" + strings.TrimRight(base32.StdEncoding.EncodeToString(sum[:]), "=")
	if got := LabeledSHA1(nil); got != want {
		t.Fatalf("LabeledSHA1(nil) = %s, want %s", got, want)
	}
}

func TestConcurrentWritesAndEmptyBody(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Options{Dir: dir, Prefix: "conc", Now: fixedClock()})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			body := "concurrent-body-" + strconv.Itoa(g)
			cap := makeCapture("GET", "example.com", "/g/"+strconv.Itoa(g), body)
			if _, err := w.WriteCapture(cap); err != nil {
				errs <- err
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := ReadCDXJ(filepath.Join(dir, "conc.cdxj"))
	if err != nil {
		t.Fatalf("ReadCDXJ: %v", err)
	}
	if len(entries) != goroutines {
		t.Fatalf("entries = %d, want %d", len(entries), goroutines)
	}

	reader := NewReader(dir)
	defer reader.Close()
	for _, entry := range entries {
		if _, err := reader.Open(entry); err != nil {
			t.Fatalf("Open %s: %v", entry.URI, err)
		}
	}
}

func TestChunkedTransferEncoding(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Options{Dir: dir, Prefix: "chunk", Now: fixedClock()})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	request := []byte("GET /stream HTTP/1.1\r\nHost: example.com\r\n\r\n")
	// Wire response uses chunked transfer encoding.
	response := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n" +
		"7\r\n world!\r\n" +
		"0\r\n\r\n")
	cap := Capture{Request: request, Response: response}
	if _, err := w.WriteCapture(cap); err != nil {
		t.Fatalf("WriteCapture: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := ReadCDXJ(filepath.Join(dir, "chunk.cdxj"))
	if err != nil {
		t.Fatalf("ReadCDXJ: %v", err)
	}
	reader := NewReader(dir)
	defer reader.Close()
	rec, err := reader.Open(entries[0])
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if rec.Type != TypeResponse {
		t.Fatalf("type = %s", rec.Type)
	}
	// Payload digest must match the decoded entity, not the raw chunk framing.
	payload, err := recordPayload(rec)
	if err != nil {
		t.Fatalf("recordPayload: %v", err)
	}
	if string(payload) != "hello world!" {
		t.Fatalf("decoded payload = %q, want %q", string(payload), "hello world!")
	}
}

func TestEmptyPayloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Options{Dir: dir, Prefix: "empty", Now: fixedClock()})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	cap1 := makeCapture("GET", "example.com", "/empty", "")
	if _, err := w.WriteCapture(cap1); err != nil {
		t.Fatalf("WriteCapture 1: %v", err)
	}
	// Two captures with identical empty payloads: second is a revisit.
	cap2 := makeCapture("GET", "example.org", "/empty-too", "")
	res2, err := w.WriteCapture(cap2)
	if err != nil {
		t.Fatalf("WriteCapture 2: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if res2.IsRevisit != true {
		t.Fatalf("empty-payload duplicate revisit = %v, want true", res2.IsRevisit)
	}

	entries, err := ReadCDXJ(filepath.Join(dir, "empty.cdxj"))
	if err != nil {
		t.Fatalf("ReadCDXJ: %v", err)
	}
	reader := NewReader(dir)
	defer reader.Close()
	for _, entry := range entries {
		if _, err := reader.Open(entry); err != nil {
			t.Fatalf("Open: %v", err)
		}
	}
}

func readAllRecords(t *testing.T, path string) []*Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var out []*Record
	r := bytes.NewReader(data)
	next := gzipMembers(r)
	for {
		gz, err := next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("gzip member: %v", err)
		}
		block, readErr := io.ReadAll(gz)
		if readErr != nil {
			t.Fatalf("read member: %v", readErr)
		}
		rec, parseErr := parseRecord(block)
		if parseErr != nil {
			t.Fatalf("parseRecord: %v", parseErr)
		}
		out = append(out, rec)
	}
	return out
}

func corruptIndexedMember(t *testing.T, path string, offset int64) ([]byte, int64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	region := data[offset:]
	r := bytes.NewReader(region)
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	gz.Multistream(false)
	block, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("inflate: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	idx := bytes.Index(block, []byte("original body"))
	if idx < 0 {
		t.Fatal("payload marker not found")
	}
	block[idx] = 'X'

	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(block); err != nil {
		t.Fatalf("recompress: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	consumed := int64(len(region) - r.Len())
	memberEnd := offset + consumed
	result := append([]byte(nil), data[:offset]...)
	result = append(result, out.Bytes()...)
	result = append(result, data[memberEnd:]...)
	return result, int64(out.Len())
}
