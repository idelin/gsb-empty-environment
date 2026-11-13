package netsec

import (
	"errors"
	"testing"
)

// headerDefinite is the COSE protected header {1: -7, 4: "kid-1"}
// (alg ES256, key id) as a definite-length map.
var headerDefinite = []byte{
	0xa2,       // map(2)
	0x01, 0x26, // 1: -7
	0x04, 0x65, 'k', 'i', 'd', '-', '1', // 4: "kid-1"
}

// headerIndefinite is the same header as an indefinite-length map.
var headerIndefinite = []byte{
	0xbf, // map(*)
	0x01, 0x26,
	0x04, 0x65, 'k', 'i', 'd', '-', '1',
	0xff, // break
}

func TestAdjudicateDefiniteMapEntersVerification(t *testing.T) {
	m, err := AdjudicateProtectedHeader(headerDefinite)
	if err != nil {
		t.Fatalf("definite-length map rejected: %v", err)
	}
	if got := m[uint64(1)]; got != int64(-7) {
		t.Errorf("alg = %v, want -7", got)
	}
	if got := m[uint64(4)]; got != "kid-1" {
		t.Errorf("kid = %v, want kid-1", got)
	}
}

func TestAdjudicateIndefiniteMapSameHeaderFails(t *testing.T) {
	if _, err := AdjudicateProtectedHeader(headerIndefinite); !errors.Is(err, ErrIndefiniteLength) {
		t.Fatalf("indefinite-length map: err = %v, want ErrIndefiniteLength", err)
	}
}

func TestAdjudicateRejections(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want error
	}{
		"indefinite array":       {[]byte{0x9f, 0x01, 0xff}, ErrIndefiniteLength},
		"indefinite byte string": {[]byte{0x5f, 0x40, 0xff}, ErrIndefiniteLength},
		"indefinite text string": {[]byte{0x7f, 0x60, 0xff}, ErrIndefiniteLength},
		"nested indefinite map":  {[]byte{0xa1, 0x01, 0xbf, 0xff}, ErrIndefiniteLength},
		"float16":                {[]byte{0xa1, 0x01, 0xf9, 0x3c, 0x00}, ErrFloat},
		"float32":                {[]byte{0xa1, 0x01, 0xfa, 0x3f, 0x80, 0x00, 0x00}, ErrFloat},
		"float64":                {[]byte{0xa1, 0x01, 0xfb, 0, 0, 0, 0, 0, 0, 0, 0}, ErrFloat},
		"undefined":              {[]byte{0xa1, 0x01, 0xf7}, ErrUndefined},
		"undefined bare":         {[]byte{0xf7}, ErrUndefined},
		"duplicate key":          {[]byte{0xa2, 0x01, 0x01, 0x01, 0x02}, ErrDuplicateKey},
		"trailing data":          {[]byte{0xa0, 0x00}, ErrTrailingData},
		"unexpected break":       {[]byte{0xff}, ErrUnexpectedBreak},
		"truncated":              {[]byte{0xa2, 0x01}, ErrUnexpectedEOF},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := AdjudicateProtectedHeader(tc.data); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAdjudicateNonMapFails(t *testing.T) {
	for _, data := range [][]byte{
		{0x01},       // uint
		{0x81, 0x01}, // array
		{0x41, 0x00}, // byte string
		{0x61, 'a'},  // text string
		{0xc0, 0x00}, // tag
	} {
		if _, err := AdjudicateProtectedHeader(data); !errors.Is(err, ErrNotMap) {
			t.Fatalf("%x: err = %v, want ErrNotMap", data, err)
		}
	}
}

func TestDecodeDefiniteValues(t *testing.T) {
	// {1: -7, 4: "kid-1", 5: [true, null, h'00'], 6: 32(0)}
	data := []byte{
		0xa4,
		0x01, 0x26,
		0x04, 0x65, 'k', 'i', 'd', '-', '1',
		0x05, 0x83, 0xf5, 0xf6, 0x41, 0x00,
		0x06, 0xd8, 0x20, 0x00,
	}
	v, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	m := v.(map[any]any)
	arr := m[uint64(5)].([]any)
	if arr[0] != true || arr[1] != nil {
		t.Errorf("array = %v", arr)
	}
	if b := arr[2].([]byte); len(b) != 1 || b[0] != 0 {
		t.Errorf("byte string = %v", b)
	}
	if tag := m[uint64(6)].(Tag); tag.Number != 32 || tag.Content != uint64(0) {
		t.Errorf("tag = %v", tag)
	}
}
