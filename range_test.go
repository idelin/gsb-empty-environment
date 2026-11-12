package netsec

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		header string
		want   []Spec
	}{
		{"bytes=0-99", []Spec{{Kind: Closed, Start: 0, End: 99}}},
		{"bytes=500-", []Spec{{Kind: OpenTail, Start: 500}}},
		{"bytes=-500", []Spec{{Kind: Suffix, Suffix: 500}}},
		{"bytes=0-0,-1", []Spec{{Kind: Closed, Start: 0, End: 0}, {Kind: Suffix, Suffix: 1}}},
		{"bytes= 0-99 , -200 , 500-", []Spec{
			{Kind: Closed, Start: 0, End: 99},
			{Kind: Suffix, Suffix: 200},
			{Kind: OpenTail, Start: 500},
		}},
		{"Bytes=10-20", []Spec{{Kind: Closed, Start: 10, End: 20}}},
		{" BYTES =1-2", []Spec{{Kind: Closed, Start: 1, End: 2}}},
		{"bytes=9223372036854775807-", []Spec{{Kind: OpenTail, Start: math.MaxInt64}}},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			got, err := Parse(tc.header)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseMalformed(t *testing.T) {
	bad := []string{
		"",
		"bytes",
		"bytes=",
		"bytes=,",
		"bytes=10",
		"bytes=-",
		"bytes=-0",
		"bytes=10-5",
		"bytes=0-99,",
		"bytes=0--9",
		"bytes=0x-9",
		"bytes=-1-",
		"bytes= 1 2-3",
		"bytes=9223372036854775808-",
		"bytes=18446744073709551616-18446744073709551617",
		"bytes=+1-2",
	}
	for _, header := range bad {
		t.Run(header, func(t *testing.T) {
			if _, err := Parse(header); !errors.Is(err, ErrMalformed) {
				t.Fatalf("got %v, want ErrMalformed", err)
			}
		})
	}
}

func TestParseUnsupportedUnit(t *testing.T) {
	if _, err := Parse("items=0-9"); !errors.Is(err, ErrUnsupportedUnit) {
		t.Fatalf("got %v, want ErrUnsupportedUnit", err)
	}
}

func TestConstructorsRejectInvalid(t *testing.T) {
	if _, err := NewClosed(-1, 2); !errors.Is(err, ErrMalformed) {
		t.Fatalf("NewClosed negative start: %v", err)
	}
	if _, err := NewClosed(5, 2); !errors.Is(err, ErrMalformed) {
		t.Fatalf("NewClosed inverted: %v", err)
	}
	if _, err := NewOpenTail(-1); !errors.Is(err, ErrMalformed) {
		t.Fatalf("NewOpenTail negative: %v", err)
	}
	if _, err := NewSuffix(0); !errors.Is(err, ErrMalformed) {
		t.Fatalf("NewSuffix zero: %v", err)
	}
	if _, err := NewSuffix(-3); !errors.Is(err, ErrMalformed) {
		t.Fatalf("NewSuffix negative: %v", err)
	}

	closed, err := NewClosed(2, 4)
	if err != nil || closed != (Spec{Kind: Closed, Start: 2, End: 4}) {
		t.Fatalf("NewClosed happy path: %+v %v", closed, err)
	}
	open, err := NewOpenTail(7)
	if err != nil || open != (Spec{Kind: OpenTail, Start: 7}) {
		t.Fatalf("NewOpenTail happy path: %+v %v", open, err)
	}
	suffix, err := NewSuffix(9)
	if err != nil || suffix != (Spec{Kind: Suffix, Suffix: 9}) {
		t.Fatalf("NewSuffix happy path: %+v %v", suffix, err)
	}
	if (Spec{Kind: 99}).valid() {
		t.Fatalf("unknown kind must be invalid")
	}
}

func TestUnsatisfiableErrorMessage(t *testing.T) {
	_, err := (Spec{Kind: Closed, Start: 5, End: 9}).Normalize(3)
	var unsatisfiable *UnsatisfiableError
	if !errors.As(err, &unsatisfiable) {
		t.Fatalf("want UnsatisfiableError, got %v", err)
	}
	if unsatisfiable.Error() == "" || unsatisfiable.Size != 3 {
		t.Fatalf("unexpected error %q", unsatisfiable.Error())
	}
}

func TestSpecString(t *testing.T) {
	cases := map[Spec]string{
		{Kind: Closed, Start: 1, End: 2}: "1-2",
		{Kind: OpenTail, Start: 7}:       "7-",
		{Kind: Suffix, Suffix: 9}:        "-9",
	}
	for spec, want := range cases {
		if got := spec.String(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	const size int64 = 100
	cases := []struct {
		spec Spec
		want Segment
	}{
		{Spec{Kind: Closed, Start: 0, End: 49}, Segment{0, 49}},
		{Spec{Kind: Closed, Start: 90, End: 500}, Segment{90, 99}},
		{Spec{Kind: OpenTail, Start: 80}, Segment{80, 99}},
		{Spec{Kind: Suffix, Suffix: 10}, Segment{90, 99}},
		{Spec{Kind: Suffix, Suffix: 500}, Segment{0, 99}},
		{Spec{Kind: Closed, Start: 99, End: 99}, Segment{99, 99}},
	}
	for _, tc := range cases {
		got, err := tc.spec.Normalize(size)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.spec.String(), err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %+v, want %+v", tc.spec.String(), got, tc.want)
		}
	}
}

func TestNormalizeUnsatisfiable(t *testing.T) {
	const size int64 = 10
	bad := []Spec{
		{Kind: Closed, Start: 10, End: 20},
		{Kind: Closed, Start: 100, End: 200},
		{Kind: OpenTail, Start: 10},
		{Kind: Suffix, Suffix: 5},
	}
	for index, spec := range bad {
		err := error(nil)
		switch spec.Kind {
		case Suffix:
			_, err = spec.Normalize(0)
		default:
			_, err = spec.Normalize(size)
		}
		var unsatisfiable *UnsatisfiableError
		if !errors.As(err, &unsatisfiable) {
			t.Fatalf("case %d: got %v, want UnsatisfiableError", index, err)
		}
		if !errors.Is(err, ErrUnsatisfiable) {
			t.Fatalf("case %d: error does not wrap ErrUnsatisfiable", index)
		}
	}
}

func TestNormalizeEmptyObject(t *testing.T) {
	all := []Spec{
		{Kind: Closed, Start: 0, End: 0},
		{Kind: OpenTail, Start: 0},
		{Kind: Suffix, Suffix: 1},
	}
	for _, spec := range all {
		if _, err := spec.Normalize(0); !errors.Is(err, ErrUnsatisfiable) {
			t.Fatalf("%s on empty object: got %v", spec.String(), err)
		}
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	if _, err := (Spec{Kind: Closed, Start: 5, End: 2}).Normalize(10); !errors.Is(err, ErrMalformed) {
		t.Fatalf("invalid spec not rejected")
	}
	if _, err := (Spec{Kind: Closed, Start: 0, End: 1}).Normalize(-1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("negative size not rejected")
	}
}

func TestSegmentHelpers(t *testing.T) {
	segment := Segment{First: 10, Last: 19}
	if segment.Length() != 10 {
		t.Fatalf("length = %d", segment.Length())
	}
	if !segment.Contains(10) || !segment.Contains(19) || segment.Contains(20) || segment.Contains(9) {
		t.Fatalf("Contains wrong")
	}
	if got := segment.ContentRange(100); got != "bytes 10-19/100" {
		t.Fatalf("ContentRange = %q", got)
	}
}
