package netsec

import "bytes"

// scanProlog walks the XML declaration, comments, processing instructions
// and whitespace up to either the document element (no DOCTYPE: accept) or
// a DOCTYPE declaration (validate it).
func (s *scanner) scanProlog() error {
	// Optional UTF-8 BOM.
	if bytes.HasPrefix(s.data, []byte{0xEF, 0xBB, 0xBF}) {
		s.pos = 3
	}

	for s.pos < len(s.data) {
		s.skipSpace()
		if s.pos >= len(s.data) {
			return s.fail(ErrNoProlog)
		}

		// Document element begins: the prolog is complete.
		if s.data[s.pos] == '<' && s.pos+1 < len(s.data) && isNameStartByte(s.data[s.pos+1]) {
			return nil
		}

		switch {
		case s.consume("<!--"):
			if err := s.skipComment(); err != nil {
				return err
			}
		case s.consume("<?"):
			if err := s.skipPI(); err != nil {
				return err
			}
		case s.consume("<!DOCTYPE"):
			if !s.boundaryAt(s.pos) {
				// "<!DOCTYPEfoo ..." is not a real declaration.
				return s.fail(ErrNoProlog)
			}
			return s.scanDoctype()
		default:
			return s.fail(ErrNoProlog)
		}
	}
	return s.fail(ErrNoProlog)
}

// scanDoctype parses "<!DOCTYPE" through the terminating ">", including an
// optional internal subset. The document body is never inspected.
func (s *scanner) scanDoctype() error {
	s.skipSpace()
	if !s.consumeName() {
		return s.fail(ErrMalformedDTD)
	}

	s.skipSpace()

	// External ID (external subset or external entity) is forbidden in any
	// spelling, including whitespace padded SYSTEM / PUBLIC.
	if s.hasKeyword("SYSTEM") || s.hasKeyword("PUBLIC") {
		return s.fail(ErrExternalEntity)
	}

	if s.pos >= len(s.data) {
		return s.fail(ErrMalformedDTD)
	}

	switch s.data[s.pos] {
	case '>':
		s.pos++
		return s.finalizeEntities()
	case '[':
		s.pos++
		return s.scanInternalSubset()
	default:
		return s.fail(ErrMalformedDTD)
	}
}

func (s *scanner) scanInternalSubset() error {
	for {
		s.skipSpace()
		if s.pos >= len(s.data) {
			return s.fail(ErrMalformedDTD)
		}

		switch {
		case s.data[s.pos] == ']':
			s.pos++
			s.skipSpace()
			if s.pos >= len(s.data) || s.data[s.pos] != '>' {
				return s.fail(ErrMalformedDTD)
			}
			s.pos++
			return s.finalizeEntities()
		case s.consume("<!--"):
			if err := s.skipComment(); err != nil {
				return err
			}
		case s.consume("<?"):
			if err := s.skipPI(); err != nil {
				return err
			}
		case s.consume("<!ENTITY"):
			if !s.boundaryAt(s.pos) {
				return s.fail(ErrMalformedDTD)
			}
			if err := s.scanEntityDecl(); err != nil {
				return err
			}
		case s.data[s.pos] == '<':
			// ELEMENT, ATTLIST, NOTATION and any other markup
			// declarations: only the narrow, safe subset of DTD syntax is
			// allowed through.
			if err := s.skipOtherMarkup(); err != nil {
				return err
			}
		default:
			return s.fail(ErrMalformedDTD)
		}
	}
}

// scanEntityDecl handles "<!ENTITY" through the terminating ">".
func (s *scanner) scanEntityDecl() error {
	s.skipSpace()

	// Parameter entity declarations are forbidden in every shape.
	if s.pos < len(s.data) && s.data[s.pos] == '%' {
		return s.fail(ErrParameterEntity)
	}

	name := s.readName()
	if name == "" {
		return s.fail(ErrMalformedDTD)
	}
	if _, exists := s.entities[name]; exists {
		// Duplicate declarations are rejected rather than silently
		// overridden; the XML spec forbids them anyway.
		return s.fail(ErrMalformedDTD)
	}

	s.skipSpace()
	if s.pos >= len(s.data) {
		return s.fail(ErrMalformedDTD)
	}

	// External entities: a SYSTEM/PUBLIC keyword anywhere before the value
	// literal. Comments inside the declaration cannot smuggle the keyword
	// because comments are markup and rejected inside entity values and
	// unrecognized markup.
	if s.hasKeyword("SYSTEM") || s.hasKeyword("PUBLIC") {
		return s.fail(ErrExternalEntity)
	}

	quote := s.data[s.pos]
	if quote != '"' && quote != '\'' {
		return s.fail(ErrMalformedDTD)
	}
	s.pos++

	raw, err := s.readEntityValue(quote)
	if err != nil {
		return err
	}

	s.skipSpace()
	if s.pos >= len(s.data) || s.data[s.pos] != '>' {
		return s.fail(ErrMalformedDTD)
	}
	s.pos++

	// Defer expansion: the complete subset may contain forward references,
	// and cycles are only detectable once every declaration is known.
	s.entities[name] = entityInfo{raw: string(raw)}
	return nil
}

