package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var testTime = time.Date(2026, 11, 14, 12, 34, 56, 123456789, time.UTC)

func testOptions(subject string) []Option {
	var n int
	return []Option{
		WithClock(func() time.Time { return testTime }),
		WithSubject(subject),
		WithEventIDGenerator(func() (string, error) {
			n++
			return fmt.Sprintf("evt-%04d", n), nil
		}),
	}
}

func newTestEncoder(t *testing.T, subject string, opts ...Option) (*Encoder, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	all := append(testOptions(subject), opts...)
	enc, err := NewEncoder(buf, all...)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	return enc, buf
}

func mustWrite(t *testing.T, enc *Encoder, fields map[string]string) Record {
	t.Helper()
	rec, err := enc.Emit(fields)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return rec
}

func parsedLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	trimmed := strings.TrimSuffix(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	raw := strings.Split(trimmed, "\n")
	out := make([]map[string]any, 0, len(raw))
	for i, line := range raw {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\n%s", i+1, err, line)
		}
		out = append(out, obj)
	}
	return out
}

func TestNewlineAndControlInjectionStaysInOneRecord(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")

	fakeRecord := `{"v":1,"seq":9,"timestamp":"1999-01-01T00:00:00Z","subject":"admin","event_id":"forged","fields":{"action":"approve"},"prev":"` + Genesis + `","hash":"` + strings.Repeat("0", 64) + `"}`
	comment := "looks harmless\n" + fakeRecord + "\r\napproved"
	controls := "nul:\x00 unit:\x1f nel:\u0085 ls:\u2028 ps:\u2029"
	delimiters := `"},"timestamp":"1999-01-01T00:00:00Z","subject":"root","event_id":"forged","x":"`

	if err := enc.Write(map[string]string{
		"action":     "approve",
		"comment":    comment,
		"controls":   controls,
		"delimiters": delimiters,
		"name\nrole": "admin",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data := buf.Bytes()
	if got := bytes.Count(data, []byte("\n")); got != 1 {
		t.Fatalf("record contains embedded newlines: found %d newline bytes", got)
	}
	if bytes.ContainsAny(data, "\x00\x1f\u0085\u2028\u2029") {
		t.Fatalf("raw control or line-separator character reached the log: %q", data)
	}
	line := strings.TrimSuffix(string(data), "\n")
	for _, escaped := range []string{`\n`, `\r`, `\u0000`, `\u001f`, `\u0085`, `\u2028`, `\u2029`, `\"`} {
		if !strings.Contains(line, escaped) {
			t.Errorf("canonical line does not contain escape %q\n%s", escaped, line)
		}
	}

	obj := parsedLines(t, data)[0]
	if obj["timestamp"] != testTime.Format(time.RFC3339Nano) {
		t.Fatalf("server timestamp was replaced: %v", obj["timestamp"])
	}
	if obj["subject"] != "svc-42" {
		t.Fatalf("server subject was replaced: %v", obj["subject"])
	}
	if obj["event_id"] != "evt-0001" {
		t.Fatalf("server event ID was replaced: %v", obj["event_id"])
	}
	fields := obj["fields"].(map[string]any)
	if fields["comment"] != comment {
		t.Fatalf("comment did not round-trip: %q", fields["comment"])
	}
	if fields["delimiters"] != delimiters {
		t.Fatalf("delimiter payload did not round-trip: %q", fields["delimiters"])
	}
	if fields["name\nrole"] != "admin" {
		t.Fatalf("field key with newline did not round-trip: %v", fields)
	}

	v, err := VerifyBytes(data)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if v.Records != 1 {
		t.Fatalf("verified record count = %d, want 1", v.Records)
	}
}

func TestServerOwnedEnvelopeCannotBeForgedByFieldNames(t *testing.T) {
	enc, buf := newTestEncoder(t, "trusted-service")
	fields := map[string]string{
		"timestamp": "1999-01-01T00:00:00Z",
		"subject":   "admin",
		"event_id":  "forged-event",
		"seq":       "999",
		"prev":      strings.Repeat("f", 64),
		"hash":      strings.Repeat("f", 64),
		"v":         "9",
		"fields":    `{"approval":"forged"}`,
	}
	rec := mustWrite(t, enc, fields)

	obj := parsedLines(t, buf.Bytes())[0]
	if obj["timestamp"] != testTime.Format(time.RFC3339Nano) {
		t.Fatalf("timestamp = %v, want server time", obj["timestamp"])
	}
	if obj["subject"] != "trusted-service" {
		t.Fatalf("subject = %v, want server subject", obj["subject"])
	}
	if obj["event_id"] != "evt-0001" {
		t.Fatalf("event_id = %v, want server ID", obj["event_id"])
	}
	if obj["seq"] != float64(1) || obj["v"] != float64(SchemaVersion) || obj["prev"] != Genesis {
		t.Fatalf("envelope was overwritten: %v", obj)
	}
	if obj["hash"] != rec.Hash {
		t.Fatalf("hash was overwritten: %v", obj["hash"])
	}
	nested := obj["fields"].(map[string]any)
	for key, want := range fields {
		if nested[key] != want {
			t.Errorf("nested field %q = %v, want %q", key, nested[key], want)
		}
	}
	if _, err := VerifyBytes(buf.Bytes()); err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}

	marshaled, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal(Record): %v", err)
	}
	line := strings.TrimSuffix(buf.String(), "\n")
	if string(marshaled) != line {
		t.Fatalf("Record JSON does not match wire format:\n got: %s\nwant: %s", marshaled, line)
	}
	var decoded Record
	if err := json.Unmarshal(marshaled, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(Record): %v", err)
	}
	if decoded.Hash != rec.Hash || decoded.Seq != rec.Seq || decoded.Fields["subject"] != "admin" {
		t.Fatalf("decoded record = %+v", decoded)
	}
}

