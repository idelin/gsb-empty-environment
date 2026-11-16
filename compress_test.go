package netsec

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"
)

type token struct {
	literal  bool
	value    byte
	distance int
	length   int
}

func parseTokens(t *testing.T, encoded []byte) []token {
	t.Helper()
	var tokens []token
	for pos := 0; pos < len(encoded); {
		switch encoded[pos] {
		case 0:
			if len(encoded)-pos < 2 {
				t.Fatalf("truncated literal at %d", pos)
			}
			tokens = append(tokens, token{literal: true, value: encoded[pos+1]})
			pos += 2
		case 1:
			if len(encoded)-pos < 4 {
				t.Fatalf("truncated match at %d", pos)
			}
			tokens = append(tokens, token{
				distance: int(encoded[pos+1])<<8 | int(encoded[pos+2]),
				length:   int(encoded[pos+3]),
			})
			pos += 4
		default:
			t.Fatalf("unknown tag %d at %d", encoded[pos], pos)
		}
	}
	return tokens
}

func TestCompressExactTokens(t *testing.T) {
	tests := []struct {
		name       string
		dictionary []byte
		input      []byte
		want       []byte
	}{
		{
			name:  "literal then longest match",
			input: []byte("abcabcabc"),
			want:  []byte{0, 'a', 0, 'b', 0, 'c', 1, 0, 3, 6},
		},
		{
			name:  "overlapping match",
			input: []byte("aaaaaaaa"),
			want:  []byte{0, 'a', 1, 0, 1, 7},
		},
		{
			name:       "overlap across dictionary boundary",
			dictionary: []byte("a"),
			input:      []byte("aaaa"),
			want:       []byte{1, 0, 1, 4},
		},
		{
			name:       "dictionary suffix",
			dictionary: []byte("SECRET"),
			input:      []byte("SECRET"),
			want:       []byte{1, 0, 6, 6},
		},
		{
			name:  "empty input",
			input: nil,
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compress(tc.dictionary, tc.input)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("Compress() = % x, want % x", got, tc.want)
			}
			decoded, err := Decompress(tc.dictionary, got)
			if err != nil {
				t.Fatalf("Decompress() error: %v", err)
			}
			if !bytes.Equal(decoded, tc.input) {
				t.Fatalf("Decompress() = %q, want %q", decoded, tc.input)
			}
		})
	}
}

func TestMatchTieUsesSmallestDistance(t *testing.T) {
	input := []byte("abcXabcYabc")
	encoded := Compress(nil, input)
	tokens := parseTokens(t, encoded)
	last := tokens[len(tokens)-1]
	if last.literal || last.distance != 4 || last.length != 3 {
		t.Fatalf("last token = %+v, want match distance 4 length 3", last)
	}
}

func TestMatchPrefersLongestBeforeDistance(t *testing.T) {
	input := []byte("abcdXabcYabcd")
	encoded := Compress(nil, input)
	tokens := parseTokens(t, encoded)
	last := tokens[len(tokens)-1]
	if last.literal || last.distance != 9 || last.length != 4 {
		t.Fatalf("last token = %+v, want longest match distance 9 length 4", last)
	}
}

func TestMatchLengthCappedAt255(t *testing.T) {
	input := bytes.Repeat([]byte{'a'}, 400)
	tokens := parseTokens(t, Compress(nil, input))
	if len(tokens) != 3 {
		t.Fatalf("got %d tokens, want 3: %+v", len(tokens), tokens)
	}
	if tokens[1].literal || tokens[1].length != 255 || tokens[1].distance != 1 {
		t.Fatalf("second token = %+v, want distance 1 length 255", tokens[1])
	}
	if tokens[2].literal || tokens[2].length != 144 {
		t.Fatalf("third token = %+v, want match length 144", tokens[2])
	}
}

func TestWindowSlidingBoundary(t *testing.T) {
	const marker = "WXYZQ"
	filler := func(n int) []byte {
		out := make([]byte, n)
		for i := range out {
			out[i] = byte(i % 17)
		}
		return out
	}

	t.Run("distance 4096 is available", func(t *testing.T) {
		input := append([]byte(marker), filler(WindowSize-len(marker))...)
		input = append(input, marker...)
		if len(input) != WindowSize+len(marker) {
			t.Fatalf("test input length = %d", len(input))
		}
		tokens := parseTokens(t, Compress(nil, input))
		last := tokens[len(tokens)-1]
		if last.literal || last.distance != WindowSize || last.length != len(marker) {
			t.Fatalf("last token = %+v, want distance %d length %d", last, WindowSize, len(marker))
		}
	})

	t.Run("distance 4097 is outside the window", func(t *testing.T) {
		input := append([]byte(marker), filler(WindowSize+1-len(marker))...)
		input = append(input, marker...)
		tokens := parseTokens(t, Compress(nil, input))
		tail := tokens[len(tokens)-len(marker):]
		for i, tok := range tail {
			if !tok.literal || tok.value != marker[i] {
				t.Fatalf("tail token %d = %+v, want literal %q", i, tok, marker[i])
			}
		}
	})
}

func TestRoundTripRandomInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		dictionary := make([]byte, rng.Intn(WindowSize+50))
		input := make([]byte, rng.Intn(10000))
		for j := range dictionary {
			dictionary[j] = byte(rng.Intn(8))
		}
		for j := range input {
			input[j] = byte(rng.Intn(8))
		}
		encoded := Compress(dictionary, input)
		decoded, err := Decompress(dictionary, encoded)
		if err != nil {
			t.Fatalf("case %d: Decompress() error: %v", i, err)
		}
		if !bytes.Equal(decoded, input) {
			t.Fatalf("case %d: round trip mismatch", i)
		}
	}
}

func TestDecompressRejectsMalformedInput(t *testing.T) {
	tests := [][]byte{
		{0},
		{1, 0},
		{9},
		{1, 0, 0, 3},
		{1, 0, 1, 2},
		{1, 0, 5, 3},
	}
	for _, encoded := range tests {
		if _, err := Decompress(nil, encoded); !errors.Is(err, ErrInvalidEncoding) {
			t.Fatalf("Decompress(% x) error = %v, want ErrInvalidEncoding", encoded, err)
		}
	}
}

func TestCompressContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompressContext(ctx, nil, []byte("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompressContext() error = %v, want context.Canceled", err)
	}
}
