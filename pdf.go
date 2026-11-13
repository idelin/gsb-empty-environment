package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Signature describes a PDF signature dictionary found in a document.
type Signature struct {
	// ObjectNumber and Generation identify the indirect object that contains
	// the signature dictionary when that information is available.
	ObjectNumber int
	Generation   int

	// ByteRange is the /ByteRange array exactly as stored in the dictionary.
	ByteRange ByteRange

	// ContentsSpan is the full /Contents string token, including its
	// delimiters.  ValueSpan is the span between the delimiters.
	ContentsSpan Span
	ValueSpan    Span

	// Contents is the decoded signature value.  RawContents is the raw PDF
	// string token, normally a hexadecimal string.
	Contents    []byte
	RawContents []byte

	// DictionarySpan is the byte span of the signature dictionary.
	DictionarySpan Span

	// ParseErr records a problem encountered while interpreting the dictionary.
	ParseErr error

	documentSize int64
	order        int
}

// Document is a parsed PDF document.
type Document struct {
	// Data is the complete current revision of the document.
	Data []byte

	// Signatures contains every signature dictionary found while scanning the
	// file, including dictionaries from earlier incremental revisions.
	Signatures []Signature
}

// FileSize returns the number of bytes in the current revision.
func (d *Document) FileSize() int64 {
	if d == nil {
		return 0
	}
	return int64(len(d.Data))
}

// Parse parses a PDF document and extracts its signature dictionaries.
//
// Parsing is deliberately independent of the cross-reference table: signature
// dictionaries from every revision remain visible in the physical byte stream,
// and the coverage check must therefore consider all of them.  Stream bodies
// are skipped so binary stream data cannot masquerade as a dictionary.
func Parse(data []byte) (*Document, error) {
	p := newPDFParser(data)
	if err := p.parseObjects(); err != nil {
		return nil, err
	}

	doc := &Document{
		Data:       data,
		Signatures: p.extractSignatures(),
	}
	for i := range doc.Signatures {
		doc.Signatures[i].documentSize = int64(len(data))
	}
	return doc, nil
}

// ParseDocument is an alias for Parse.
func ParseDocument(data []byte) (*Document, error) {
	return Parse(data)
}

