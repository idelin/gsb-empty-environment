package pdfscan

import (
	"errors"
	"fmt"
)

const maxParseDepth = 256

// parser turns a byte slice into the generic PDF value tree. It performs
// no rendering and no object resolution; references stay as KindRef values
// and are resolved later against the latest effective object version.
type parser struct {
	lx    *lexer
	buf   [3]token
	count int
	depth int
}

func newParser(data []byte) *parser {
	return &parser{lx: &lexer{data: data}}
}

func (p *parser) peekAt(n int) (token, error) {
	for p.count <= n {
		t, err := p.lx.next()
		if err != nil {
			return token{}, err
		}
		p.buf[p.count] = t
		p.count++
	}
	return p.buf[n], nil
}

func (p *parser) peek() (token, error) { return p.peekAt(0) }

func (p *parser) consume() token {
	if p.count == 0 {
		return token{kind: tokEOF}
	}
	t := p.buf[0]
	copy(p.buf[:], p.buf[1:p.count])
	p.buf[p.count-1] = token{}
	p.count--
	return t
}

// parseIndirect parses "N G obj <value> [stream ... endstream] endobj".
// The optional endobj keyword is tolerated because some producers omit it.
func (p *parser) parseIndirect() (num, gen int, value *Value, err error) {
	t, err := p.peek()
	if err != nil {
		return 0, 0, nil, err
	}
	if t.kind != tokNumber {
		return 0, 0, nil, fmt.Errorf("pdfscan: expected object number at offset %d", t.pos)
	}
	num = int(t.num)
	p.consume()

	t, err = p.peek()
	if err != nil {
		return 0, 0, nil, err
	}
	if t.kind != tokNumber {
		return 0, 0, nil, fmt.Errorf("pdfscan: expected generation at offset %d", t.pos)
	}
	gen = int(t.num)
	p.consume()

	t, err = p.peek()
	if err != nil {
		return 0, 0, nil, err
	}
	if t.kind != tokKeyword || t.text != "obj" {
		return 0, 0, nil, fmt.Errorf("pdfscan: expected obj keyword at offset %d", t.pos)
	}
	p.consume()

	value, err = p.parseValue()
	if err != nil {
		return 0, 0, nil, err
	}
	value.ObjNum = num

	if value.Kind == KindDict {
		st, ok, serr := p.tryAttachStream(value.Dict, num)
		if serr != nil {
			return 0, 0, nil, serr
		}
		if ok {
			value = &Value{Kind: KindStream, Stream: st, ObjNum: num}
		}
	}

	if t, err = p.peek(); err == nil && t.kind == tokKeyword && t.text == "endobj" {
		p.consume()
	}
	return num, gen, value, nil
}

// tryAttachStream checks for the stream keyword following a dictionary and
// slices out the content bytes using /Length when reliable, falling back to
// an endstream scan.
func (p *parser) tryAttachStream(d *Dict, objNum int) (*Stream, bool, error) {
	t, err := p.peek()
	if err != nil {
		return nil, false, err
	}
	if t.kind != tokKeyword || t.text != "stream" {
		return nil, false, nil
	}
	streamPos := t.pos
	p.consume()

	data := p.lx.data
	pos := p.lx.pos
	if pos < len(data) && data[pos] == '\r' {
		pos++
	}
	if pos < len(data) && data[pos] == '\n' {
		pos++
	}

	length := -1
	if lv := d.Get("Length"); lv != nil {
		if lv.Kind == KindInt {
			length = int(lv.Int)
		}
		// Indirect /Length values are left alone: carved objects must not
		// rely on cross-reference state, and endstream is unambiguous here.
	}

	start := pos
	var end int
	if length >= 0 && start+length <= len(data) {
		candidate := start + length
		if hasEndStreamAt(data, candidate) {
			end = candidate
		}
	}
	if end == 0 {
		idx := indexEndStream(data, start)
		if idx < 0 {
			return nil, false, fmt.Errorf("pdfscan: object %d has stream without endstream", objNum)
		}
		end = idx
	}

	raw := data[start:end]
	p.lx.pos = end
	// Re-sync the lookahead buffer to the new lexer position.
	p.count = 0

	return &Stream{Dict: d, Raw: raw, ObjNum: objNum, Offset: streamPos}, true, nil
}

func hasEndStreamAt(data []byte, pos int) bool {
	// Content is followed by EOL then endstream.
	for pos < len(data) && (data[pos] == '\r' || data[pos] == '\n') {
		pos++
	}
	return pos+9 <= len(data) && string(data[pos:pos+9]) == "endstream"
}

func indexEndStream(data []byte, from int) int {
	for i := from; i+9 <= len(data); i++ {
		if string(data[i:i+9]) == "endstream" && (i == 0 || isWhite(data[i-1])) {
			return i
		}
	}
	return -1
}

func (p *parser) parseValue() (*Value, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, errors.New("pdfscan: object nesting too deep")
	}

	t, err := p.peek()
	if err != nil {
		return nil, err
	}
	switch t.kind {
	case tokKeyword:
		p.consume()
		switch t.text {
		case "true":
			return &Value{Kind: KindBool, Bool: true}, nil
		case "false":
			return &Value{Kind: KindBool, Bool: false}, nil
		case "null":
			return &Value{Kind: KindNull}, nil
		default:
			return nil, fmt.Errorf("pdfscan: unexpected keyword %q at offset %d", t.text, t.pos)
		}
	case tokNumber:
		return p.parseNumberOrRef(), nil
	case tokName:
		p.consume()
		return &Value{Kind: KindName, Str: []byte(t.text)}, nil
	case tokString:
		p.consume()
		return &Value{Kind: KindString, Str: []byte(t.text)}, nil
	case tokHex:
		p.consume()
		return &Value{Kind: KindString, Str: []byte(t.text)}, nil
	case tokLBracket:
		return p.parseArray()
	case tokLBrace:
		return p.parseDict()
	case tokEOF:
		return nil, errors.New("pdfscan: unexpected end of file while parsing object")
	default:
		return nil, fmt.Errorf("pdfscan: unexpected token at offset %d", t.pos)
	}
}

