package csvq

import "fmt"

// Parser is a quote-aware CSV parser.
type Parser struct {
	Comma byte // field separator, default ','
	Quote byte // quote character, default '"'
}

// NewParser returns a Parser with RFC 4180 defaults.
func NewParser() *Parser { return &Parser{Comma: ',', Quote: '"'} }

// Parse parses src with a default parser.
func Parse(src []byte) (*Table, error) { return NewParser().Parse(src) }

// Parse parses src into a Table. The first record is the header; its
// column count defines the positional width for all data records.
func (p *Parser) Parse(src []byte) (*Table, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("csvq: empty source")
	}
	p.defaults()
	hfields, pos, err := p.parseRecord(src, 0)
	if err != nil {
		return nil, fmt.Errorf("csvq: header: %v", err)
	}
	t := &Table{DataOffset: int64(pos)}
	for i, f := range hfields {
		t.Header.Columns = append(t.Header.Columns, Column{Index: i, Name: f.Value})
	}
	records, quarantine := p.parseItems(src[pos:], int64(pos), t.Header.Len(), 0)
	t.Records = records
	t.Quarantine = quarantine
	t.Items = interleave(t.Records, t.Quarantine)
	return t, nil
}

func (p *Parser) defaults() {
	if p.Comma == 0 {
		p.Comma = ','
	}
	if p.Quote == 0 {
		p.Quote = '"'
	}
}

// interleave merges valid records and quarantined rows by sequence.
func interleave(records []Record, quarantine []QuarantinedRow) []Item {
	items := make([]Item, 0, len(records)+len(quarantine))
	ri, qi := 0, 0
	for ri < len(records) || qi < len(quarantine) {
		takeRecord := qi >= len(quarantine) ||
			(ri < len(records) && records[ri].Seq < quarantine[qi].Seq)
		if takeRecord {
			r := &records[ri]
			items = append(items, Item{Seq: r.Seq, Offset: r.Offset, EndOffset: r.EndOffset, Record: r})
			ri++
		} else {
			q := &quarantine[qi]
			items = append(items, Item{Seq: q.Seq, Offset: q.Offset, EndOffset: q.EndOffset, Quarantine: q})
			qi++
		}
	}
	return items
}

// parseItems parses the data records of src (no header). base is added to
// all offsets and seqBase to all sequences, so a byte range carved out of
// a larger source can be re-parsed independently with absolute positions.
// Malformed rows and over-wide rows become quarantined rows; short rows
// are padded with missing fields. Newlines inside quotes are consumed by
// the quote state machine and never split or repair a row.
func (p *Parser) parseItems(src []byte, base int64, width int, seqBase int) ([]Record, []QuarantinedRow) {
	p.defaults()
	var records []Record
	var quarantine []QuarantinedRow
	pos := 0
	seq := seqBase
	for pos < len(src) {
		start := pos
		fields, next, err := p.parseRecord(src, pos)
		if err != nil {
			end := resync(src, next)
			quarantine = append(quarantine, QuarantinedRow{
				Seq:       seq,
				Offset:    base + int64(start),
				EndOffset: base + int64(end),
				Raw:       append([]byte(nil), src[start:end]...),
				Fields:    fieldValues(fields),
				Reason:    err.Error(),
			})
			pos = end
			seq++
			continue
		}
		pos = next
		if len(fields) > width {
			quarantine = append(quarantine, QuarantinedRow{
				Seq:       seq,
				Offset:    base + int64(start),
				EndOffset: base + int64(pos),
				Raw:       append([]byte(nil), src[start:pos]...),
				Fields:    fieldValues(fields),
				Reason:    fmt.Sprintf("too many fields: got %d, want %d", len(fields), width),
			})
			seq++
			continue
		}
		for len(fields) < width {
			fields = append(fields, Field{}) // missing: Present stays false
		}
		records = append(records, Record{
			Seq:       seq,
			Offset:    base + int64(start),
			EndOffset: base + int64(pos),
			Fields:    fields,
		})
		seq++
	}
	return records, quarantine
}

// resync skips to just past the next newline after a malformed row.
func resync(src []byte, pos int) int {
	for pos < len(src) {
		if src[pos] == '\n' {
			return pos + 1
		}
		pos++
	}
	return len(src)
}

func fieldValues(fields []Field) []string {
	values := make([]string, len(fields))
	for i, f := range fields {
		values[i] = f.Value
	}
	return values
}

// parseRecord parses one record starting at pos. It returns the fields,
// the position one past the record terminator, and any quoting error.
func (p *Parser) parseRecord(src []byte, pos int) ([]Field, int, error) {
	var fields []Field
	for {
		f, next, terminated, err := p.parseField(src, pos)
		if err != nil {
			return fields, next, err
		}
		fields = append(fields, f)
		pos = next
		if terminated {
			return fields, pos, nil
		}
	}
}

// parseField parses one field starting at pos. terminated reports whether
// the field ended the record (terminator or end of source).
func (p *Parser) parseField(src []byte, pos int) (Field, int, bool, error) {
	if pos < len(src) && src[pos] == p.Quote {
		return p.parseQuoted(src, pos)
	}
	i := pos
	for i < len(src) && src[i] != p.Comma && src[i] != '\r' && src[i] != '\n' {
		i++
	}
	f := Field{Value: string(src[pos:i]), Present: true, Empty: i == pos}
	return p.afterField(src, i, f)
}

// parseQuoted parses a quoted field. A doubled quote is an escaped quote;
// any other byte, including newlines, is field content.
func (p *Parser) parseQuoted(src []byte, pos int) (Field, int, bool, error) {
	var buf []byte
	i := pos + 1
	for {
		if i >= len(src) {
			return Field{}, i, true, fmt.Errorf("unterminated quoted field at offset %d", pos)
		}
		if src[i] == p.Quote {
			if i+1 < len(src) && src[i+1] == p.Quote {
				buf = append(buf, p.Quote)
				i += 2
				continue
			}
			i++
			break
		}
		buf = append(buf, src[i])
		i++
	}
	f := Field{Value: string(buf), Present: true, Empty: len(buf) == 0, Quoted: true}
	if i < len(src) && src[i] != p.Comma && src[i] != '\r' && src[i] != '\n' {
		return Field{}, i, true, fmt.Errorf("unexpected byte %q after closing quote at offset %d", src[i], i)
	}
	return p.afterField(src, i, f)
}

// afterField consumes the separator or record terminator at i.
func (p *Parser) afterField(src []byte, i int, f Field) (Field, int, bool, error) {
	if i >= len(src) {
		return f, i, true, nil
	}
	switch src[i] {
	case p.Comma:
		return f, i + 1, false, nil
	case '\r':
		if i+1 < len(src) && src[i+1] == '\n' {
			return f, i + 2, true, nil
		}
		return f, i + 1, true, nil
	default: // '\n'
		return f, i + 1, true, nil
	}
}
