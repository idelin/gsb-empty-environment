package delimparse

import (
	"fmt"
	"strings"
	"testing"
)

func feedChunks(opt Options, chunks ...[]byte) []Event {
	p := NewParser(opt)
	var events []Event
	for _, c := range chunks {
		events = append(events, p.Feed(c)...)
	}
	events = append(events, p.Close()...)
	return events
}

func formatEvents(events []Event) string {
	var b strings.Builder
	for _, e := range events {
		if r := e.Record; r != nil {
			fmt.Fprintf(&b, "R[%d,%d)", r.Start, r.End)
			for _, f := range r.Fields {
				fmt.Fprintf(&b, " {%d,%d", f.Start, f.End)
				if f.Quoted {
					b.WriteString(" q")
				}
				if f.Truncated {
					b.WriteString(" t")
				}
				fmt.Fprintf(&b, " %q}", string(f.Value))
			}
			b.WriteByte('\n')
		} else {
			fmt.Fprintf(&b, "E[%d,%d) %s\n", e.Err.Start, e.Err.End, e.Err.Message)
		}
	}
	return b.String()
}

// TestExact pins down records, raw byte ranges, decoded values, error
// ranges and truncation flags for representative inputs.
func TestExact(t *testing.T) {
	cases := []struct {
		name string
		opt  Options
		in   string
		want string
	}{
		{
			name: "simple",
			in:   "a,b,c\n1,2,3\n",
			want: "R[0,6) {0,1 \"a\"} {2,3 \"b\"} {4,5 \"c\"}\n" +
				"R[6,12) {6,7 \"1\"} {8,9 \"2\"} {10,11 \"3\"}\n",
		},
		{
			name: "quoted newline and doubled quote",
			in:   "\"a\nb\",c\n\"d\"\"e\",f\n",
			want: "R[0,8) {0,5 q \"a\\nb\"} {6,7 \"c\"}\n" +
				"R[8,17) {8,14 q \"d\\\"e\"} {15,16 \"f\"}\n",
		},
		{
			name: "escape character",
			opt:  Options{Escape: '\\'},
			in:   "\"a\\\"b\",c\n",
			want: "R[0,9) {0,6 q \"a\\\"b\"} {7,8 \"c\"}\n",
		},
		{
			name: "bare quote isolates physical line",
			in:   "ab\"cd,ef\ngh,ij\n",
			want: "E[0,9) bare quote in unquoted field\n" +
				"R[9,15) {9,11 \"gh\"} {12,14 \"ij\"}\n",
		},
		{
			name: "junk after closing quote",
			in:   "\"ab\"x,cd\nef,gh\n",
			want: "E[0,9) unexpected character after closing quote\n" +
				"R[9,15) {9,11 \"ef\"} {12,14 \"gh\"}\n",
		},
		{
			name: "error line is not merged into next record",
			in:   "a,\"b\nc\",d\nbad\"line,x\nok1,ok2\n\"unclosed",
			want: "R[0,10) {0,1 \"a\"} {2,7 q \"b\\nc\"} {8,9 \"d\"}\n" +
				"E[10,21) bare quote in unquoted field\n" +
				"R[21,29) {21,24 \"ok1\"} {25,28 \"ok2\"}\n" +
				"E[29,38) unterminated quoted field\n",
		},
		{
			name: "error on later physical line of multiline record",
			in:   "\"a\nb\"x,c\nd,e\n",
			want: "E[3,9) unexpected character after closing quote\n" +
				"R[9,13) {9,10 \"d\"} {11,12 \"e\"}\n",
		},
		{
			name: "quotes inside skipped line stay inert",
			in:   "ab\"cd,\"ef\ngh\n",
			want: "E[0,10) bare quote in unquoted field\n" +
				"R[10,13) {10,12 \"gh\"}\n",
		},
		{
			name: "field cap truncates but record end is found",
			opt:  Options{MaxFieldBytes: 3},
			in:   "\"abcdef\",x\ny,z\n",
			want: "R[0,11) {0,8 q t \"\"} {9,10 \"x\"}\n" +
				"R[11,15) {11,12 \"y\"} {13,14 \"z\"}\n",
		},
		{
			name: "field cap on unquoted field",
			opt:  Options{MaxFieldBytes: 3},
			in:   "abcdefgh,x\n",
			want: "R[0,11) {0,8 t \"\"} {9,10 \"x\"}\n",
		},
		{
			name: "field cap across quoted newline",
			opt:  Options{MaxFieldBytes: 3},
			in:   "\"ab\ncdef\",x\n",
			want: "R[0,12) {0,9 q t \"\"} {10,11 \"x\"}\n",
		},
		{
			name: "crlf",
			in:   "a,b\r\nc,d\r\n",
			want: "R[0,5) {0,1 \"a\"} {2,3 \"b\"}\n" +
				"R[5,10) {5,6 \"c\"} {7,8 \"d\"}\n",
		},
		{
			name: "quoted crlf",
			in:   "\"a\"\r\n\"b\"\r\n",
			want: "R[0,5) {0,3 q \"a\"}\n" +
				"R[5,10) {5,8 q \"b\"}\n",
		},
		{
			name: "blank lines skipped",
			in:   "\n\r\na\n\n",
			want: "R[3,5) {3,4 \"a\"}\n",
		},
		{
			name: "unterminated quote at eof",
			in:   "\"abc",
			want: "E[0,4) unterminated quoted field\n",
		},
		{
			name: "trailing comma at eof",
			in:   "a,",
			want: "R[0,2) {0,1 \"a\"} {2,2 \"\"}\n",
		},
		{
			name: "doubled quote at eof",
			in:   "\"a\"\"b\",c",
			want: "R[0,8) {0,6 q \"a\\\"b\"} {7,8 \"c\"}\n",
		},
		{
			name: "bare quote at eof",
			in:   "x\"y",
			want: "E[0,3) bare quote in unquoted field\n",
		},
		{
			name: "junk after quote at eof",
			in:   "\"a\"x",
			want: "E[0,4) unexpected character after closing quote\n",
		},
		{
			name: "escape at eof",
			opt:  Options{Escape: '\\'},
			in:   "\"a\\",
			want: "E[0,3) unterminated quoted field\n",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "empty quoted field",
			in:   "\"\",x\n",
			want: "R[0,5) {0,2 q \"\"} {3,4 \"x\"}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatEvents(feedChunks(tc.opt, []byte(tc.in)))
			if got != tc.want {
				t.Errorf("input %q\nwant:\n%s\ngot:\n%s", tc.in, tc.want, got)
			}
		})
	}
}

