package streamguard

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EventKind identifies a low-level XML token event.
type EventKind int

const (
	EventNone EventKind = iota
	EventStartElement
	EventEndElement
	EventCharData
	EventComment
	EventProcInst
)

// Event is one incremental XML token. String fields are copies; consuming an
// event never exposes parser-owned mutable memory.
type Event struct {
	Kind        EventKind
	Name        Name
	Attrs       []Attr
	Text        string
	SelfClosing bool
}

// Attr is a parsed XML attribute.
type Attr struct {
	Name  Name
	Value string
}

// Parser is a bounded, incremental, non-validating XML tokenizer. It does not
// read DTDs or expand custom entities. It is not safe for concurrent use.
type Parser struct {
	limits Limits
	buf    []byte

	stack  []Name
	scopes []map[string]string

	failed error
}

// NewParser creates a safe parser. A zero Limits value is normalized.
func NewParser(limits ...Limits) *Parser {
	cfg := DefaultLimits()
	if len(limits) > 0 {
		cfg = limits[0].Normalize()
	}
	return &Parser{limits: cfg}
}

// Write appends raw UTF-8 bytes. Incomplete UTF-8 and XML tokens may remain
// buffered until later writes.
func (p *Parser) Write(data []byte) (int, error) {
	if p.failed != nil {
		return 0, p.failed
	}
	if len(data) == 0 {
		return 0, nil
	}
	if len(p.buf)+len(data) > p.limits.MaxBufferBytes {
		err := limitError(ErrBufferLimit, p.limits.MaxBufferBytes, len(p.buf)+len(data))
		p.fail(err)
		return 0, err
	}

	p.buf = append(p.buf, data...)
	if err := validateIncrementalUTF8(p.buf); err != nil {
		p.fail(err)
		return 0, err
	}
	return len(data), nil
}

// Buffered returns the number of unconsumed bytes.
func (p *Parser) Buffered() int { return len(p.buf) }

// Failed returns the terminal parser error, if any.
func (p *Parser) Failed() error { return p.failed }

// Reset discards all pending bytes and parser state. It is used at the
// StartTLS upgrade boundary.
func (p *Parser) Reset() {
	if p.buf != nil {
		for i := range p.buf {
			p.buf[i] = 0
		}
	}
	p.buf = nil
	p.stack = nil
	p.scopes = nil
	p.failed = nil
}

// Next returns the next complete token. It returns ErrNeedMore when additional
// input is required.
func (p *Parser) Next() (Event, error) {
	if p.failed != nil {
		return Event{}, p.failed
	}

	event, size, err := p.scan()
	if err != nil {
		if !errors.Is(err, ErrNeedMore) {
			p.fail(err)
		}
		return Event{}, err
	}
	p.consume(size)
	return event, nil
}

func (p *Parser) fail(err error) {
	if p.failed == nil {
		p.failed = err
	}
}

func (p *Parser) consume(size int) {
	if size <= 0 || size > len(p.buf) {
		return
	}
	// Zero consumed plaintext so credential fragments do not linger in the
	// active backing array after the slice is advanced.
	for i := 0; i < size; i++ {
		p.buf[i] = 0
	}
	p.buf = p.buf[size:]
}

func validateIncrementalUTF8(b []byte) error {
	i := 0
	for i < len(b) {
		if b[i] < 0x80 {
			i++
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			if !utf8.FullRune(b[i:]) {
				return nil
			}
			return ErrInvalidUTF8
		}
		i += size
	}
	return nil
}

func (p *Parser) scan() (Event, int, error) {
	b := p.buf
	if len(b) == 0 {
		return Event{}, 0, ErrNeedMore
	}

	if b[0] != '<' {
		return p.scanText()
	}
	if len(b) < 2 {
		return Event{}, 0, ErrNeedMore
	}

	switch b[1] {
	case '?':
		return p.scanProcInst()
	case '!':
		return p.scanDeclaration()
	case '/':
		return p.scanEndElement()
	default:
		return p.scanStartElement()
	}
}

