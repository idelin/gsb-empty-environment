package delim

import (
	"math/rand"
	"reflect"
	"testing"
)

func fld(start, end int64, val string) Field {
	return Field{Start: start, End: end, Value: []byte(val)}
}

func fldDropped(start, end int64) Field {
	return Field{Start: start, End: end, Value: nil}
}

func rec(start, end int64, err *ParseError, fields ...Field) Record {
	return Record{Start: start, End: end, Fields: fields, Err: err}
}

var defaultCfg = Config{}

func TestParseCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		cfg   Config
		want  []Record
	}{
		{
			name:  "basic",
			input: "a,b,c\n1,2,3\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 6, nil, fld(0, 1, "a"), fld(2, 3, "b"), fld(4, 5, "c")),
				rec(6, 12, nil, fld(6, 7, "1"), fld(8, 9, "2"), fld(10, 11, "3")),
			},
		},
		{
			name:  "quoted newline",
			input: "a,\"x\ny\",c\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 10, nil, fld(0, 1, "a"), fld(2, 7, "x\ny"), fld(8, 9, "c")),
			},
		},
		{
			name:  "escaped quote",
			input: "\"a\"\"b\",c\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 9, nil, fld(0, 6, "a\"b"), fld(7, 8, "c")),
			},
		},
		{
			name:  "illegal quote isolates physical line",
			input: "ab\"cd,ef\ngh,ij\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 9, &ParseError{ErrIllegalQuote, 2, 3, 1}, fld(0, 2, "ab")),
				rec(9, 15, nil, fld(9, 11, "gh"), fld(12, 14, "ij")),
			},
		},
		{
			name:  "garbage after closing quote",
			input: "\"ab\"x,c\nnext\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 8, &ParseError{ErrIllegalQuote, 3, 4, 1}, fld(0, 4, "ab")),
				rec(8, 13, nil, fld(8, 12, "next")),
			},
		},
		{
			name:  "field too large unquoted",
			input: "abcdef,next\nok\n",
			cfg:   Config{MaxFieldSize: 4},
			want: []Record{
				rec(0, 12, &ParseError{ErrFieldTooLarge, 0, 5, 1}, fldDropped(0, 6), fld(7, 11, "next")),
				rec(12, 15, nil, fld(12, 14, "ok")),
			},
		},
		{
			name:  "field too large quoted with newline",
			input: "\"ab\ncdefgh\",x\ny,z\n",
			cfg:   Config{MaxFieldSize: 5},
			want: []Record{
				rec(0, 14, &ParseError{ErrFieldTooLarge, 0, 7, 2}, fldDropped(0, 11), fld(12, 13, "x")),
				rec(14, 18, nil, fld(14, 15, "y"), fld(16, 17, "z")),
			},
		},
		{
			name:  "unterminated quote at EOF",
			input: "\"abc",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 4, &ParseError{ErrUnterminatedQuote, 0, 1, 1}, fld(0, 4, "abc")),
			},
		},
		{
			name:  "crlf line endings",
			input: "a,b\r\nc,d\r\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 5, nil, fld(0, 1, "a"), fld(2, 3, "b")),
				rec(5, 10, nil, fld(5, 6, "c"), fld(7, 8, "d")),
			},
		},
		{
			name:  "quoted field before crlf",
			input: "\"a\"\r\nb\r\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 5, nil, fld(0, 3, "a")),
				rec(5, 8, nil, fld(5, 6, "b")),
			},
		},
		{
			name:  "empty fields",
			input: "a,,b\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 5, nil, fld(0, 1, "a"), fld(2, 2, ""), fld(3, 4, "b")),
			},
		},
		{
			name:  "trailing delimiter at EOF",
			input: "a,",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 2, nil, fld(0, 1, "a"), fld(2, 2, "")),
			},
		},
		{
			name:  "blank lines skipped",
			input: "\n\na\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(2, 4, nil, fld(2, 3, "a")),
			},
		},
		{
			name:  "crlf blank line skipped",
			input: "\r\na\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(2, 4, nil, fld(2, 3, "a")),
			},
		},
		{
			name:  "empty input",
			input: "",
			cfg:   defaultCfg,
			want:  nil,
		},
		{
			name:  "illegal quote at EOF",
			input: "ab\"cd",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 5, &ParseError{ErrIllegalQuote, 2, 3, 1}, fld(0, 2, "ab")),
			},
		},
		{
			name:  "garbage after closing quote before crlf",
			input: "\"a\"\rx\n",
			cfg:   defaultCfg,
			want: []Record{
				rec(0, 6, &ParseError{ErrIllegalQuote, 2, 3, 1}, fld(0, 3, "a")),
			},
		},
		{
			name:  "first error wins",
			input: "abcd\"ef\ngh\n",
			cfg:   Config{MaxFieldSize: 2},
			want: []Record{
				rec(0, 8, &ParseError{ErrFieldTooLarge, 0, 3, 1}, fldDropped(0, 4)),
				rec(8, 11, nil, fld(8, 10, "gh")),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse([]byte(tc.input), tc.cfg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q) mismatch\ngot:  %+v\nwant: %+v", tc.input, got, tc.want)
			}
		})
	}
}

