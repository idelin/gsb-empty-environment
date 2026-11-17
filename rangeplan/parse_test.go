package rangeplan

import (
	"errors"
	"math"
	"strconv"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := map[string][]Spec{
		"bytes=0-0":   {{Start: 0, End: 0}},
		"bytes=0-499": {{Start: 0, End: 499}},
		"bytes=500-":  {{Start: 500, OpenEnd: true}},
		"bytes=-500":  {{End: 500, Suffix: true}},
		"  bytes=0-1 ,2-3, 4- , -9 ": {
			{Start: 0, End: 1},
			{Start: 2, End: 3},
			{Start: 4, OpenEnd: true},
			{End: 9, Suffix: true},
		},
		"BYTES=0-1": {{Start: 0, End: 1}},
	}
	for header, want := range cases {
		got, err := Parse(header)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", header, err)
		}
		if len(got) != len(want) {
			t.Fatalf("Parse(%q) got %d specs, want %d", header, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Parse(%q)[%d] = %+v, want %+v", header, i, got[i], want[i])
			}
		}
	}
}

func TestParseMalformed(t *testing.T) {
	headers := []string{
		"",
		"bytes",
		"bytes=",
		"bytes= ",
		"items=0-1",
		"bytes=0",
		"bytes=-",
		"bytes=--5",
		"bytes=0-1-",
		"bytes=1-0",
		"bytes=a-b",
		"bytes=0x1-2",
		"bytes= 0 -1",
		"bytes=0-1,",
		"bytes=-1,-2,",
		"bytes=+0-1",
		"bytes=0--1",
	}
	for _, header := range headers {
		if _, err := Parse(header); !errors.Is(err, ErrMalformedRange) {
			t.Errorf("Parse(%q) err = %v, want ErrMalformedRange", header, err)
		}
	}
}

func TestParseOverflow(t *testing.T) {
	headers := []string{
		"bytes=9223372036854775808-",   // max int64 + 1
		"bytes=0-99999999999999999999", // 20 digits
		"bytes=-9223372036854775808",   // suffix overflow
		"bytes=99999999999999999999-999999999999999999999",
	}
	for _, header := range headers {
		if _, err := Parse(header); !errors.Is(err, ErrOverflow) {
			t.Errorf("Parse(%q) err = %v, want ErrOverflow", header, err)
		}
	}
}

func TestParseMaxInt64Accepted(t *testing.T) {
	specs, err := Parse("bytes=" + strconv.FormatInt(math.MaxInt64, 10) + "-")
	if err != nil {
		t.Fatalf("max int64 range rejected: %v", err)
	}
	if specs[0].Start != math.MaxInt64 || !specs[0].IsOpenEnded() {
		t.Fatalf("unexpected spec: %+v", specs[0])
	}
}
