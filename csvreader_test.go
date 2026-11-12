package taskpkg

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type splitReader struct {
	data  []byte
	split int
	stage int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if r.stage == 0 {
		if r.split > len(r.data) {
			r.split = len(r.data)
		}
		if r.split == 0 {
			r.stage = 1
		} else {
			n := copy(p, r.data[:r.split])
			r.split = -r.split
			if r.split == 0 {
				r.stage = 2
			} else {
				r.stage = 1
			}
			return n, nil
		}
	}
	if r.stage == 1 {
		split := r.split
		if split < 0 {
			split = -split
		}
		if split >= len(r.data) {
			r.stage = 2
			return 0, io.EOF
		}
		n := copy(p, r.data[split:])
		r.stage = 2
		return n, nil
	}
	return 0, io.EOF
}

func TestReaderAllSplitPositionsAgainstWholeParse(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions()
	fixtures := []struct {
		name string
		data string
		opts Options
	}{
		{
			name: "lf_and_crlf",
			data: "id,name,note\r\n1,\"a,b\",plain\n2,\"line1\nline2\",\"say \"\"hi\"\"\"\r\n",
			opts: opts,
		},
		{
			name: "double_quote_split",
			data: "a,\"x\"\"y\",z\r\n1,\"\"\"\",2\n",
			opts: opts,
		},
		{
			name: "trailing_crlf",
			data: "a,b\r\n",
			opts: opts,
		},
		{
			name: "bare_cr_is_data",
			data: "a,b\rc\nd,e\r",
			opts: opts,
		},
		{
			name: "embedded_crlf",
			data: "a,\"x\r\n y\",b\n",
			opts: opts,
		},
		{
			name: "empty_fields_and_lines",
			data: ",\n\n,\r\na,\n,last",
			opts: opts,
		},
		{
			name: "quote_outside_is_literal",
			data: "a\"b,\"c\"\"d\"\n\"\"\"x\",y\n",
			opts: opts,
		},
		{
			name: "custom_delimiter_and_quote",
			data: "a;'x;''y'\n'line1\nline2';z\n",
			opts: Options{Delimiter: ';', Quote: '\''},
		},
		{
			name: "no_final_newline",
			data: "a,b",
			opts: opts,
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			want := referenceParse([]byte(fixture.data), fixture.opts)
			for split := 0; split <= len(fixture.data); split++ {
				got := collectReader(t, &splitReader{data: []byte(fixture.data), split: split}, fixture.opts)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("split %d:\ngot  %#v\nwant %#v", split, got, want)
				}
			}
		})
	}
}

func TestExhaustiveShortInputsAgainstWholeParse(t *testing.T) {
	t.Parallel()

	alphabet := []byte{'a', ',', '"', '\r', '\n'}
	for length := 0; length <= 5; length++ {
		var walk func(prefix []byte)
		walk = func(prefix []byte) {
			if len(prefix) == length {
				data := append([]byte(nil), prefix...)
				want := referenceParse(data, DefaultOptions())
				for split := 0; split <= len(data); split++ {
					got := collectReader(t, &splitReader{data: data, split: split}, DefaultOptions())
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("data %q split %d:\ngot  %#v\nwant %#v\ngot errors  %#v\nwant errors %#v",
							data, split, got, want, derefErrors(got.Errors), derefErrors(want.Errors))
					}
				}
				return
			}
			for _, b := range alphabet {
				walk(append(prefix, b))
			}
		}
		walk(nil)
	}
}

func derefErrors(errs []*RecordError) []RecordError {
	out := make([]RecordError, len(errs))
	for i := range errs {
		out[i] = *errs[i]
	}
	return out
}

func TestCrossChunkDoubleQuoteAndCRLF(t *testing.T) {
	t.Parallel()

	const data = "a,\"x\"\"y\",z\r\n"
	want := referenceParse([]byte(data), DefaultOptions())

	for _, split := range []int{
		1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13,
	} {
		got := collectReader(t, &splitReader{data: []byte(data), split: split}, DefaultOptions())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("split %d changed parse: got %#v want %#v", split, got, want)
		}
	}

	// The last split is immediately after CR and before LF.
	rec, err := NewReader(&splitReader{data: []byte(data), split: len(data) - 1}).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got, want := rec.Span, (Span{Start: 0, End: int64(len(data) - 2)}); got != want {
		t.Fatalf("record span: got %+v want %+v", got, want)
	}
	if got, want := rec.Fields[1].Value, `x"y`; got != want {
		t.Fatalf("decoded value: got %q want %q", got, want)
	}
	if rec.LineSpan != (LineSpan{Start: 1, End: 1}) {
		t.Fatalf("line span: got %+v", rec.LineSpan)
	}
}