// consistencyInputs exercises every parser state and every error path.
var consistencyInputs = []struct {
	name string
	opt  Options
	in   string
}{
	{"simple", Options{}, "a,b,c\n1,2,3\n"},
	{"quoted newline", Options{}, "\"a\nb\",c\n\"d\"\"e\",f,g\n"},
	{"escape", Options{Escape: '\\'}, "\"a\\\"b\n\",c\nd,e\n"},
	{"bare quote", Options{}, "ab\"cd,ef\ngh,ij\n"},
	{"junk after quote", Options{}, "\"ab\"x,cd\nef,gh\n"},
	{"multiline then error", Options{}, "\"a\nb\"x,c\nd,e\n"},
	{"error with quotes in skipped line", Options{}, "ab\"cd,\"ef\"g\nh,i\n"},
	{"cap quoted", Options{MaxFieldBytes: 3}, "\"abcdef\",x\ny,z\n"},
	{"cap unquoted", Options{MaxFieldBytes: 3}, "abcdefgh,x\ny,z\n"},
	{"cap across quoted newline", Options{MaxFieldBytes: 3}, "\"ab\ncdef\",x\ny,z\n"},
	{"cap with escape", Options{MaxFieldBytes: 2, Escape: '\\'}, "\"a\\nb\\nc\",d\n"},
	{"crlf", Options{}, "a,b\r\nc,d\r\n"},
	{"quoted crlf", Options{}, "\"a\"\r\n\"b\"\r\n"},
	{"cr in unquoted field", Options{}, "a\rb,c\n"},
	{"blank lines", Options{}, "\n\r\na\n\n"},
	{"unterminated", Options{}, "ok\n\"abc"},
	{"unterminated multiline", Options{}, "ok1,ok2\n\"a\nb"},
	{"trailing comma", Options{}, "a,"},
	{"doubled quote eof", Options{}, "\"a\"\"b\",c"},
	{"bare quote eof", Options{}, "x\"y"},
	{"junk after quote eof", Options{}, "\"a\"x"},
	{"junk after cr", Options{}, "\"a\"\rx\n"},
	{"escape eof", Options{Escape: '\\'}, "\"a\\"},
	{"empty", Options{}, ""},
	{"only newlines", Options{}, "\n\n\n"},
	{"utf8", Options{}, "héllo,\"wörld\"\n"},
	{"many errors", Options{}, "a\"b\nc\"d\ne,f\n"},
	{"custom comma", Options{Comma: ';'}, "a;b\n\"c;d\";e\n"},
	{"custom quote", Options{Quote: '\''}, "'a''b',c\n"},
}