func (p *Parser) scanProcInst() (Event, int, error) {
	b := p.buf
	closeIdx := bytes.Index(b, []byte("?>"))
	if closeIdx < 0 {
		if len(b)-2 > p.limits.MaxProcessingInstBytes {
			return Event{}, 0, limitError(
				ErrProcessingInstLimit,
				p.limits.MaxProcessingInstBytes,
				len(b)-2,
			)
		}
		return Event{}, 0, ErrNeedMore
	}
	end := closeIdx + 2
	body := b[2:closeIdx]
	if len(body) > p.limits.MaxProcessingInstBytes {
		return Event{}, 0, limitError(
			ErrProcessingInstLimit,
			p.limits.MaxProcessingInstBytes,
			len(body),
		)
	}
	if err := validateXMLChars(body); err != nil {
		return Event{}, 0, err
	}
	return Event{Kind: EventProcInst, Text: string(body)}, end, nil
}

func (p *Parser) scanDeclaration() (Event, int, error) {
	b := p.buf

	commentOpen := []byte("<!--")
	cdataOpen := []byte("<![CDATA[")

	if bytes.HasPrefix(b, commentOpen) {
		return p.scanComment()
	}
	if bytes.HasPrefix(b, cdataOpen) {
		return p.scanCDATA()
	}
	if isPartialPrefix(commentOpen, b) || isPartialPrefix(cdataOpen, b) {
		return Event{}, 0, ErrNeedMore
	}

	// Every other <! markup declaration is DTD/internal-subset territory.
	// Reject DOCTYPE and ENTITY explicitly, and do not accept ELEMENT,
	// ATTLIST, NOTATION, or other DTD declarations either.
	word, wordLen, complete := scanASCIIWord(b[2:])
	if wordLen == 0 {
		if len(b) <= 3 {
			return Event{}, 0, ErrNeedMore
		}
		return Event{}, 0, ErrMalformedXML
	}
	if strings.EqualFold(word, "DOCTYPE") {
		return Event{}, 0, ErrDTDForbidden
	}
	if strings.EqualFold(word, "ENTITY") {
		return Event{}, 0, ErrEntityDeclForbidden
	}
	if !complete {
		return Event{}, 0, ErrNeedMore
	}
	return Event{}, 0, ErrDTDForbidden
}

func isPartialPrefix(prefix, b []byte) bool {
	return len(b) < len(prefix) && bytes.HasPrefix(prefix, b)
}

func scanASCIIWord(b []byte) (string, int, bool) {
	n := 0
	for n < len(b) && isASCIILetter(b[n]) {
		n++
	}
	if n == 0 {
		return "", 0, false
	}
	complete := n < len(b)
	return string(b[:n]), n, complete
}

