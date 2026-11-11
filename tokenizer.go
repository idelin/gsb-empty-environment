package streamsel

import (
	"bytes"
	"strconv"
	"strings"
)

type tokenType int

const (
	tokenText tokenType = iota
	tokenStartTag
	tokenEndTag
)

type token struct {
	typ         tokenType
	name        string // lowercased tag name for start/end tags
	text        string // decoded text for text tokens
	attrs       []attr
	selfClosing bool
}

// attr is a single element attribute. Attribute names are lowercased.
type attr struct {
	name  string
	value string
}

func attrValue(attrs []attr, name string) (string, bool) {
	for _, a := range attrs {
		if a.name == name {
			return a.value, true
		}
	}
	return "", false
}

// tokenizer is an incremental, pull-based HTML tokenizer. Input may arrive
// in arbitrary chunks; tokens split across chunk boundaries are held back
// until complete. Its internal buffer never exceeds the size of the largest
// single token. Tokens are pulled one at a time via next so the caller can
// switch raw-text mode the moment a raw-text element opens.
type tokenizer struct {
	buf []byte
	raw string // raw-text element currently open (script/style/...), "" if none
	eof bool
}

func (z *tokenizer) feed(p []byte) {
	z.buf = append(z.buf, p...)
}

// next parses one token from the front of the buffer. It returns ok=false
// when the buffer does not yet hold a complete token.
func (z *tokenizer) next() (int, token, bool) {
	b := z.buf
	if z.raw != "" {
		return z.nextRaw()
	}
	if b[0] != '<' {
		end := bytes.IndexByte(b, '<')
		if end < 0 {
			end = len(b)
			if !z.eof {
				end = safeTextLen(b)
			}
		}
		if end == 0 {
			return 0, token{}, false
		}
		return end, token{typ: tokenText, text: decodeEntities(string(b[:end]))}, true
	}
	if len(b) < 2 {
		if z.eof {
			return 1, token{typ: tokenText, text: "<"}, true
		}
		return 0, token{}, false
	}
	switch {
	case bytes.HasPrefix(b, []byte("<!--")):
		i := bytes.Index(b, []byte("-->"))
		if i < 0 {
			if z.eof {
				return len(b), token{typ: tokenText}, true // drop unterminated comment
			}
			return 0, token{}, false
		}
		return i + 3, token{typ: tokenText}, true
	case b[1] == '!' || b[1] == '?':
		i := bytes.IndexByte(b, '>')
		if i < 0 {
			if z.eof {
				return len(b), token{typ: tokenText}, true
			}
			return 0, token{}, false
		}
		return i + 1, token{typ: tokenText}, true
	case b[1] == '/':
		return z.nextEndTag()
	case isASCIILetter(b[1]):
		return z.nextStartTag()
	default:
		// Stray '<' is literal text.
		return 1, token{typ: tokenText, text: "<"}, true
	}
}

// nextRaw handles content of raw-text elements (script, style, ...): everything
// up to the matching close tag is text.
func (z *tokenizer) nextRaw() (int, token, bool) {
	b := z.buf
	idx := indexRawClose(b, z.raw)
	if idx < 0 {
		keep := 0
		if !z.eof {
			keep = len(z.raw) + 3 // "</" + name + one boundary byte
		}
		n := len(b) - keep
		if n <= 0 {
			return 0, token{}, false
		}
		return n, token{typ: tokenText, text: decodeEntities(string(b[:n]))}, true
	}
	if idx == 0 {
		z.raw = ""
		return z.next()
	}
	return idx, token{typ: tokenText, text: decodeEntities(string(b[:idx]))}, true
}

// indexRawClose finds "</name" followed by a tag-boundary byte, case
// insensitively. A match needs one byte past the name, so a truncated
// candidate at the end of the buffer is reported as not found.
func indexRawClose(b []byte, name string) int {
	end := len(b) - (2 + len(name) + 1)
	for i := 0; i <= end; i++ {
		if b[i] != '<' || b[i+1] != '/' {
			continue
		}
		if !equalFoldASCII(b[i+2:i+2+len(name)], name) {
			continue
		}
		if isTagBoundary(b[i+2+len(name)]) {
			return i
		}
	}
	return -1
}

func (z *tokenizer) nextEndTag() (int, token, bool) {
	b := z.buf
	i := 2
	start := i
	for i < len(b) && isNameChar(b[i]) {
		i++
	}
	name := strings.ToLower(string(b[start:i]))
	j := i
	for j < len(b) && b[j] != '>' {
		j++
	}
	if j >= len(b) {
		if z.eof {
			if name == "" {
				return len(b), token{typ: tokenText}, true
			}
			return len(b), token{typ: tokenEndTag, name: name}, true
		}
		return 0, token{}, false
	}
	if name == "" { // "</>" or "</ ...": bogus markup, skip
		return j + 1, token{typ: tokenText}, true
	}
	return j + 1, token{typ: tokenEndTag, name: name}, true
}

