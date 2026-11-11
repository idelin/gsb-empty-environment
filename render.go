package netsec

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Format renders format into w using the positionally bound args.
//
// Sensitive args are replaced wholesale by the redaction token before any
// width, precision or budget processing, so a secret can never be split into a
// matchable prefix. Non-sensitive text is truncated on UTF-8 code-point
// boundaries. A malformed template produces a safe error marker and a
// FormatError; raw arguments are never concatenated as a fallback.
func Format(w io.Writer, format string, args []Arg, opts ...Option) (int, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	bw := newBudgetWriter(w, cfg.budget, cfg.truncationMark)

	segs, err := parse(format)
	if err != nil {
		bw.writeTerminal(cfg.errorMark)
		return bw.written, err
	}

	implicit := 0
	for _, seg := range segs {
		switch seg.kind {
		case segLiteral, segPercent:
			bw.writeContent(seg.text)
		case segVerb:
			out, err := renderVerb(seg.verb, args, &implicit, cfg.redactionToken)
			if err != nil {
				bw.writeTerminal(cfg.errorMark)
				return bw.written, err
			}
			bw.writeContent(out)
		}
	}
	bw.finish()
	return bw.written, bw.err
}

// FormatBytes renders to a newly allocated byte slice, honoring the same
// redaction and budget rules as Format.
func FormatBytes(format string, args []Arg, opts ...Option) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := Format(&buf, format, args, opts...); err != nil {
		return buf.Bytes(), err
	}
	return buf.Bytes(), nil
}

// FormatString renders to a string. On template error the returned string
// contains only the safe error marker (plus any already-budgeted literals);
// the error is still returned so callers can react.
func FormatString(format string, args []Arg, opts ...Option) (string, error) {
	var sb strings.Builder
	if _, err := Format(&sb, format, args, opts...); err != nil {
		return sb.String(), err
	}
	return sb.String(), nil
}

// renderVerb resolves every argument consumed by v (including starred
// width/precision) and produces its safe textual form.
func renderVerb(v *verb, args []Arg, implicit *int, token string) (string, error) {
	width, hasWidth, err := resolveField(v.widthStar, v.widthIdx, v.hasWidth, v.width, args, implicit)
	if err != nil {
		return "", err
	}
	prec, hasPrec, err := resolveField(v.precStar, v.precIdx, v.hasPrec, v.prec, args, implicit)
	if err != nil {
		return "", err
	}
	if hasWidth && width < 0 {
		if !strings.ContainsRune(v.flags, '-') {
			v.flags += "-"
		}
		width = -width
	}
	if hasPrec && prec < 0 {
		hasPrec = false
	}

	idx, err := resolveArgIndex(v, args, implicit)
	if err != nil {
		return "", err
	}
	arg := args[idx]

	if arg.Sensitive {
		// The secret is replaced before any sizing work. Width/padding apply to
		// the token; precision truncates the token. The original value never
		// reaches the formatter.
		return renderToken(v, token, width, hasWidth, prec, hasPrec), nil
	}

	spec := rebuildSpec(v, width, hasWidth, prec, hasPrec)
	return fmt.Sprintf(spec, arg.Value), nil
}

func resolveArgIndex(v *verb, args []Arg, implicit *int) (int, error) {
	var idx int
	if v.explicitIndex > 0 {
		idx = v.explicitIndex - 1
	} else {
		// Implicit conversions advance the shared argument cursor; an explicit
		// index leaves it untouched (Go fmt semantics).
		idx = *implicit
		*implicit++
	}
	if idx < 0 || idx >= len(args) {
		return 0, &FormatError{reason: "verb references missing argument", pos: v.pos}
	}
	return idx, nil
}

// resolveField returns the effective numeric width/precision. A starred field
// consumes an additional (non-sensitive) integer argument.
func resolveField(starred bool, starIndex int, present bool, static int, args []Arg, implicit *int) (value int, has bool, err error) {
	if !present {
		return 0, false, nil
	}
	if !starred {
		return static, true, nil
	}
	if starIndex > 0 {
		return starArgValue(starIndex-1, args)
	}
	idx := *implicit
	*implicit++
	return starArgValue(idx, args)
}

