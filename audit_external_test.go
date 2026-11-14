package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.org/netsec/q0482"
)

func TestImportableServiceAPI(t *testing.T) {
	var out bytes.Buffer
	var id int
	enc, err := netsec.NewEncoder(
		&out,
		netsec.WithSubject("svc-42"),
		netsec.WithClock(func() time.Time {
			return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		}),
		netsec.WithEventIDGenerator(func() (string, error) {
			id++
			return fmt.Sprintf("evt-%d", id), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := enc.Write(map[string]string{
		"action":  "approve",
		"comment": "line one\nline two",
	}); err != nil {
		t.Fatal(err)
	}
	sealed, err := enc.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Records != 1 {
		t.Fatalf("sealed count = %d", sealed.Records)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("user newline escaped incorrectly: %q", out.String())
	}

	v, err := netsec.VerifySealed(bytes.NewReader(out.Bytes()), sealed)
	if err != nil {
		t.Fatalf("VerifySealed: %v", err)
	}
	if v.Records != 1 || v.Head != sealed.Head {
		t.Fatalf("verification = %+v", v)
	}

	if err := enc.Write(map[string]string{"action": "approve"}); !errors.Is(err, netsec.ErrClosed) {
		t.Fatalf("sealed encoder error = %v", err)
	}
}