func TestLegalLogVerifiesCountAndDigestChain(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	rec1 := mustWrite(t, enc, map[string]string{"action": "approve", "ticket": "T-1"})
	rec2 := mustWrite(t, enc, map[string]string{"action": "reject", "ticket": "T-2"})
	rec3 := mustWrite(t, enc, map[string]string{"action": "close", "ticket": "T-3"})

	if rec1.Prev != Genesis || rec2.Prev != rec1.Hash || rec3.Prev != rec2.Hash {
		t.Fatalf("prev links do not form a chain")
	}
	if enc.Count() != 3 || enc.Head() != rec3.Hash {
		t.Fatalf("encoder summary = (%d, %s)", enc.Count(), enc.Head())
	}

	v, err := VerifyBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if v.Records != 3 || v.Count != 3 || v.Head != rec3.Hash {
		t.Fatalf("verification = %+v", v)
	}
	if v.FirstSeq != 1 || v.LastSeq != 3 {
		t.Fatalf("sequence bounds = %d..%d", v.FirstSeq, v.LastSeq)
	}
	if v.FirstTimestamp != testTime.Format(time.RFC3339Nano) ||
		v.LastTimestamp != testTime.Format(time.RFC3339Nano) {
		t.Fatalf("timestamp bounds = %q..%q", v.FirstTimestamp, v.LastTimestamp)
	}

	encV, err := enc.Verify()
	if err != nil {
		t.Fatalf("Encoder.Verify: %v", err)
	}
	if encV.Digest() != (Digest{Records: 3, Head: rec3.Hash}) {
		t.Fatalf("Encoder.Verify digest = %+v", encV.Digest())
	}

	sealedV, err := enc.SealAndVerify()
	if err != nil {
		t.Fatalf("SealAndVerify: %v", err)
	}
	if sealedV.Records != 3 || sealedV.Head != rec3.Hash {
		t.Fatalf("SealAndVerify = %+v", sealedV)
	}
}

func TestModifiedTimestampAndFieldsAreRejected(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	mustWrite(t, enc, map[string]string{"action": "approve", "ticket": "T-1"})
	mustWrite(t, enc, map[string]string{"action": "approve", "ticket": "T-2"})
	original := append([]byte(nil), buf.Bytes()...)

	changedTime := bytes.Replace(
		original,
		[]byte(testTime.Format(time.RFC3339Nano)),
		[]byte("1999-01-01T00:00:00Z"),
		1,
	)
	if bytes.Equal(changedTime, original) {
		t.Fatal("timestamp replacement did not change the log")
	}
	if _, err := VerifyBytes(changedTime); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("forged timestamp error = %v, want ErrChainMismatch", err)
	}

	changedField := bytes.Replace(original, []byte(`"ticket":"T-1"`), []byte(`"ticket":"T-9"`), 1)
	if bytes.Equal(changedField, original) {
		t.Fatal("field replacement did not change the log")
	}
	if _, err := VerifyBytes(changedField); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("forged field error = %v, want ErrChainMismatch", err)
	}

	changedSubject := bytes.Replace(original, []byte(`"subject":"svc-42"`), []byte(`"subject":"admin"`), 1)
	if bytes.Equal(changedSubject, original) {
		t.Fatal("subject replacement did not change the log")
	}
	if _, err := VerifyBytes(changedSubject); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("forged subject error = %v, want ErrChainMismatch", err)
	}
}

func TestInjectedFakeApprovalRecordIsRejected(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	mustWrite(t, enc, map[string]string{"action": "view"})

	fake := `{"v":1,"seq":2,"timestamp":"2026-11-14T13:00:00Z","subject":"admin","event_id":"forged","fields":{"action":"approve"},"prev":"` + Genesis + `","hash":"` + strings.Repeat("0", 64) + `"}` + "\n"
	data := append(append([]byte(nil), buf.Bytes()...), []byte(fake)...)
	if _, err := VerifyBytes(data); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("injected record error = %v, want ErrChainMismatch", err)
	}

	if _, err := VerifyCount(bytes.NewReader(data), 1); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("count-verified injected log error = %v, want ErrChainMismatch", err)
	}
}