func starArgValue(idx int, args []Arg) (int, bool, error) {
	if idx < 0 || idx >= len(args) {
		return 0, false, &FormatError{reason: "star width/precision references missing argument", pos: idx}
	}
	if args[idx].Sensitive {
		return 0, false, &FormatError{reason: "star width/precision cannot reference a sensitive argument", pos: idx}
	}
	n, ok := asInt(args[idx].Value)
	if !ok {
		return 0, false, &FormatError{reason: "star width/precision requires an integer argument", pos: idx}
	}
	return n, true, nil
}

func asInt(value any) (int, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int(rv.Uint()), true
	default:
		return 0, false
	}
}

// rebuildSpec reconstructs a fmt verb with the parsed flags and resolved
// numeric width/precision. It is only ever used for non-sensitive values.
func rebuildSpec(v *verb, width int, hasWidth bool, prec int, hasPrec bool) string {
	var b strings.Builder
	b.WriteByte('%')
	b.WriteString(v.flags)
	if hasWidth && width >= 0 {
		fmt.Fprintf(&b, "%d", width)
	}
	if hasPrec {
		fmt.Fprintf(&b, ".%d", prec)
	}
	b.WriteByte(v.verb)
	return b.String()
}

// renderToken formats the redaction token. String-oriented verbs keep their
// meaning; numeric/boolean verbs cannot describe a redacted value, so the
// token is rendered as a padded, optionally precision-truncated string.
func renderToken(v *verb, token string, width int, hasWidth bool, prec int, hasPrec bool) string {
	switch v.verb {
	case 'q':
		token = fmt.Sprintf("%q", token)
	case 'x', 'X':
		token = fmt.Sprintf("%"+string(v.verb), token)
	}

	if hasPrec && prec < len(token) {
		token = runePrefix(token, prec)
	}

	if !hasWidth || width <= len(token) {
		return token
	}

	pad := width - len(token)
	if strings.ContainsRune(v.flags, '-') {
		return token + strings.Repeat(" ", pad)
	}
	if strings.ContainsRune(v.flags, '0') {
		return strings.Repeat("0", pad) + token
	}
	return strings.Repeat(" ", pad) + token
}

// runePrefix returns the longest prefix of s no longer than maxBytes without
// splitting a UTF-8 code point.
func runePrefix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	used := 0
	for _, r := range s {
		size := len(string(r))
		if used+size > maxBytes {
			break
		}
		used += size
	}
	return s[:used]
}

// budgetWriter enforces a byte budget while keeping UTF-8 code points intact
// and reserving room for the truncation marker.
type budgetWriter struct {
	w            io.Writer
	remaining    int
	contentLimit int
	marker       string
	truncated    bool
	written      int
	err          error
}

func newBudgetWriter(w io.Writer, budget int, marker string) *budgetWriter {
	bw := &budgetWriter{w: w, remaining: budget, marker: marker}
	if budget >= 0 {
		bw.contentLimit = budget - len(marker)
		if bw.contentLimit < 0 {
			bw.contentLimit = 0
		}
	}
	return bw
}

func (b *budgetWriter) writeContent(s string) {
	if b.err != nil || b.remaining == 0 {
		if b.remaining >= 0 {
			b.truncated = true
		}
		return
	}
	if b.remaining < 0 {
		// Unlimited budget.
		n, err := io.WriteString(b.w, s)
		b.written += n
		b.err = err
		return
	}

	allowed := b.contentLimit
	if len(s) <= allowed {
		b.flush(s)
		b.contentLimit -= len(s)
		b.remaining -= len(s)
		return
	}

	prefix := runePrefix(s, allowed)
	if prefix != "" {
		b.flush(prefix)
	}
	b.contentLimit = 0
	b.remaining -= len(prefix)
	b.truncated = true
}

// writeTerminal emits a marker that must never itself carry a truncation
// marker (parse/render failures). It is still bounded by the remaining budget
// and kept code-point safe.
func (b *budgetWriter) writeTerminal(s string) {
	if b.err != nil || b.remaining == 0 {
		return
	}
	if b.remaining < 0 {
		n, err := io.WriteString(b.w, s)
		b.written += n
		b.err = err
		return
	}
	prefix := runePrefix(s, b.remaining)
	b.flush(prefix)
	b.remaining = 0
}

func (b *budgetWriter) finish() {
	if !b.truncated || b.err != nil || b.remaining <= 0 {
		return
	}
	prefix := runePrefix(b.marker, b.remaining)
	b.flush(prefix)
	b.remaining -= len(prefix)
}

func (b *budgetWriter) flush(s string) {
	n, err := io.WriteString(b.w, s)
	b.written += n
	if err != nil {
		b.err = err
	}
}