func isASCIILetter(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func (p *Parser) scanComment() (Event, int, error) {
	b := p.buf
	openLen := len("<!--")
	closeIdx := bytes.Index(b[openLen:], []byte("-->"))
	if closeIdx < 0 {
		if len(b)-openLen > p.limits.MaxCommentBytes {
			return Event{}, 0, limitError(ErrCommentBomb, p.limits.MaxCommentBytes, len(b)-openLen)
		}
		return Event{}, 0, ErrNeedMore
	}

	content := b[openLen : openLen+closeIdx]
	end := openLen + closeIdx + len("-->")
	if len(content) > p.limits.MaxCommentBytes {
		return Event{}, 0, limitError(ErrCommentBomb, p.limits.MaxCommentBytes, len(content))
	}
	if bytes.Contains(content, []byte("--")) {
		return Event{}, 0, ErrMalformedXML
	}
	if err := validateXMLChars(content); err != nil {
		return Event{}, 0, err
	}
	return Event{Kind: EventComment, Text: string(content)}, end, nil
}

func (p *Parser) scanCDATA() (Event, int, error) {
	b := p.buf
	openLen := len("<![CDATA[")
	closeIdx := bytes.Index(b[openLen:], []byte("]]>"))
	if closeIdx < 0 {
		if len(b)-openLen > p.limits.MaxTextBytes {
			return Event{}, 0, limitError(ErrTextLimit, p.limits.MaxTextBytes, len(b)-openLen)
		}
		return Event{}, 0, ErrNeedMore
	}

	content := b[openLen : openLen+closeIdx]
	end := openLen + closeIdx + len("]]>")
	if len(content) > p.limits.MaxTextBytes {
		return Event{}, 0, limitError(ErrTextLimit, p.limits.MaxTextBytes, len(content))
	}
	if err := validateXMLChars(content); err != nil {
		return Event{}, 0, err
	}
	return Event{Kind: EventCharData, Text: string(content)}, end, nil
}

func (p *Parser) scanText() (Event, int, error) {
	b := p.buf
	if b[0] == '&' {
		return p.scanEntityRef()
	}

	n := 0
	for n < len(b) {
		if b[n] == '<' || b[n] == '&' {
			break
		}
		n++
	}
	if complete := completeUTF8PrefixLen(b[:n]); complete < n {
		n = complete
	}
	if n == 0 {
		return Event{}, 0, ErrNeedMore
	}
	text := b[:n]
	if err := validateXMLChars(text); err != nil {
		return Event{}, 0, err
	}
	if bytes.Contains(text, []byte("]]>")) {
		return Event{}, 0, ErrMalformedXML
	}
	return Event{Kind: EventCharData, Text: string(text)}, n, nil
}

func completeUTF8PrefixLen(b []byte) int {
	i := 0
	for i < len(b) {
		if b[i] < 0x80 {
			i++
			continue
		}
		_, size := utf8.DecodeRune(b[i:])
		if size == 1 && !utf8.FullRune(b[i:]) {
			return i
		}
		i += size
	}
	return i
}

var builtinEntities = map[string]string{
	"amp":  "&",
	"lt":   "<",
	"gt":   ">",
	"quot": "\"",
	"apos": "'",
}

func (p *Parser) scanEntityRef() (Event, int, error) {
	b := p.buf
	if len(b) < 2 {
		return Event{}, 0, ErrNeedMore
	}

	if b[1] == '#' {
		i := 2
		hex := false
		if i < len(b) && (b[i] == 'x' || b[i] == 'X') {
			hex = true
			i++
		}
		digitStart := i
		for i < len(b) {
			if hex && isHexDigit(b[i]) || !hex && b[i] >= '0' && b[i] <= '9' {
				i++
				continue
			}
			break
		}
		if i == digitStart {
			if i >= len(b) {
				return Event{}, 0, ErrNeedMore
			}
			return Event{}, 0, ErrMalformedEntity
		}
		if i >= len(b) {
			return Event{}, 0, ErrNeedMore
		}
		if b[i] != ';' {
			return Event{}, 0, ErrMalformedEntity
		}

		number := string(b[digitStart:i])
		base := 10
		bitSize := 32
		if hex {
			base = 16
		}
		value, err := strconv.ParseUint(number, base, bitSize)
		if err != nil || value > utf8.MaxRune {
			return Event{}, 0, ErrMalformedEntity
		}
		r := rune(value)
		if !validXMLRune(r) || utf16Surrogate(r) {
			return Event{}, 0, ErrMalformedEntity
		}
		return Event{Kind: EventCharData, Text: string(r)}, i + 1, nil
	}

	name, nameLen, err := readName(b[1:])
	if err != nil {
		if errors.Is(err, ErrNeedMore) {
			return Event{}, 0, ErrNeedMore
		}
		return Event{}, 0, ErrMalformedEntity
	}
	if 1+nameLen >= len(b) {
		return Event{}, 0, ErrNeedMore
	}
	if b[1+nameLen] != ';' {
		return Event{}, 0, ErrMalformedEntity
	}
	value, ok := builtinEntities[name.Raw]
	if !ok {
		return Event{}, 0, ErrUndeclaredEntity
	}
	return Event{Kind: EventCharData, Text: value}, 1 + nameLen + 1, nil
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func utf16Surrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

func findTagEnd(b []byte) (int, error) {
	var quote byte
	for i := 1; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i, nil
		case '<':
			return 0, ErrMalformedXML
		}
	}
	if quote != 0 {
		return 0, ErrNeedMore
	}
	return 0, ErrNeedMore
}