func (p *parser) parseNumberOrRef() *Value {
	first := p.consume()
	t1, err := p.peekAt(0)
	if err == nil && t1.kind == tokNumber && !isRealToken(p.lx.data, t1.pos) {
		// A reference requires the object and generation numbers to be
		// separate tokens joined only by whitespace. Anything else (for
		// example a "/" delimiter between array elements) means these are
		// unrelated numbers.
		if onlyWhiteBetween(p.lx.data, numberEnd(p.lx.data, first.pos), t1.pos) {
			t2, err := p.peekAt(1)
			if err == nil && t2.kind == tokKeyword && t2.text == "R" &&
				isContiguousRef(p.lx.data, first, t1, t2) {
				p.consume() // generation
				p.consume() // R
				return &Value{Kind: KindRef, RefNum: int(first.num), RefGen: int(t1.num)}
			}
		}
	}
	// Distinguish integer from real using the presence of a dot in source.
	if isRealToken(p.lx.data, first.pos) {
		return &Value{Kind: KindReal, Real: first.real}
	}
	return &Value{Kind: KindInt, Int: first.num}
}

// isContiguousRef verifies that "N G R" consists of three separate tokens
// with nothing but whitespace between them. This rejects both glued forms
// like "R/Name" and unrelated sequences such as "2 /Index" where the
// generation slot would otherwise swallow the next number.
func isContiguousRef(data []byte, first, gen, r token) bool {
	if r.text != "R" || r.pos <= gen.pos || gen.pos <= first.pos {
		return false
	}
	return onlyWhiteBetween(data, numberEnd(data, first.pos), gen.pos) &&
		onlyWhiteBetween(data, numberEnd(data, gen.pos), r.pos) &&
		(r.pos+1 >= len(data) || isWhite(data[r.pos+1]) || isDelim(data[r.pos+1]))
}

// numberEnd returns the byte offset immediately after the integer that
// starts at pos.
func numberEnd(data []byte, pos int) int {
	end := pos
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}
	return end
}

func onlyWhiteBetween(data []byte, from, to int) bool {
	for i := from; i < to; i++ {
		if !isWhite(data[i]) {
			return false
		}
	}
	return true
}

func isRealToken(data []byte, pos int) bool {
	for i := pos; i < len(data); i++ {
		c := data[i]
		if isWhite(c) || isDelim(c) {
			return false
		}
		if c == '.' {
			return true
		}
	}
	return false
}

func (p *parser) parseArray() (*Value, error) {
	p.consume() // [
	arr := &Value{Kind: KindArray}
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind == tokRBracket {
			p.consume()
			return arr, nil
		}
		if t.kind == tokEOF {
			return nil, errors.New("pdfscan: unterminated array")
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		arr.Array = append(arr.Array, v)
	}
}

func (p *parser) parseDict() (*Value, error) {
	p.consume() // <<
	d := &Dict{Map: map[string]*Value{}}
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind == tokRBrace {
			p.consume()
			return &Value{Kind: KindDict, Dict: d}, nil
		}
		if t.kind == tokEOF {
			return nil, errors.New("pdfscan: unterminated dictionary")
		}
		if t.kind != tokName {
			return nil, fmt.Errorf("pdfscan: expected dictionary key name at offset %d", t.pos)
		}
		p.consume()
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if _, exists := d.Map[t.text]; !exists {
			d.Keys = append(d.Keys, t.text)
		}
		d.Map[t.text] = v
	}
}

// parseObjectStreamBody parses N objects out of an already-inflated object
// stream, using the N/First header offsets to locate each inner object.
func parseObjectStreamBody(body []byte, count, first int) (map[int]*Value, error) {
	header := newParser(body[:clamp(first, len(body))])
	nums := make([]int, 0, count)
	offsets := make([]int, 0, count)
	for i := 0; i < count; i++ {
		v, err := header.parseValue()
		if err != nil {
			return nil, fmt.Errorf("pdfscan: object stream header: %w", err)
		}
		if v.Kind != KindInt {
			return nil, errors.New("pdfscan: object stream header expects integer object numbers")
		}
		nums = append(nums, int(v.Int))
	}
	for i := 0; i < count; i++ {
		v, err := header.parseValue()
		if err != nil {
			return nil, fmt.Errorf("pdfscan: object stream header: %w", err)
		}
		if v.Kind != KindInt {
			return nil, errors.New("pdfscan: object stream header expects integer offsets")
		}
		offsets = append(offsets, int(v.Int))
	}

	out := make(map[int]*Value, count)
	for i, objNum := range nums {
		start := first + offsets[i]
		if start < 0 || start > len(body) {
			return nil, fmt.Errorf("pdfscan: object stream offset out of range for object %d", objNum)
		}
		// Each sub-parser stops at the natural end of its value; trailing
		// bytes belong to subsequent objects.
		op := newParser(body[start:])
		v, err := op.parseValue()
		if err != nil {
			return nil, fmt.Errorf("pdfscan: compressed object %d: %w", objNum, err)
		}
		v.ObjNum = objNum
		out[objNum] = v
	}
	return out, nil
}

func clamp(v, limit int) int {
	if v < 0 {
		return 0
	}
	if v > limit {
		return limit
	}
	return v
}