func TestTruncatedQuoteNeverReturnsPartialRecord(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		`a,"unterminated`,
		`a,"unterminated""`,
		"a,\"unterminated\nnext",
		`a,"x""`,
	} {
		data := data
		t.Run(strings.ReplaceAll(data, "\n", `\n`), func(t *testing.T) {
			t.Parallel()
			for split := 0; split <= len(data); split++ {
				reader := NewReader(&splitReader{data: []byte(data), split: split})
				rec, err := reader.Next()
				if rec != nil {
					t.Fatalf("split %d: partial record returned: %#v", split, rec)
				}
				var recErr *RecordError
				if !errors.As(err, &recErr) {
					t.Fatalf("split %d: error type: %T %v", split, err, err)
				}
				if !errors.Is(err, ErrTruncatedQuote) {
					t.Fatalf("split %d: wrong error: %v", split, err)
				}
				if recErr.Index != 0 || recErr.RecordSpan != (Span{Start: 0, End: int64(len(data))}) {
					t.Fatalf("split %d: error metadata: %#v", split, recErr)
				}
			}
		})
	}
}

func TestOverlongFieldDiscardsRecordAndRecoversAtNextBoundary(t *testing.T) {
	t.Parallel()

	opts := Options{
		Delimiter:     ',',
		Quote:         '"',
		MaxFieldBytes: 8,
	}
	const data = "a,\"012345\n6789\"\"tail\",z\r\nok,small\n"

	for split := 0; split <= len(data); split++ {
		reader := NewReader(&splitReader{data: []byte(data), split: split}, opts)

		rec, err := reader.Next()
		if rec != nil {
			t.Fatalf("split %d: overlong record returned fields: %#v", split, rec)
		}
		var recErr *RecordError
		if !errors.As(err, &recErr) || !errors.Is(err, ErrFieldTooLong) {
			t.Fatalf("split %d: want ErrFieldTooLong, got %T %v", split, err, err)
		}
		if recErr.FieldIndex != 1 {
			t.Fatalf("split %d: field index: got %d", split, recErr.FieldIndex)
		}
		if recErr.RecordSpan != (Span{Start: 0, End: 23}) || recErr.LineSpan != (LineSpan{Start: 1, End: 2}) {
			t.Fatalf("split %d: error metadata: %#v", split, recErr)
		}

		rec, err = reader.Next()
		if err != nil {
			t.Fatalf("split %d: recovery failed: %v", split, err)
		}
		want := []Field{
			{Index: 0, Raw: Span{Start: 25, End: 27}, Value: "ok"},
			{Index: 1, Raw: Span{Start: 28, End: 33}, Value: "small"},
		}
		if !reflect.DeepEqual(rec.Fields, want) {
			t.Fatalf("split %d: recovered fields: got %#v want %#v", split, rec.Fields, want)
		}
		if rec.LineSpan != (LineSpan{Start: 3, End: 3}) {
			t.Fatalf("split %d: recovered line span: got %+v", split, rec.LineSpan)
		}
		if rec.Span != (Span{Start: 25, End: 33}) {
			t.Fatalf("split %d: recovered span: got %+v", split, rec.Span)
		}
	}
}

func TestMalformedRecordIsDiscardedButFollowingRecordParses(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions()
	const data = "\"a\"x,\"b\nc\",d\nnext,ok\n"
	wantErrSpan := Span{Start: 0, End: 12}
	wantErrLines := LineSpan{Start: 1, End: 2}

	for split := 0; split <= len(data); split++ {
		reader := NewReader(&splitReader{data: []byte(data), split: split}, opts)
		rec, err := reader.Next()
		if rec != nil {
			t.Fatalf("split %d: malformed record returned: %#v", split, rec)
		}
		var recErr *RecordError
		if !errors.As(err, &recErr) || !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("split %d: want malformed error, got %T %v", split, err, err)
		}
		if recErr.RecordSpan != wantErrSpan || recErr.LineSpan != wantErrLines {
			t.Fatalf("split %d: malformed error metadata: %#v", split, recErr)
		}

		rec, err = reader.Next()
		if err != nil {
			t.Fatalf("split %d: recovery failed: %v", split, err)
		}
		if got, want := fieldValues(rec), []string{"next", "ok"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("split %d: recovered values: got %#v want %#v", split, got, want)
		}
	}
}