func (p *Parser) scanStartElement() (Event, int, error) {
	b := p.buf
	tagEnd, err := findTagEnd(b)
	if err != nil {
		return Event{}, 0, err
	}
	tag := b[:tagEnd+1]

	pos := 1 // skip '<'
	name, nameLen, err := readName(tag[pos:])
	if err != nil {
		return Event{}, 0, err
	}
	if nameLen > p.limits.MaxNameBytes || len(name.Raw) > p.limits.MaxNameBytes {
		return Event{}, 0, limitError(ErrNameLimit, p.limits.MaxNameBytes, len(name.Raw))
	}
	pos += nameLen

	attrs := make([]Attr, 0, 4)
	selfClosing := false
	for {
		pos += skipASCIIWhitespace(tag[pos:])
		if pos >= len(tag) {
			return Event{}, 0, ErrMalformedXML
		}

		switch tag[pos] {
		case '>':
			pos++
			goto parsed
		case '/':
			if pos+1 >= len(tag) || tag[pos+1] != '>' {
				return Event{}, 0, ErrMalformedXML
			}
			selfClosing = true
			pos += 2
			goto parsed
		}

		if len(attrs) >= p.limits.MaxAttributes {
			return Event{}, 0, limitError(ErrAttributeLimit, p.limits.MaxAttributes, len(attrs)+1)
		}

		attr, next, err := parseAttribute(tag, pos, p.limits)
		if err != nil {
			return Event{}, 0, err
		}
		for _, existing := range attrs {
			if existing.Name.Raw == attr.Name.Raw {
				return Event{}, 0, ErrMalformedXML
			}
		}
		attrs = append(attrs, attr)
		pos = next
	}

parsed:
	if pos != len(tag) {
		return Event{}, 0, ErrMalformedXML
	}
	if len(p.stack)+1 > p.limits.MaxDepth {
		return Event{}, 0, limitError(ErrDepthExceeded, p.limits.MaxDepth, len(p.stack)+1)
	}

	pushNamespaceScope(p, name, attrs)
	resolveName(&name, p.scopes)
	for i := range attrs {
		resolveAttrName(&attrs[i], p.scopes)
	}

	if selfClosing {
		p.scopes = p.scopes[:len(p.scopes)-1]
	} else {
		p.stack = append(p.stack, name)
	}

	return Event{
		Kind:        EventStartElement,
		Name:        name,
		Attrs:       attrs,
		SelfClosing: selfClosing,
	}, tagEnd + 1, nil
}

func parseAttribute(tag []byte, pos int, limits Limits) (Attr, int, error) {
	name, nameLen, err := readName(tag[pos:])
	if err != nil {
		return Attr{}, 0, err
	}
	if len(name.Raw) > limits.MaxNameBytes {
		return Attr{}, 0, limitError(ErrNameLimit, limits.MaxNameBytes, len(name.Raw))
	}
	pos += nameLen

	pos += skipASCIIWhitespace(tag[pos:])
	if pos >= len(tag) || tag[pos] != '=' {
		return Attr{}, 0, ErrMalformedXML
	}
	pos++
	pos += skipASCIIWhitespace(tag[pos:])
	if pos >= len(tag) || (tag[pos] != '"' && tag[pos] != '\'') {
		return Attr{}, 0, ErrMalformedXML
	}

	quote := tag[pos]
	pos++
	valueStart := pos
	for pos < len(tag) && tag[pos] != quote {
		if tag[pos] == '<' {
			return Attr{}, 0, ErrMalformedXML
		}
		pos++
	}
	if pos >= len(tag) {
		return Attr{}, 0, ErrMalformedXML
	}
	rawValue := tag[valueStart:pos]
	if len(rawValue) > limits.MaxAttributeValueBytes {
		return Attr{}, 0, limitError(
			ErrAttributeValueLimit,
			limits.MaxAttributeValueBytes,
			len(rawValue),
		)
	}
	value, err := decodeAttributeValue(rawValue)
	if err != nil {
		return Attr{}, 0, err
	}
	if len(value) > limits.MaxAttributeValueBytes {
		return Attr{}, 0, limitError(
			ErrAttributeValueLimit,
			limits.MaxAttributeValueBytes,
			len(value),
		)
	}
	if err := validateXMLChars([]byte(value)); err != nil {
		return Attr{}, 0, err
	}
	pos++ // consume closing quote

	return Attr{Name: name, Value: value}, pos, nil
}