// finalizeEntities expands every declared internal entity now that the
// whole subset is available, enforcing the total expansion budget and
// rejecting reference cycles.
func (s *scanner) finalizeEntities() error {
	for name := range s.entities {
		info := s.entities[name]
		expanded, err := s.expandEntity(name, []byte(info.raw))
		if err != nil {
			return err
		}
		info.expanded = expanded
		s.entities[name] = info
	}
	s.guard.lastEntities = s.entities
	return nil
}

// readEntityValue consumes the quoted literal of an internal entity. The
// returned bytes keep references intact so they can be expanded with
// recursion and budget accounting; only structural validation happens here.
func (s *scanner) readEntityValue(quote byte) ([]byte, error) {
	start := s.pos
	for s.pos < len(s.data) {
		c := s.data[s.pos]
		switch {
		case c == quote:
			raw := s.data[start:s.pos]
			s.pos++
			return raw, nil
		case c == '%':
			// Parameter entity references are never allowed inside an
			// internal entity value.
			return nil, s.fail(ErrParameterEntity)
		case c == '<':
			// No markup, including comments, may hide inside entity
			// values.
			return nil, s.fail(ErrMalformedDTD)
		case c == '&':
			if err := s.validateReferenceInValue(); err != nil {
				return nil, err
			}
		default:
			s.pos++
		}
	}
	return nil, s.fail(ErrMalformedDTD)
}

// validateReferenceInValue is positioned on '&' and verifies that the
// reference is a well-formed general entity or character reference.
func (s *scanner) validateReferenceInValue() error {
	mark := s.pos
	s.pos++ // past '&'
	if s.pos >= len(s.data) {
		return s.fail(ErrMalformedDTD)
	}
	if s.data[s.pos] == '#' {
		s.pos++
		if s.pos < len(s.data) && (s.data[s.pos] == 'x' || s.data[s.pos] == 'X') {
			s.pos++
		}
		digits := 0
		for s.pos < len(s.data) && isHexByte(s.data[s.pos]) {
			s.pos++
			digits++
		}
		if digits == 0 || s.pos >= len(s.data) || s.data[s.pos] != ';' {
			return s.fail(ErrMalformedDTD)
		}
		s.pos++
		return nil
	}
	if !s.consumeName() || s.pos >= len(s.data) || s.data[s.pos] != ';' {
		s.pos = mark
		return s.fail(ErrMalformedDTD)
	}
	s.pos++ // past ';'
	return nil
}

// skipOtherMarkup skips a markup declaration that is not an ENTITY
// declaration, honoring quoted literals. It rejects external identifiers,
// parameter entity references and any nested comments or PIs, which keeps
// the bypass surface minimal.
func (s *scanner) skipOtherMarkup() error {
	// Position is on '<'.
	s.pos++
	if s.pos < len(s.data) && s.data[s.pos] == '!' {
		s.pos++
	}
	// Declaration name, e.g. ELEMENT / ATTLIST / NOTATION.
	nameStart := s.pos
	if !s.consumeName() {
		return s.fail(ErrMalformedDTD)
	}
	if string(s.data[nameStart:s.pos]) == "NOTATION" {
		return s.fail(ErrUnsupportedMarkup)
	}

	for s.pos < len(s.data) {
		c := s.data[s.pos]
		switch {
		case c == '>':
			s.pos++
			return nil
		case c == '"' || c == '\'':
			if err := s.skipQuoted(c); err != nil {
				return err
			}
		case c == '%':
			return s.fail(ErrParameterEntity)
		case s.hasKeyword("SYSTEM") || s.hasKeyword("PUBLIC"):
			return s.fail(ErrExternalEntity)
		case c == '<':
			// Nested comment / PI inside a declaration is not allowed.
			return s.fail(ErrMalformedDTD)
		default:
			s.pos++
		}
	}
	return s.fail(ErrMalformedDTD)
}