func TestUnquotedOverlongFieldAndExactLimit(t *testing.T) {
	t.Parallel()

	opts := Options{Delimiter: ',', Quote: '"', MaxFieldBytes: 8}
	const data = "head,0123456789,tail\nok,12345678\n"

	reader := NewReader(strings.NewReader(data), opts)
	rec, err := reader.Next()
	if rec != nil {
		t.Fatalf("overlong record returned: %#v", rec)
	}
	var recErr *RecordError
	if !errors.As(err, &recErr) || !errors.Is(err, ErrFieldTooLong) {
		t.Fatalf("want ErrFieldTooLong, got %T %v", err, err)
	}
	if recErr.FieldIndex != 1 {
		t.Fatalf("field index: got %d want 1", recErr.FieldIndex)
	}

	rec, err = reader.Next()
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if got, want := fieldValues(rec), []string{"ok", "12345678"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered values: got %#v want %#v", got, want)
	}
}

func TestEmptyInputTrailingCommaAndFinalBareCR(t *testing.T) {
	t.Parallel()

	reader := NewReader(strings.NewReader(""))
	if rec, err := reader.Next(); rec != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("empty input: rec=%#v err=%v", rec, err)
	}

	rec, err := NewReader(strings.NewReader("a,")).Next()
	if err != nil {
		t.Fatalf("trailing comma: %v", err)
	}
	want := []Field{
		{Index: 0, Raw: Span{Start: 0, End: 1}, Value: "a"},
		{Index: 1, Raw: Span{Start: 2, End: 2}, Value: ""},
	}
	if !reflect.DeepEqual(rec.Fields, want) {
		t.Fatalf("trailing comma fields: got %#v want %#v", rec.Fields, want)
	}

	rec, err = NewReader(strings.NewReader("a,b\r")).Next()
	if err != nil {
		t.Fatalf("final bare CR: %v", err)
	}
	if got, want := rec.Fields[1].Value, "b\r"; got != want {
		t.Fatalf("final bare CR value: got %q want %q", got, want)
	}
	if got, want := rec.Span, (Span{Start: 0, End: 4}); got != want {
		t.Fatalf("final bare CR span: got %+v want %+v", got, want)
	}
}

func TestOptionsAndConvenienceParsers(t *testing.T) {
	t.Parallel()

	if _, err := NewReaderWithOptions(nil, DefaultOptions()); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil reader error: %v", err)
	}
	if _, err := NewReaderWithOptions(strings.NewReader(""), Options{Delimiter: ',', Quote: ','}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("equal delimiter/quote error: %v", err)
	}

	records, err := ParseBytes([]byte("a,b\n"))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(records) != 1 || !reflect.DeepEqual(fieldValues(records[0]), []string{"a", "b"}) {
		t.Fatalf("ParseBytes records: %#v", records)
	}

	rec, err := NewReader(strings.NewReader("x,y")).ReadRecord()
	if err != nil || rec == nil {
		t.Fatalf("ReadRecord: %#v %v", rec, err)
	}
	if got, want := rec.Fields[0].Raw, (Span{Start: 0, End: 1}); got != want {
		t.Fatalf("raw span: got %+v want %+v", got, want)
	}
}

func fieldValues(rec *Record) []string {
	if rec == nil {
		return nil
	}
	values := make([]string, len(rec.Fields))
	for i := range rec.Fields {
		values[i] = rec.Fields[i].Value
	}
	return values
}

type readerOutcome struct {
	Records []*Record
	Errors  []*RecordError
}

func collectReader(t *testing.T, r io.Reader, opts Options) readerOutcome {
	t.Helper()
	reader := NewReader(r, opts)
	var outcome readerOutcome
	for {
		rec, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return outcome
		}
		if err != nil {
			var recErr *RecordError
			if !errors.As(err, &recErr) {
				t.Fatalf("unexpected non-record error: %v", err)
			}
			outcome.Errors = append(outcome.Errors, recErr)
			continue
		}
		if rec == nil {
			t.Fatal("nil record without error")
		}
		outcome.Records = append(outcome.Records, rec)
	}
}

type wholeRawRecord struct {
	span     Span
	lineSpan LineSpan
}