func (z *tokenizer) nextStartTag() (int, token, bool) {
	b := z.buf
	i := 1
	start := i
	for i < len(b) && isNameChar(b[i]) {
		i++
	}
	if i >= len(b) && !z.eof {
		return 0, token{}, false
	}
	tok := token{typ: tokenStartTag, name: strings.ToLower(string(b[start:i]))}
	for {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) {
			if z.eof {
				return len(b), token{typ: tokenText}, true // drop unterminated tag
			}
			return 0, token{}, false
		}
		if b[i] == '>' {
			return i + 1, tok, true
		}
		if b[i] == '/' {
			if i+1 < len(b) && b[i+1] == '>' {
				tok.selfClosing = true
				return i + 2, tok, true
			}
			if i+1 >= len(b) && !z.eof {
				return 0, token{}, false
			}
			i++ // stray '/', ignore
			continue
		}
		// Attribute name.
		as := i
		for i < len(b) && !isSpace(b[i]) && b[i] != '=' && b[i] != '>' && b[i] != '/' {
			i++
		}
		if i >= len(b) && !z.eof {
			return 0, token{}, false
		}
		aname := strings.ToLower(string(b[as:i]))
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		aval := ""
		if i < len(b) && b[i] == '=' {
			i++
			for i < len(b) && isSpace(b[i]) {
				i++
			}
			if i >= len(b) {
				if !z.eof {
					return 0, token{}, false
				}
			} else if b[i] == '"' || b[i] == '\'' {
				q := b[i]
				i++
				vs := i
				for i < len(b) && b[i] != q {
					i++
				}
				if i >= len(b) {
					if !z.eof {
						return 0, token{}, false
					}
					aval = string(b[vs:])
				} else {
					aval = string(b[vs:i])
					i++
				}
			} else {
				vs := i
				for i < len(b) && !isSpace(b[i]) && b[i] != '>' {
					i++
				}
				if i >= len(b) && !z.eof {
					return 0, token{}, false
				}
				aval = string(b[vs:i])
			}
		}
		if aname != "" {
			dup := false
			for _, a := range tok.attrs {
				if a.name == aname {
					dup = true
					break
				}
			}
			if !dup {
				tok.attrs = append(tok.attrs, attr{name: aname, value: decodeEntities(aval)})
			}
		}
		if i >= len(b) && !z.eof {
			return 0, token{}, false
		}
	}
}

// safeTextLen returns how many leading bytes of a text run (with no '<' in
// it) may be emitted now, withholding a trailing incomplete entity reference.
func safeTextLen(b []byte) int {
	i := bytes.LastIndexByte(b, '&')
	if i < 0 {
		return len(b)
	}
	if bytes.IndexByte(b[i:], ';') < 0 {
		return i
	}
	return len(b)
}

var entities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'", "nbsp": "\u00a0",
}

func decodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			sb.WriteByte(s[i])
			i++
			continue
		}
		j := strings.IndexByte(s[i:], ';')
		if j < 0 {
			sb.WriteByte(s[i])
			i++
			continue
		}
		ent := s[i+1 : i+j]
		if strings.HasPrefix(ent, "#") {
			num := ent[1:]
			base := 10
			if len(num) > 0 && (num[0] == 'x' || num[0] == 'X') {
				num = num[1:]
				base = 16
			}
			if n, err := strconv.ParseInt(num, base, 32); err == nil && n > 0 {
				sb.WriteRune(rune(n))
				i += j + 1
				continue
			}
		} else if v, ok := entities[ent]; ok {
			sb.WriteString(v)
			i += j + 1
			continue
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

func isASCIILetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isNameChar(c byte) bool {
	return isASCIILetter(c) || ('0' <= c && c <= '9') || c == '-' || c == '_' || c == ':'
}

func isTagBoundary(c byte) bool {
	return isSpace(c) || c == '/' || c == '>'
}

func equalFoldASCII(b []byte, s string) bool {
	if len(b) != len(s) {
		return false
	}
	for i := 0; i < len(b); i++ {
		c, d := b[i], s[i]
		if 'A' <= c && c <= 'Z' {
			c += 32
		}
		if 'A' <= d && d <= 'Z' {
			d += 32
		}
		if c != d {
			return false
		}
	}
	return true
}
