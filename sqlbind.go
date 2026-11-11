// Package sqlbind rewrites named parameter placeholders (":name") in SQL
// text into PostgreSQL positional placeholders ("$1", "$2", ...), collecting
// the corresponding values into an argument slice.
//
// The rewriter is a small lexer, not a string replacer: placeholders that
// appear inside string literals, quoted identifiers, dollar-quoted strings,
// line comments, or (possibly nested) block comments are left untouched.
// Slice values expand into parenthesized placeholder lists of matching
// length, and repeated uses of the same name reuse the same positions.
// Values are never interpolated into the SQL text; they only ever appear
// in the returned argument slice.
package sqlbind

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// UnclosedError reports a statement whose final construct (string literal,
// quoted identifier, block comment, or dollar-quoted string) is never
// terminated. Such statements must not be sent to the database.
type UnclosedError struct {
	Construct string
	Offset    int
}

func (e *UnclosedError) Error() string {
	return fmt.Sprintf("sqlbind: unterminated %s at offset %d", e.Construct, e.Offset)
}

// MissingArgError reports a named placeholder for which no argument was
// provided.
type MissingArgError struct {
	Name string
}

func (e *MissingArgError) Error() string {
	return fmt.Sprintf("sqlbind: no argument provided for :%s", e.Name)
}

// Bind rewrites the named placeholders in query into PostgreSQL positional
// placeholders and returns the rewritten SQL together with the positional
// argument values. Numbering continues after any positional placeholders
// already present in the query.
//
// Bind returns an *UnclosedError when the statement ends inside a string
// literal, quoted identifier, block comment, or dollar-quoted string, and a
// *MissingArgError when a placeholder has no matching entry in args.
func Bind(query string, args map[string]any) (string, []any, error) {
	b := &binder{
		src:       query,
		args:      args,
		positions: make(map[string]expansion),
		next:      1,
	}
	b.out.Grow(len(query) + 16)
	if err := b.run(); err != nil {
		return "", nil, err
	}
	return b.out.String(), b.values, nil
}

type binder struct {
	src       string
	args      map[string]any
	out       strings.Builder
	values    []any
	positions map[string]expansion
	next      int
}

// expansion records the positional placeholders assigned to a named
// parameter. list is true when the value was a slice and the expansion
// must stay parenthesized even for a single element.
type expansion struct {
	pos  []int
	list bool
}

func (b *binder) run() error {
	i := 0
	for i < len(b.src) {
		c := b.src[i]
		switch {
		case c == '-' && i+1 < len(b.src) && b.src[i+1] == '-':
			i = b.copyLineComment(i)
		case c == '/' && i+1 < len(b.src) && b.src[i+1] == '*':
			end, err := skipBlockComment(b.src, i)
			if err != nil {
				return err
			}
			b.out.WriteString(b.src[i:end])
			i = end
		case c == '\'':
			end, err := skipQuoted(b.src, i, '\'', isEscapedString(b.src, i))
			if err != nil {
				return err
			}
			b.out.WriteString(b.src[i:end])
			i = end
		case c == '"':
			end, err := skipQuoted(b.src, i, '"', false)
			if err != nil {
				return err
			}
			b.out.WriteString(b.src[i:end])
			i = end
		case c == '$':
			end, handled, err := b.copyDollar(i)
			if err != nil {
				return err
			}
			if handled {
				i = end
			} else {
				b.out.WriteByte(c)
				i++
			}
		case c == ':':
			end, handled, err := b.rewriteNamed(i)
			if err != nil {
				return err
			}
			if handled {
				i = end
			} else {
				b.out.WriteByte(c)
				i++
			}
		default:
			b.out.WriteByte(c)
			i++
		}
	}
	return nil
}

func (b *binder) copyLineComment(start int) int {
	if j := strings.IndexByte(b.src[start:], '\n'); j >= 0 {
		b.out.WriteString(b.src[start : start+j+1])
		return start + j + 1
	}
	b.out.WriteString(b.src[start:])
	return len(b.src)
}

// skipBlockComment returns the offset just past the closing "*/" of the
// block comment starting at src[start:start+2]. Nested comments are counted.
func skipBlockComment(src string, start int) (int, error) {
	depth := 0
	for i := start; i < len(src); {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			depth++
			i += 2
			continue
		}
		if i+1 < len(src) && src[i] == '*' && src[i+1] == '/' {
			depth--
			i += 2
			if depth == 0 {
				return i, nil
			}
			continue
		}
		i++
	}
	return 0, &UnclosedError{Construct: "block comment", Offset: start}
}