// TestConsistencyAllSplits verifies that feeding the input in chunks
// split at every possible position yields exactly the same records,
// raw ranges, values, truncation flags and error ranges as a single
// one-shot read.
func TestConsistencyAllSplits(t *testing.T) {
	for _, tc := range consistencyInputs {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.in
			want := formatEvents(feedChunks(tc.opt, []byte(data)))

			// Byte at a time.
			var singles [][]byte
			for i := 0; i < len(data); i++ {
				singles = append(singles, []byte{data[i]})
			}
			if got := formatEvents(feedChunks(tc.opt, singles...)); got != want {
				t.Fatalf("byte-at-a-time mismatch\nwant:\n%s\ngot:\n%s", want, got)
			}

			// All 2-chunk splits.
			for i := 0; i <= len(data); i++ {
				got := formatEvents(feedChunks(tc.opt, []byte(data[:i]), []byte(data[i:])))
				if got != want {
					t.Fatalf("2-split at %d mismatch\nwant:\n%s\ngot:\n%s", i, want, got)
				}
			}

			// All 3-chunk splits.
			for i := 0; i <= len(data); i++ {
				for j := i; j <= len(data); j++ {
					got := formatEvents(feedChunks(tc.opt,
						[]byte(data[:i]), []byte(data[i:j]), []byte(data[j:])))
					if got != want {
						t.Fatalf("3-split at %d,%d mismatch\nwant:\n%s\ngot:\n%s", i, j, want, got)
					}
				}
			}
		})
	}
}

// TestRawRanges verifies that field ranges are valid absolute offsets
// into the original input and reconstruct the raw field text.
func TestRawRanges(t *testing.T) {
	for _, tc := range consistencyInputs {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.in
			events := feedChunks(tc.opt, []byte(data))
			quote := tc.opt.Quote
			if quote == 0 {
				quote = '"'
			}
			var prevEnd int64
			for _, e := range events {
				var start, end int64
				if e.Record != nil {
					start, end = e.Record.Start, e.Record.End
				} else {
					start, end = e.Err.Start, e.Err.End
				}
				if start < 0 || end > int64(len(data)) || start > end {
					t.Fatalf("event range [%d,%d) out of bounds for %q", start, end, data)
				}
				if start < prevEnd {
					t.Fatalf("events overlap or are unordered: prev end %d, start %d", prevEnd, start)
				}
				prevEnd = end

				r := e.Record
				if r == nil {
					continue
				}
				if len(r.Fields) == 0 {
					t.Fatalf("record with no fields: %+v", r)
				}
				for i, f := range r.Fields {
					if f.Start < r.Start || f.End > r.End || f.Start > f.End {
						t.Fatalf("field range [%d,%d) outside record [%d,%d)",
							f.Start, f.End, r.Start, r.End)
					}
					if i == 0 && f.Start != r.Start {
						t.Fatalf("first field starts at %d, record at %d", f.Start, r.Start)
					}
					if i > 0 && f.Start != r.Fields[i-1].End+1 {
						t.Fatalf("fields %d and %d not delimiter-adjacent: [%d,%d) [%d,%d)",
							i-1, i, r.Fields[i-1].Start, r.Fields[i-1].End, f.Start, f.End)
					}
					if f.Quoted {
						raw := data[f.Start:f.End]
						if len(raw) < 2 || raw[0] != quote || raw[len(raw)-1] != quote {
							t.Fatalf("quoted field raw %q not enclosed in quotes", raw)
						}
					}
					if f.Truncated && f.Value != nil {
						t.Fatalf("truncated field carries a value %q", f.Value)
					}
				}
				last := r.Fields[len(r.Fields)-1]
				if d := r.End - last.End; d < 0 || d > 2 {
					t.Fatalf("record end %d too far from last field end %d", r.End, last.End)
				}
			}
		})
	}
}

// TestParseHelper checks the one-shot convenience API.
func TestParseHelper(t *testing.T) {
	records, errs := Parse([]byte("a,\"b\nc\"\nx\"y\nz\n"), Options{})
	if len(records) != 2 || len(errs) != 1 {
		t.Fatalf("got %d records, %d errors", len(records), len(errs))
	}
	if got := string(records[0].Fields[1].Value); got != "b\nc" {
		t.Fatalf("field value %q", got)
	}
	if errs[0].Message != ErrBareQuote || errs[0].Start != 8 || errs[0].End != 12 {
		t.Fatalf("unexpected error %+v", errs[0])
	}
	if got := string(records[1].Fields[0].Value); got != "z" {
		t.Fatalf("record after error line: %q", got)
	}
}