// ParseReader reads all bytes from r and parses them as a PDF document.
func ParseReader(r io.Reader) (*Document, error) {
	if r == nil {
		return nil, fmt.Errorf("pdf: nil reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Validate parses data and reports whether at least one signature covers the
// complete current revision.
//
// A document with no signatures, or with only signatures whose byte ranges end
// at an earlier revision, is rejected.
func Validate(data []byte) error {
	doc, err := Parse(data)
	if err != nil {
		return err
	}
	return doc.Validate()
}

// ValidateDocument is an alias for Validate.
func ValidateDocument(data []byte) error {
	return Validate(data)
}

// Check is an alias for Validate.
func Check(data []byte) error {
	return Validate(data)
}

// ValidateSignatures returns the coverage result for every signature.  The
// returned error is non-nil only when the document cannot be parsed.
func ValidateSignatures(data []byte) ([]Validation, error) {
	doc, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return doc.Results(), nil
}

// CheckSignatures is an alias for ValidateSignatures.
func CheckSignatures(data []byte) ([]Validation, error) {
	return ValidateSignatures(data)
}

// Validation is the coverage result for one signature.
type Validation struct {
	Signature Signature
	Err       error
}

// OK reports whether the signature covers the current revision.
func (v Validation) OK() bool {
	return v.Err == nil
}

// Results validates every signature in the document.
func (d *Document) Results() []Validation {
	if d == nil {
		return nil
	}
	results := make([]Validation, 0, len(d.Signatures))
	for _, signature := range d.Signatures {
		results = append(results, Validation{
			Signature: signature,
			Err:       signature.Validate(d.FileSize()),
		})
	}
	return results
}

// CurrentSignatures returns the signatures that cover the complete current
// revision.
func (d *Document) CurrentSignatures() []Signature {
	if d == nil {
		return nil
	}
	var current []Signature
	for _, signature := range d.Signatures {
		if signature.Covers(d.FileSize()) {
			current = append(current, signature)
		}
	}
	return current
}

// Validate succeeds when at least one signature covers the current revision.
func (d *Document) Validate() error {
	if d == nil || len(d.Signatures) == 0 {
		return ErrNoSignatures
	}

	var failures []error
	for _, signature := range d.Signatures {
		err := signature.Validate(d.FileSize())
		if err == nil {
			return nil
		}
		failures = append(failures, err)
	}

	return fmt.Errorf("%w: %w", ErrNoCurrentSignature, errors.Join(failures...))
}

// ValidateSignature checks one signature against the current revision.
func (d *Document) ValidateSignature(signature Signature) error {
	if d == nil {
		return ErrNoSignatures
	}
	return signature.Validate(d.FileSize())
}

// ValidateSignatureByteRange checks a signature against the complete data of
// the current document revision.
func ValidateSignatureByteRange(data []byte, signature Signature) error {
	return signature.Validate(int64(len(data)))
}

// ---------------------------------------------------------------------------
// PDF lexer and object parser

type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokNumber
	tokName
	tokKeyword
	tokString
	tokHexString
	tokArrayOpen
	tokArrayClose
	tokDictOpen
	tokDictClose
)

type token struct {
	kind       tokenKind
	text       string
	start, end int64
}

type pdfLexer struct {
	data     []byte
	pos      int
	buffered []token
}

func newPDFLexer(data []byte) *pdfLexer {
	return &pdfLexer{data: data}
}

func (l *pdfLexer) next() (token, error) {
	if len(l.buffered) > 0 {
		tok := l.buffered[0]
		l.buffered = l.buffered[1:]
		return tok, nil
	}
	return l.scan()
}

func (l *pdfLexer) peekN(n int) ([]token, error) {
	for len(l.buffered) < n {
		tok, err := l.scan()
		if err != nil {
			return nil, err
		}
		l.buffered = append(l.buffered, tok)
	}
	return l.buffered[:n], nil
}

func (l *pdfLexer) consumeBuffered(n int) {
	if n > len(l.buffered) {
		n = len(l.buffered)
	}
	l.buffered = l.buffered[n:]
}

func (l *pdfLexer) scan() (token, error) {
	l.skipWhitespaceAndComments()
	if l.pos >= len(l.data) {
		return token{kind: tokEOF, start: int64(l.pos), end: int64(l.pos)}, nil
	}

	start := l.pos
	c := l.data[l.pos]

	switch {
	case c == '<' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '<':
		l.pos += 2
		return token{kind: tokDictOpen, text: "<<", start: int64(start), end: int64(l.pos)}, nil
	case c == '<':
		l.pos++
		for l.pos < len(l.data) && l.data[l.pos] != '>' {
			l.pos++
		}
		if l.pos < len(l.data) {
			l.pos++
		}
		return token{kind: tokHexString, text: string(l.data[start:l.pos]), start: int64(start), end: int64(l.pos)}, nil
	case c == '(':
		end, err := scanLiteralString(l.data, l.pos)
		if err != nil {
			return token{}, err
		}
		l.pos = end
		return token{kind: tokString, text: string(l.data[start:l.pos]), start: int64(start), end: int64(l.pos)}, nil
	case c == '[':
		l.pos++
		return token{kind: tokArrayOpen, text: "[", start: int64(start), end: int64(l.pos)}, nil
	case c == ']':
		l.pos++
		return token{kind: tokArrayClose, text: "]", start: int64(start), end: int64(l.pos)}, nil
	case c == '>':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '>' {
			l.pos += 2
			return token{kind: tokDictClose, text: ">>", start: int64(start), end: int64(l.pos)}, nil
		}
		l.pos++
		return token{kind: tokKeyword, text: ">", start: int64(start), end: int64(l.pos)}, nil
	case c == '/':
		l.pos++
		for l.pos < len(l.data) && !isPDFWhitespace(l.data[l.pos]) && !isPDFDelimiter(l.data[l.pos]) {
			l.pos++
		}
		return token{kind: tokName, text: string(l.data[start:l.pos]), start: int64(start), end: int64(l.pos)}, nil
	case c == '%':
		l.skipWhitespaceAndComments()
		return l.scan()
	case isNumberStart(c):
		l.pos++
		for l.pos < len(l.data) && !isPDFWhitespace(l.data[l.pos]) && !isPDFDelimiter(l.data[l.pos]) {
			l.pos++
		}
		return token{kind: tokNumber, text: string(l.data[start:l.pos]), start: int64(start), end: int64(l.pos)}, nil
	default:
		l.pos++
		for l.pos < len(l.data) && !isPDFWhitespace(l.data[l.pos]) && !isPDFDelimiter(l.data[l.pos]) {
			l.pos++
		}
		return token{kind: tokKeyword, text: string(l.data[start:l.pos]), start: int64(start), end: int64(l.pos)}, nil
	}
}

func (l *pdfLexer) skipWhitespaceAndComments() {
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if isPDFWhitespace(c) {
			l.pos++
			continue
		}
		if c == '%' {
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
			continue
		}
		return
	}
}

func (l *pdfLexer) skipStream() error {
	l.buffered = nil
	idx := bytes.Index(l.data[l.pos:], []byte("endstream"))
	if idx < 0 {
		return fmt.Errorf("pdf: unterminated stream")
	}
	l.pos += idx + len("endstream")
	return nil
}

func scanLiteralString(data []byte, start int) (int, error) {
	depth := 1
	for i := start + 1; i < len(data); {
		switch data[i] {
		case '\\':
			i += 2
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
		i++
	}
	return 0, fmt.Errorf("pdf: unterminated literal string")
}

func isPDFWhitespace(c byte) bool {
	switch c {
	case 0x00, '\t', '\n', '\f', '\r', ' ':
		return true
	default:
		return false
	}
}

func isPDFDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	default:
		return false
	}
}