func decodeAttributeValue(raw []byte) (string, error) {
	if bytes.IndexByte(raw, '&') < 0 {
		if err := validateXMLChars(raw); err != nil {
			return "", err
		}
		return string(raw), nil
	}

	var out strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] != '&' {
			r, size := utf8.DecodeRune(raw[i:])
			if r == utf8.RuneError && size == 1 {
				return "", ErrInvalidUTF8
			}
			out.WriteRune(r)
			i += size
			continue
		}

		semiRel := bytes.IndexByte(raw[i:], ';')
		if semiRel < 0 {
			return "", ErrMalformedEntity
		}
		semi := i + semiRel
		ref := raw[i+1 : semi]
		value, err := decodeEntityReference(ref)
		if err != nil {
			return "", err
		}
		out.WriteString(value)
		i = semi + 1
	}
	return out.String(), nil
}

func decodeEntityReference(ref []byte) (string, error) {
	if len(ref) > 0 && ref[0] == '#' {
		hex := len(ref) > 1 && (ref[1] == 'x' || ref[1] == 'X')
		numberBytes := ref[1:]
		base := 10
		if hex {
			numberBytes = ref[2:]
			base = 16
		}
		value, err := strconv.ParseUint(string(numberBytes), base, 32)
		if err != nil || value > utf8.MaxRune {
			return "", ErrMalformedEntity
		}
		r := rune(value)
		if !validXMLRune(r) || utf16Surrogate(r) {
			return "", ErrMalformedEntity
		}
		return string(r), nil
	}

	name := string(ref)
	value, ok := builtinEntities[name]
	if !ok {
		return "", ErrUndeclaredEntity
	}
	return value, nil
}

func pushNamespaceScope(p *Parser, elementName Name, attrs []Attr) map[string]string {
	scope := make(map[string]string)
	for _, attr := range attrs {
		switch {
		case attr.Name.Raw == "xmlns":
			scope[""] = attr.Value
		case attr.Name.Prefix == "xmlns":
			scope[attr.Name.Local] = attr.Value
		}
	}
	p.scopes = append(p.scopes, scope)
	return scope
}

func resolveName(name *Name, scopes []map[string]string) {
	if name.Prefix == "" {
		name.Space = lookupNamespace(scopes, "")
		return
	}
	name.Space = lookupNamespace(scopes, name.Prefix)
}

func resolveAttrName(attr *Attr, scopes []map[string]string) {
	if attr.Name.Prefix == "" {
		attr.Name.Space = ""
		return
	}
	attr.Name.Space = lookupNamespace(scopes, attr.Name.Prefix)
}

func lookupNamespace(scopes []map[string]string, prefix string) string {
	if prefix == "xml" {
		return NamespaceXML
	}
	for i := len(scopes) - 1; i >= 0; i-- {
		if uri, ok := scopes[i][prefix]; ok {
			return uri
		}
	}
	return ""
}

func (p *Parser) scanEndElement() (Event, int, error) {
	b := p.buf
	closeIdx := bytes.IndexByte(b, '>')
	if closeIdx < 0 {
		return Event{}, 0, ErrNeedMore
	}

	body := b[2:closeIdx]
	pos := skipASCIIWhitespace(body)
	name, nameLen, err := readName(body[pos:])
	if err != nil {
		return Event{}, 0, err
	}
	pos += nameLen
	pos += skipASCIIWhitespace(body[pos:])
	if pos != len(body) {
		return Event{}, 0, ErrMalformedXML
	}

	if len(p.stack) == 0 {
		return Event{}, 0, ErrMalformedXML
	}
	expected := p.stack[len(p.stack)-1]
	if expected.Raw != name.Raw {
		return Event{}, 0, ErrMalformedXML
	}
	name.Space = expected.Space
	p.stack = p.stack[:len(p.stack)-1]
	if len(p.scopes) > 0 {
		p.scopes = p.scopes[:len(p.scopes)-1]
	}

	return Event{Kind: EventEndElement, Name: name}, closeIdx + 1, nil
}