// parseInChunks feeds data through p in the given chunk sizes and returns
// all records, including any final record from Close.
func parseInChunks(data []byte, cfg Config, sizes ...int) []Record {
	p := New(cfg)
	var out []Record
	pos := 0
	for _, n := range sizes {
		if pos+n > len(data) {
			n = len(data) - pos
		}
		out = append(out, p.Feed(data[pos:pos+n])...)
		pos += n
	}
	out = append(out, p.Feed(data[pos:])...)
	return append(out, p.Close()...)
}

var consistencyInputs = []struct {
	input string
	cfg   Config
}{
	{"a,b,c\n1,2,3\n", defaultCfg},
	{"a,\"x\ny\",c\n", defaultCfg},
	{"\"a\"\"b\",c\n", defaultCfg},
	{"ab\"cd,ef\ngh,ij\n", defaultCfg},
	{"\"ab\"x,c\nnext\n", defaultCfg},
	{"abcdef,next\nok\n", Config{MaxFieldSize: 3}},
	{"\"ab\ncdefgh\",x\ny,z\n", Config{MaxFieldSize: 5}},
	{"\"abc", defaultCfg},
	{"a,b\r\nc,d\r\n", defaultCfg},
	{"\"a\"\r\nb\r\n", defaultCfg},
	{"a,,b\n", defaultCfg},
	{"a,", defaultCfg},
	{"\n\na\n", defaultCfg},
	{"\r\na\n", defaultCfg},
	{"", defaultCfg},
	{"ab\"cd", defaultCfg},
	{"\"a\"\rx\n", defaultCfg},
	{"abcd\"ef\ngh\n", Config{MaxFieldSize: 2}},
	{"\"q\"\"\"\r\n,\"multi\nline\"\"x\",z\rlast", Config{MaxFieldSize: 4}},
	{"x\ry\nz", defaultCfg},
}

// TestAllTwoWaySplits feeds every input split at every possible position
// into two chunks and requires results identical to a one-shot parse.
func TestAllTwoWaySplits(t *testing.T) {
	for _, ci := range consistencyInputs {
		data := []byte(ci.input)
		want := Parse(data, ci.cfg)
		for i := 0; i <= len(data); i++ {
			got := parseInChunks(data, ci.cfg, i)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("input %q split at %d mismatch\ngot:  %+v\nwant: %+v", ci.input, i, got, want)
			}
		}
	}
}