func isNumberStart(c byte) bool {
	return c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

type valueKind uint8

const (
	valNull valueKind = iota
	valBoolean
	valNumber
	valString
	valName
	valArray
	valDict
	valRef
	valKeyword
)

type pdfRef struct {
	number     int
	generation int
}

type pdfValue struct {
	kind   valueKind
	span   Span
	number int64
	real   float64
	isInt  bool
	name   string
	text   []byte
	raw    []byte
	array  []*pdfValue
	dict   map[string]*pdfValue
	keys   []string
	ref    pdfRef
}

type pdfObject struct {
	number     int
	generation int
	root       *pdfValue
	span       Span
	order      int
}

type pdfParser struct {
	data      []byte
	lexer     *pdfLexer
	objects   []*pdfObject
	objectMap map[int]*pdfObject
}

func newPDFParser(data []byte) *pdfParser {
	return &pdfParser{
		data:      data,
		lexer:     newPDFLexer(data),
		objectMap: make(map[int]*pdfObject),
	}
}

func (p *pdfParser) parseObjects() error {
	var previous []token

	for {
		tok, err := p.lexer.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			break
		}

		if tok.kind != tokKeyword {
			previous = appendToken(previous, tok)
			continue
		}

		switch tok.text {
		case "obj":
			number, generation := objectHeader(previous)
			root, err := p.parseValue(0)
			if err != nil {
				return err
			}
			obj := &pdfObject{
				number:     number,
				generation: generation,
				root:       root,
				span:       Span{Start: int64(tok.start), End: root.span.End},
				order:      len(p.objects),
			}
			p.objects = append(p.objects, obj)

			next, err := p.lexer.peekN(1)
			if err != nil {
				return err
			}
			if next[0].kind == tokKeyword && next[0].text == "stream" {
				_, _ = p.lexer.next()
				if err := p.lexer.skipStream(); err != nil {
					return err
				}
			}

			for {
				next, err := p.lexer.next()
				if err != nil {
					return err
				}
				if next.kind == tokEOF {
					break
				}
				if next.kind == tokKeyword && next.text == "endobj" {
					break
				}
				if next.kind == tokKeyword && next.text == "stream" {
					if err := p.lexer.skipStream(); err != nil {
						return err
					}
				}
			}
			previous = nil
		case "stream":
			if err := p.lexer.skipStream(); err != nil {
				return err
			}
			previous = nil
		default:
			previous = appendToken(previous, tok)
		}
	}

	for _, obj := range p.objects {
		// Later revisions define the same object number again; the newest
		// definition is the one an indirect reference resolves to.
		p.objectMap[obj.number] = obj
	}
	return nil
}