// referenceParse intentionally uses a separate whole-input record scanner and
// a separate fixed-record decoder. It does not call the streaming parser.
func referenceParse(data []byte, opts Options) readerOutcome {
	if opts.Delimiter == 0 {
		opts.Delimiter = ','
	}
	if opts.Quote == 0 {
		opts.Quote = '"'
	}

	var outcome readerOutcome
	raw := referenceRecords(data, opts.Delimiter, opts.Quote)
	for i, rawRec := range raw {
		fields, kind, fieldIndex, message := referenceFields(data, rawRec.span, opts.Delimiter, opts.Quote)
		if kind == nil && opts.MaxFieldBytes > 0 {
			for j := range fields {
				if fields[j].Raw.Len() > opts.MaxFieldBytes {
					kind = ErrFieldTooLong
					fieldIndex = j
					message = "field raw size exceeds MaxFieldBytes"
					break
				}
			}
		}
		if kind != nil {
			outcome.Errors = append(outcome.Errors, &RecordError{
				Index:      int64(i),
				Kind:       kind,
				RecordSpan: rawRec.span,
				LineSpan:   rawRec.lineSpan,
				FieldIndex: fieldIndex,
				Message:    message,
			})
			continue
		}
		outcome.Records = append(outcome.Records, &Record{
			Index:    int64(i),
			Span:     rawRec.span,
			LineSpan: rawRec.lineSpan,
			Fields:   fields,
		})
	}
	return outcome
}

func referenceRecords(data []byte, delimiter, quote byte) []wholeRawRecord {
	var records []wholeRawRecord
	recordStart := 0
	startLine := int64(1)
	line := int64(1)
	atFieldStart := true
	inQuote := false

	for i := 0; i < len(data); {
		b := data[i]
		if inQuote {
			switch {
			case b == quote && i+1 < len(data) && data[i+1] == quote:
				i += 2
			case b == quote:
				inQuote = false
				atFieldStart = false
				i++
			default:
				if b == '\n' {
					line++
				}
				i++
			}
			continue
		}

		switch {
		case b == quote && atFieldStart:
			inQuote = true
			i++
		case b == delimiter:
			atFieldStart = true
			i++
		case b == '\n':
			records = append(records, wholeRawRecord{
				span:     Span{Start: int64(recordStart), End: int64(i)},
				lineSpan: LineSpan{Start: startLine, End: line},
			})
			i++
			line++
			recordStart = i
			startLine = line
			atFieldStart = true
		case b == '\r' && i+1 < len(data) && data[i+1] == '\n':
			records = append(records, wholeRawRecord{
				span:     Span{Start: int64(recordStart), End: int64(i)},
				lineSpan: LineSpan{Start: startLine, End: line},
			})
			i += 2
			line++
			recordStart = i
			startLine = line
			atFieldStart = true
		default:
			atFieldStart = false
			i++
		}
	}

	if recordStart < len(data) {
		records = append(records, wholeRawRecord{
			span:     Span{Start: int64(recordStart), End: int64(len(data))},
			lineSpan: LineSpan{Start: startLine, End: line},
		})
	}
	return records
}

func referenceFields(data []byte, record Span, delimiter, quote byte) ([]Field, error, int, string) {
	var fields []Field
	i := record.Start
	for {
		fieldStart := i
		if i == record.End {
			fields = append(fields, Field{Index: len(fields), Raw: Span{Start: i, End: i}})
			return fields, nil, 0, ""
		}

		if data[i] == quote {
			i++
			var value []byte
			closed := false
			for i < record.End {
				b := data[i]
				if b == quote {
					if i+1 < record.End && data[i+1] == quote {
						value = append(value, quote)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				value = append(value, b)
				i++
			}
			if !closed {
				return nil, ErrTruncatedQuote, len(fields), "EOF reached inside a quoted field"
			}
			fields = append(fields, Field{
				Index: len(fields),
				Raw:   Span{Start: fieldStart, End: i},
				Value: string(value),
			})
			if i == record.End {
				return fields, nil, 0, ""
			}
			if data[i] != delimiter {
				return nil, ErrMalformedRecord, len(fields) - 1, "byte after closing quote is not a delimiter or line ending"
			}
			i++
			continue
		}

		var value []byte
		for i < record.End && data[i] != delimiter {
			value = append(value, data[i])
			i++
		}
		fields = append(fields, Field{
			Index: len(fields),
			Raw:   Span{Start: fieldStart, End: i},
			Value: string(value),
		})
		if i == record.End {
			return fields, nil, 0, ""
		}
		i++
	}
}