func TestSealedDigestCatchesTruncation(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	mustWrite(t, enc, map[string]string{"action": "one"})
	mustWrite(t, enc, map[string]string{"action": "two"})
	mustWrite(t, enc, map[string]string{"action": "three"})

	sealed, err := enc.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed.Records != 3 {
		t.Fatalf("sealed count = %d", sealed.Records)
	}
	if _, err := VerifySealed(bytes.NewReader(buf.Bytes()), sealed); err != nil {
		t.Fatalf("VerifySealed full log: %v", err)
	}

	lines := strings.SplitAfter(string(buf.Bytes()), "\n")
	truncated := []byte(strings.Join(lines[:2], ""))
	if _, err := VerifySealed(bytes.NewReader(truncated), sealed); !errors.Is(err, ErrCountMismatch) {
		t.Fatalf("truncated log error = %v, want ErrCountMismatch", err)
	}
	if _, err := VerifyCount(bytes.NewReader(truncated), 3); !errors.Is(err, ErrCountMismatch) {
		t.Fatalf("VerifyCount truncated log error = %v, want ErrCountMismatch", err)
	}

	if v, err := VerifyBytes(truncated); err != nil || v.Records != 2 {
		t.Fatalf("plain prefix should verify by itself: v=%+v err=%v", v, err)
	}
}

func TestKeyedChainRejectsRecomputedChain(t *testing.T) {
	key := []byte("server-side-audit-key")
	enc, buf := newTestEncoder(t, "svc-42", WithKey(key))
	mustWrite(t, enc, map[string]string{"action": "approve"})
	mustWrite(t, enc, map[string]string{"action": "close"})

	if _, err := VerifyBytes(buf.Bytes(), WithKey(key)); err != nil {
		t.Fatalf("VerifyBytes with key: %v", err)
	}
	if _, err := VerifyBytes(buf.Bytes()); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("unkeyed verification error = %v, want ErrChainMismatch", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	prev := Genesis
	var forged strings.Builder
	for i, line := range lines {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		rec.Timestamp = "1999-01-01T00:00:00Z"
		rec.Seq = uint64(i + 1)
		rec.Prev = prev
		payload := encodePayload(rec)
		sum := sha256.Sum256(payload)
		rec.Hash = hex.EncodeToString(sum[:])
		prev = rec.Hash
		forged.Write(appendHash(payload, rec.Hash))
		forged.WriteByte('\n')
	}
	if forged.String() == buf.String() {
		t.Fatal("attacker rewrite did not change the log")
	}
	if _, err := VerifyBytes([]byte(forged.String()), WithKey(key)); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("recomputed unkeyed chain error = %v, want ErrChainMismatch", err)
	}
}

func TestNonCanonicalRecordIsRejected(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	mustWrite(t, enc, map[string]string{"action": "approve"})
	line := strings.TrimSuffix(buf.String(), "\n")

	withSpace := strings.Replace(line, `"v":1`, `"v": 1`, 1)
	if _, err := VerifyBytes([]byte(withSpace + "\n")); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("whitespace record error = %v, want ErrInvalidRecord", err)
	}

	duplicate := strings.Replace(line, `"subject":"svc-42"`, `"subject":"admin","subject":"svc-42"`, 1)
	if _, err := VerifyBytes([]byte(duplicate + "\n")); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("duplicate-key record error = %v, want ErrInvalidRecord", err)
	}
}

func TestInvalidInputDoesNotPoisonEncoder(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	if err := enc.Write(map[string]string{"bad": string([]byte{0xff, 0xfe})}); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8 error = %v, want ErrInvalidUTF8", err)
	}
	if err := enc.Write(map[string]string{"action": "approve"}); err != nil {
		t.Fatalf("valid write after invalid input: %v", err)
	}
	if _, err := VerifyBytes(buf.Bytes()); err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
}

func TestSealRejectsFurtherWrites(t *testing.T) {
	enc, _ := newTestEncoder(t, "svc-42")
	mustWrite(t, enc, map[string]string{"action": "approve"})
	if _, err := enc.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := enc.Write(map[string]string{"action": "approve"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after Seal error = %v, want ErrClosed", err)
	}
}

func TestConcurrentWritesProduceValidChain(t *testing.T) {
	enc, buf := newTestEncoder(t, "svc-42")
	const writers = 32

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			if err := enc.Write(map[string]string{"worker": fmt.Sprint(i)}); err != nil {
				t.Errorf("Write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	v, err := VerifyBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if v.Records != writers || v.LastSeq != writers {
		t.Fatalf("verified %d records, last seq %d", v.Records, v.LastSeq)
	}
	if v.Head != enc.Head() {
		t.Fatalf("verified head %s, encoder head %s", v.Head, enc.Head())
	}
}