// skipQuoted returns the offset just past the closing quote of the string
// literal or quoted identifier starting at src[start]. Doubled quotes are
// treated as escapes; when backslash is true (PostgreSQL E'...' strings),
// backslash escapes are honored as well.
func skipQuoted(src string, start int, quote byte, backslash bool) (int, error) {
	construct := "string literal"
	if quote == '"' {
		construct = "quoted identifier"
	}
	for i := start + 1; i < len(src); {
		switch src[i] {
		case '\\':
			if backslash && i+1 < len(src) {
				i += 2
				continue
			}
			i++
		case quote:
			if i+1 < len(src) && src[i+1] == quote {
				i += 2
				continue
			}
			return i + 1, nil
		default:
			i++
		}
	}
	return 0, &UnclosedError{Construct: construct, Offset: start}
}

// isEscapedString reports whether the quote at src[start] is the opening
// quote of a PostgreSQL E'...' escape string.
func isEscapedString(src string, start int) bool {
	if start == 0 {
		return false
	}
	prev := src[start-1]
	if prev != 'e' && prev != 'E' {
		return false
	}
	return start < 2 || !isIdentChar(src[start-2])
}

// copyDollar copies an existing "$N" positional placeholder (tracking the
// highest index seen) or a dollar-quoted string. It reports handled=false
// when the "$" is neither, leaving it to be copied as a literal byte.
func (b *binder) copyDollar(start int) (end int, handled bool, err error) {
	j := start + 1
	if j < len(b.src) && isDigit(b.src[j]) {
		k := j
		for k < len(b.src) && isDigit(b.src[k]) {
			k++
		}
		n, convErr := strconv.Atoi(b.src[j:k])
		if convErr != nil {
			return 0, false, convErr
		}
		if n >= b.next {
			b.next = n + 1
		}
		b.out.WriteString(b.src[start:k])
		return k, true, nil
	}
	k := j
	for k < len(b.src) && isTagChar(b.src[k]) {
		k++
	}
	if k < len(b.src) && b.src[k] == '$' {
		tag := b.src[start : k+1]
		close := strings.Index(b.src[k+1:], tag)
		if close < 0 {
			return 0, false, &UnclosedError{Construct: "dollar-quoted string", Offset: start}
		}
		end = k + 1 + close + len(tag)
		b.out.WriteString(b.src[start:end])
		return end, true, nil
	}
	return 0, false, nil
}

// rewriteNamed rewrites the ":name" placeholder starting at src[start].
// It reports handled=false when the ":" does not introduce a placeholder
// (a "::" cast, or a ":" not followed by an identifier start).
func (b *binder) rewriteNamed(start int) (end int, handled bool, err error) {
	j := start + 1
	if j < len(b.src) && b.src[j] == ':' {
		b.out.WriteString("::")
		return start + 2, true, nil
	}
	if j >= len(b.src) || !isIdentStart(b.src[j]) {
		return 0, false, nil
	}
	k := j + 1
	for k < len(b.src) && isIdentChar(b.src[k]) {
		k++
	}
	if err := b.emit(b.src[j:k]); err != nil {
		return 0, false, err
	}
	return k, true, nil
}

// emit writes the positional placeholder(s) for a named parameter, reusing
// previously assigned positions when the name repeats.
func (b *binder) emit(name string) error {
	if exp, ok := b.positions[name]; ok {
		b.writeExpansion(exp)
		return nil
	}
	v, ok := b.args[name]
	if !ok {
		return &MissingArgError{Name: name}
	}
	if rv := reflect.ValueOf(v); isExpandable(rv) {
		pos := make([]int, rv.Len())
		for i := range pos {
			pos[i] = b.next
			b.values = append(b.values, rv.Index(i).Interface())
			b.next++
		}
		exp := expansion{pos: pos, list: true}
		b.positions[name] = exp
		b.writeExpansion(exp)
		return nil
	}
	b.positions[name] = expansion{pos: []int{b.next}}
	b.values = append(b.values, v)
	b.out.WriteByte('$')
	b.out.WriteString(strconv.Itoa(b.next))
	b.next++
	return nil
}

// writeExpansion writes a previously assigned expansion: "$n" for a scalar,
// "($n,...)" for a slice, and "(NULL)" for an empty slice.
func (b *binder) writeExpansion(exp expansion) {
	switch {
	case len(exp.pos) == 0:
		b.out.WriteString("(NULL)")
	case len(exp.pos) == 1 && !exp.list:
		b.out.WriteByte('$')
		b.out.WriteString(strconv.Itoa(exp.pos[0]))
	default:
		b.out.WriteByte('(')
		for i, p := range exp.pos {
			if i > 0 {
				b.out.WriteByte(',')
			}
			b.out.WriteByte('$')
			b.out.WriteString(strconv.Itoa(p))
		}
		b.out.WriteByte(')')
	}
}

// isExpandable reports whether v is a slice or array that should expand
// into a placeholder list. Byte slices are scalar blobs, not lists.
func isExpandable(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return rv.Type().Elem().Kind() != reflect.Uint8
	default:
		return false
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }

// isTagChar reports whether c may appear in a dollar-quote tag. Tags follow
// identifier rules except that they may be empty and may not start with a
// digit; the leading-digit case is handled before tag scanning.
func isTagChar(c byte) bool { return isIdentChar(c) }