// TestAllThreeWaySplits exhaustively checks all pairs of split positions
// for every input.
func TestAllThreeWaySplits(t *testing.T) {
	for _, ci := range consistencyInputs {
		data := []byte(ci.input)
		want := Parse(data, ci.cfg)
		for i := 0; i <= len(data); i++ {
			for j := i; j <= len(data); j++ {
				got := parseInChunks(data, ci.cfg, i, j-i)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("input %q split at %d,%d mismatch\ngot:  %+v\nwant: %+v",
						ci.input, i, j, got, want)
				}
			}
		}
	}
}

// TestRandomChunking parses random inputs in random chunkings and
// compares against one-shot parses, also validating raw ranges.
func TestRandomChunking(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte("ab,\"\n\rx")
	maxes := []int{0, 1, 4, 9}
	for iter := 0; iter < 400; iter++ {
		n := rng.Intn(80)
		data := make([]byte, n)
		for i := range data {
			data[i] = alphabet[rng.Intn(len(alphabet))]
		}
		cfg := Config{MaxFieldSize: maxes[rng.Intn(len(maxes))]}
		want := Parse(data, cfg)

		var sizes []int
		for pos := 0; pos < n; {
			s := rng.Intn(9)
			sizes = append(sizes, s)
			pos += s
		}
		got := parseInChunks(data, cfg, sizes...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d input %q chunks %v mismatch\ngot:  %+v\nwant: %+v",
				iter, data, sizes, got, want)
		}

		// Raw ranges must lie inside their record, be ordered, and slice
		// the original input without escaping it.
		for _, r := range want {
			if r.Start < 0 || r.End > int64(len(data)) || r.Start > r.End {
				t.Fatalf("iter %d: bad record range [%d,%d)", iter, r.Start, r.End)
			}
			prev := r.Start
			for _, f := range r.Fields {
				if f.Start < prev || f.End > r.End || f.Start > f.End {
					t.Fatalf("iter %d: bad field range [%d,%d) in record [%d,%d)",
						iter, f.Start, f.End, r.Start, r.End)
				}
				prev = f.End
				_ = data[f.Start:f.End] // must not panic
			}
			if r.Err != nil && (r.Err.Start < r.Start || r.Err.End > r.End || r.Err.Start >= r.Err.End) {
				t.Fatalf("iter %d: bad error range [%d,%d) in record [%d,%d)",
					iter, r.Err.Start, r.Err.End, r.Start, r.End)
			}
		}
	}
}

// TestFieldLimitMemory verifies that once a field crosses the limit the
// parser stops buffering its value yet still finds the record end.
func TestFieldLimitMemory(t *testing.T) {
	cfg := Config{MaxFieldSize: 8}
	p := New(cfg)
	// A huge quoted field with embedded newlines, fed byte by byte.
	head := "\""
	body := make([]byte, 0, 4096)
	for i := 0; i < 4096; i++ {
		if i%7 == 0 {
			body = append(body, '\n')
		} else {
			body = append(body, 'z')
		}
	}
	tail := "\",ok\nnext,rec\n"
	var out []Record
	out = append(out, p.Feed([]byte(head))...)
	for _, b := range body {
		out = append(out, p.Feed([]byte{b})...)
	}
	out = append(out, p.Feed([]byte(tail))...)
	out = append(out, p.Close()...)

	if len(out) != 2 {
		t.Fatalf("expected 2 records, got %d", len(out))
	}
	if out[0].Err == nil || out[0].Err.Kind != ErrFieldTooLarge {
		t.Fatalf("expected ErrFieldTooLarge, got %+v", out[0].Err)
	}
	if out[0].Fields[0].Value != nil {
		t.Fatalf("oversize field should have nil Value, got %d bytes", len(out[0].Fields[0].Value))
	}
	if string(out[0].Fields[1].Value) != "ok" {
		t.Fatalf("second field = %q, want %q", out[0].Fields[1].Value, "ok")
	}
	if string(out[1].Fields[0].Value) != "next" || string(out[1].Fields[1].Value) != "rec" {
		t.Fatalf("record after oversize record corrupted: %+v", out[1])
	}
}