func appendToken(previous []token, tok token) []token {
	previous = append(previous, tok)
	if len(previous) > 2 {
		previous = previous[len(previous)-2:]
	}
	return previous
}

func objectHeader(previous []token) (int, int) {
	if len(previous) != 2 {
		return 0, 0
	}
	number, okNumber := tokenInt(previous[0])
	generation, okGeneration := tokenInt(previous[1])
	if !okNumber || !okGeneration {
		return 0, 0
	}
	return int(number), int(generation)
}

func tokenInt(tok token) (int64, bool) {
	if tok.kind != tokNumber {
		return 0, false
	}
	n, err := strconv.ParseInt(tok.text, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (p *pdfParser) parseValue(depth int) (*pdfValue, error) {
	if depth > 100 {
		return nil, fmt.Errorf("pdf: object nesting too deep")
	}

	tok, err := p.lexer.next()
	if err != nil {
		return nil, err
	}

	switch tok.kind {
	case tokDictOpen:
		dict := &pdfValue{
			kind: valDict,
			span: Span{Start: int64(tok.start)},
			dict: make(map[string]*pdfValue),
		}
		for {
			key, err := p.lexer.next()
			if err != nil {
				return nil, err
			}
			if key.kind == tokDictClose {
				dict.span.End = int64(key.end)
				return dict, nil
			}
			if key.kind != tokName {
				return nil, fmt.Errorf("pdf: expected dictionary key at offset %d", key.start)
			}
			decoded := decodePDFName(key.text)
			val, err := p.parseValue(depth + 1)
			if err != nil {
				return nil, err
			}
			if _, exists := dict.dict[decoded]; !exists {
				dict.keys = append(dict.keys, decoded)
			}
			dict.dict[decoded] = val
		}

	case tokArrayOpen:
		array := &pdfValue{
			kind: valArray,
			span: Span{Start: int64(tok.start)},
		}
		for {
			next, err := p.lexer.peekN(1)
			if err != nil {
				return nil, err
			}
			if next[0].kind == tokArrayClose {
				closeTok, _ := p.lexer.next()
				array.span.End = int64(closeTok.end)
				return array, nil
			}
			val, err := p.parseValue(depth + 1)
			if err != nil {
				return nil, err
			}
			array.array = append(array.array, val)
		}

	case tokNumber:
		val := &pdfValue{
			kind:   valNumber,
			span:   Span{Start: int64(tok.start), End: int64(tok.end)},
			number: 0,
			real:   0,
			isInt:  false,
		}
		if !strings.Contains(tok.text, ".") {
			if n, err := strconv.ParseInt(tok.text, 10, 64); err == nil {
				val.number = n
				val.real = float64(n)
				val.isInt = true
			} else if f, err := strconv.ParseFloat(tok.text, 64); err == nil {
				val.real = f
			} else {
				return nil, fmt.Errorf("pdf: invalid number %q", tok.text)
			}
		} else {
			f, err := strconv.ParseFloat(tok.text, 64)
			if err != nil {
				return nil, fmt.Errorf("pdf: invalid number %q", tok.text)
			}
			val.real = f
		}

		// A non-negative integer followed by "generation R" is an indirect
		// reference.  Leave the lookahead tokens buffered if it is not.
		if val.isInt && val.number >= 0 {
			peeked, err := p.lexer.peekN(2)
			if err == nil && len(peeked) == 2 &&
				peeked[0].kind == tokNumber &&
				peeked[1].kind == tokKeyword &&
				peeked[1].text == "R" {
				generation, ok := tokenInt(peeked[0])
				if ok && generation >= 0 {
					p.lexer.consumeBuffered(2)
					return &pdfValue{
						kind: valRef,
						span: Span{Start: int64(tok.start), End: int64(peeked[1].end)},
						ref:  pdfRef{number: int(val.number), generation: int(generation)},
					}, nil
				}
			}
		}
		return val, nil

	case tokName:
		return &pdfValue{
			kind: valName,
			span: Span{Start: int64(tok.start), End: int64(tok.end)},
			name: decodePDFName(tok.text),
		}, nil

	case tokString:
		raw := p.data[tok.start:tok.end]
		return &pdfValue{
			kind: valString,
			span: Span{Start: int64(tok.start), End: int64(tok.end)},
			text: decodePDFLiteral(raw),
			raw:  append([]byte(nil), raw...),
		}, nil

	case tokHexString:
		raw := p.data[tok.start:tok.end]
		return &pdfValue{
			kind: valString,
			span: Span{Start: int64(tok.start), End: int64(tok.end)},
			text: decodeHexString(raw),
			raw:  append([]byte(nil), raw...),
		}, nil

	case tokKeyword:
		switch tok.text {
		case "true", "false":
			return &pdfValue{
				kind:   valBoolean,
				span:   Span{Start: int64(tok.start), End: int64(tok.end)},
				number: map[bool]int64{false: 0, true: 1}[tok.text == "true"],
			}, nil
		case "null":
			return &pdfValue{
				kind: valNull,
				span: Span{Start: int64(tok.start), End: int64(tok.end)},
			}, nil
		default:
			return &pdfValue{
				kind: valKeyword,
				span: Span{Start: int64(tok.start), End: int64(tok.end)},
				name: tok.text,
			}, nil
		}
	}

	return nil, fmt.Errorf("pdf: unexpected token %q at offset %d", tok.text, tok.start)
}

func decodePDFName(raw string) string {
	if !strings.HasPrefix(raw, "/") {
		return raw
	}
	name := raw[1:]
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '#' && i+2 < len(name) {
			if v, err := strconv.ParseUint(name[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

func decodeHexString(raw []byte) []byte {
	if len(raw) < 2 {
		return nil
	}
	inner := raw[1:]
	if len(inner) > 0 && inner[len(inner)-1] == '>' {
		inner = inner[:len(inner)-1]
	}
	inner = bytes.Map(func(r rune) rune {
		if strings.ContainsRune(" \t\r\n\f\v", r) {
			return -1
		}
		return r
	}, inner)

	out := make([]byte, 0, len(inner)/2+1)
	for i := 0; i < len(inner); i += 2 {
		hi := hexNibble(inner[i])
		if hi < 0 {
			continue
		}
		lo := byte(0)
		if i+1 < len(inner) {
			lo = byte(max(hexNibble(inner[i+1]), 0))
		}
		out = append(out, byte(hi<<4)|lo)
	}
	return out
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

func decodePDFLiteral(raw []byte) []byte {
	if len(raw) < 2 || raw[0] != '(' || raw[len(raw)-1] != ')' {
		return nil
	}
	body := raw[1 : len(raw)-1]
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out = append(out, body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case '(', ')', '\\':
			out = append(out, body[i])
		case '\r':
			if i+1 < len(body) && body[i+1] == '\n' {
				i++
			}
		case '\n':
			// A backslash followed by a line ending is a continuation.
		default:
			if body[i] >= '0' && body[i] <= '7' {
				value := body[i] - '0'
				for j := 0; j < 2 && i+1 < len(body); j++ {
					next := body[i+1]
					if next < '0' || next > '7' {
						break
					}
					i++
					value = value*8 + next - '0'
				}
				out = append(out, value)
			} else {
				out = append(out, body[i])
			}
		}
	}
	return out
}

func (p *pdfParser) extractSignatures() []Signature {
	var signatures []Signature

	var walk func(value *pdfValue, obj *pdfObject)
	walk = func(value *pdfValue, obj *pdfObject) {
		if value == nil {
			return
		}
		switch value.kind {
		case valDict:
			if signature, ok := p.signatureFromDict(value, obj); ok {
				signature.documentSize = int64(len(p.data))
				signature.order = len(signatures)
				signatures = append(signatures, signature)
			}
			for _, value := range value.dict {
				walk(value, obj)
			}
		case valArray:
			for _, value := range value.array {
				walk(value, obj)
			}
		}
	}

	for _, obj := range p.objects {
		walk(obj.root, obj)
	}
	return signatures
}

func (p *pdfParser) signatureFromDict(dict *pdfValue, obj *pdfObject) (Signature, bool) {
	byteRangeValue, hasByteRange := dict.dict["ByteRange"]
	contentsValue, hasContents := dict.dict["Contents"]
	isSignatureType := false
	if typeValue := p.resolve(dict.dict["Type"]); typeValue != nil &&
		typeValue.kind == valName && typeValue.name == "Sig" {
		isSignatureType = true
	}
	_, hasSubFilter := dict.dict["SubFilter"]
	if !hasByteRange && !isSignatureType && !hasSubFilter {
		return Signature{}, false
	}

	byteRangeValue = p.resolve(byteRangeValue)
	contentsValue = p.resolve(contentsValue)

	signature := Signature{
		ObjectNumber:   0,
		Generation:     0,
		DictionarySpan: dict.span,
	}
	if obj != nil {
		signature.ObjectNumber = obj.number
		signature.Generation = obj.generation
	}

	if byteRangeValue != nil && byteRangeValue.kind == valArray {
		for _, item := range byteRangeValue.array {
			item = p.resolve(item)
			if item == nil || item.kind != valNumber || !item.isInt {
				signature.ParseErr = fmt.Errorf("%w: non-integer /ByteRange entry", ErrInvalidByteRange)
				break
			}
			signature.ByteRange = append(signature.ByteRange, item.number)
		}
	} else if hasByteRange {
		signature.ParseErr = fmt.Errorf("%w: /ByteRange is not an array", ErrInvalidByteRange)
	}

	if contentsValue != nil && contentsValue.kind == valString {
		signature.ContentsSpan = contentsValue.span
		signature.ValueSpan = contentsValue.span
		raw := contentsValue.raw
		if len(raw) >= 2 &&
			((raw[0] == '<' && raw[len(raw)-1] == '>') ||
				(raw[0] == '(' && raw[len(raw)-1] == ')')) {
			signature.ValueSpan = Span{Start: contentsValue.span.Start + 1, End: contentsValue.span.End - 1}
		}
		signature.RawContents = append([]byte(nil), raw...)
		signature.Contents = append([]byte(nil), contentsValue.text...)
	} else if hasContents {
		signature.ParseErr = fmt.Errorf("%w: /Contents is not a string", ErrMalformedSignature)
	}

	return signature, true
}

func (p *pdfParser) resolve(value *pdfValue) *pdfValue {
	for depth := 0; value != nil && value.kind == valRef && depth < 32; depth++ {
		obj := p.objectMap[value.ref.number]
		if obj == nil {
			return nil
		}
		value = obj.root
	}
	return value
}