func (s *scanner) skipQuoted(quote byte) error {
	s.pos++ // past opening quote
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case quote:
			s.pos++
			return nil
		case '<':
			// Markup has no business inside a quoted literal of a skipped
			// declaration.
			return s.fail(ErrMalformedDTD)
		case '%':
			// A bare "%" is allowed in attribute defaults, but a "%name;"
			// parameter entity reference is not.
			if s.looksLikeParameterReference() {
				return s.fail(ErrParameterEntity)
			}
			s.pos++
		default:
			s.pos++
		}
	}
	return s.fail(ErrMalformedDTD)
}

// looksLikeParameterReference reports whether the current "%" begins a
// "%name;" reference. The scanner remains positioned on "%".
func (s *scanner) looksLikeParameterReference() bool {
	j := s.pos + 1
	if j >= len(s.data) || !isNameStartByte(s.data[j]) {
		return false
	}
	j++
	for j < len(s.data) && isNameByte(s.data[j]) {
		j++
	}
	return j < len(s.data) && s.data[j] == ';'
}

func (s *scanner) skipComment() error {
	// Position is just past "<!--".
	idx := bytes.Index(s.data[s.pos:], []byte("-->"))
	if idx < 0 {
		s.pos = len(s.data)
		return s.fail(ErrMalformedDTD)
	}
	// XML forbids "--" within a comment.
	if bytes.Contains(s.data[s.pos:s.pos+idx], []byte("--")) {
		return s.fail(ErrMalformedDTD)
	}
	s.pos += idx + 3
	return nil
}

func (s *scanner) skipPI() error {
	// Position is just past "<?".
	idx := bytes.Index(s.data[s.pos:], []byte("?>"))
	if idx < 0 {
		s.pos = len(s.data)
		return s.fail(ErrMalformedDTD)
	}
	s.pos += idx + 2
	return nil
}

func (s *scanner) skipSpace() {
	for s.pos < len(s.data) && isSpaceByte(s.data[s.pos]) {
		s.pos++
	}
}

// consume reports whether the data at the current position equals token and
// advances past it on success.
func (s *scanner) consume(token string) bool {
	if s.pos+len(token) > len(s.data) || string(s.data[s.pos:s.pos+len(token)]) != token {
		return false
	}
	s.pos += len(token)
	return true
}

// hasKeyword reports whether an ASCII keyword (matched case-sensitively,
// as XML keywords are) starts at the current position on a token boundary.
// It does not advance the scanner.
func (s *scanner) hasKeyword(keyword string) bool {
	if s.pos+len(keyword) > len(s.data) || string(s.data[s.pos:s.pos+len(keyword)]) != keyword {
		return false
	}
	return s.boundaryAt(s.pos + len(keyword))
}

// boundaryAt reports whether offset is at the end of the data or a byte that
// cannot continue an XML name.
func (s *scanner) boundaryAt(offset int) bool {
	return offset >= len(s.data) || !isNameByte(s.data[offset])
}

// readName returns the XML name at the current position and advances past
// it. Names here are restricted to the ASCII name-start/name byte classes,
// which covers every DTD keyword and entity name produced by SAML tooling.
func (s *scanner) readName() string {
	start := s.pos
	if !s.consumeName() {
		return ""
	}
	return string(s.data[start:s.pos])
}

func (s *scanner) consumeName() bool {
	if s.pos >= len(s.data) || !isNameStartByte(s.data[s.pos]) {
		return false
	}
	s.pos++
	for s.pos < len(s.data) && isNameByte(s.data[s.pos]) {
		s.pos++
	}
	return true
}

func isSpaceByte(c byte) bool {
	switch c {
	case 0x20, 0x09, 0x0D, 0x0A:
		return true
	default:
		return false
	}
}

func isNameStartByte(c byte) bool {
	return c == ':' || c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= 0xC0 && c <= 0xD6) || (c >= 0xD8 && c <= 0xF6) || c >= 0xF8
}

func isNameByte(c byte) bool {
	return isNameStartByte(c) || c == '-' || c == '.' || (c >= '0' && c <= '9') || c == 0xB7
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
