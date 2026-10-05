// Package configcomplete provides context-aware completion for a
// configuration language with nested objects, arrays and escaped strings
// (JSON grammar).
//
// Given incomplete text and a cursor position (a byte offset), Complete
// reports which token categories are legal at the cursor and the shortest
// suffix that turns the text into a valid document. The completion only
// ever appends at the cursor; valid tokens before the cursor are never
// modified. Cursor positions inside strings count original source bytes,
// so a cursor may sit between the bytes of an escape sequence. If the text
// before the cursor is already invalid, Complete reports the earliest
// byte offset at which the text stops being extendable into a valid
// document.
package configcomplete

import "strings"

// TokenCategory classifies the token that text inserted at the cursor
// would start (or continue).
type TokenCategory string

const (
	CatObjectStart TokenCategory = "object-start" // {
	CatObjectEnd   TokenCategory = "object-end"   // }
	CatArrayStart  TokenCategory = "array-start"  // [
	CatArrayEnd    TokenCategory = "array-end"    // ]
	CatColon       TokenCategory = "colon"        // :
	CatComma       TokenCategory = "comma"        // ,
	CatString      TokenCategory = "string"       // "..." or string content
	CatNumber      TokenCategory = "number"       // JSON number
	CatBoolean     TokenCategory = "boolean"      // true / false
	CatNull        TokenCategory = "null"         // null
)

// categoryOrder fixes a deterministic order for Result.Categories.
var categoryOrder = []TokenCategory{
	CatObjectStart, CatObjectEnd, CatArrayStart, CatArrayEnd,
	CatColon, CatComma, CatString, CatNumber, CatBoolean, CatNull,
}

// Result is the outcome of Complete.
type Result struct {
	// OK reports whether the text before the cursor can be extended into
	// a valid document.
	OK bool
	// Categories lists the legal token categories at the cursor. It is
	// empty when the document is already complete. When the cursor falls
	// inside or right after an extendable token (string, number, keyword
	// prefix), the category of that token is included.
	Categories []TokenCategory
	// Suffix is the shortest string that, appended at the cursor, makes
	// the whole text a valid document. It is "" when the document is
	// already complete.
	Suffix string
	// ErrOffset is the byte offset of the earliest conflict, valid when
	// OK is false.
	ErrOffset int
	// Err describes the conflict, valid when OK is false.
	Err string
}

// Parser states. The machine is deterministic, so the first token it
// rejects is the earliest conflict.
type pstate int

const (
	sValue         pstate = iota // a value is expected (continuation on stack)
	sObjKeyOrEnd                 // after '{': a key string or '}'
	sObjKey                      // after ',' in an object: a key string
	sObjColon                    // after an object key: ':'
	sObjAfterValue               // after a value in an object: ',' or '}'
	sArrValOrEnd                 // after '[': a value or ']'
	sArrValue                    // after ',' in an array: a value
	sArrAfterValue               // after a value in an array: ',' or ']'
	sDone                        // the document is complete
)

type machine struct {
	cur  pstate
	cont []pstate // continuation stack, top is last
}

func newMachine() *machine { return &machine{cur: sValue, cont: []pstate{sDone}} }

func (m *machine) pop() pstate {
	top := m.cont[len(m.cont)-1]
	m.cont = m.cont[:len(m.cont)-1]
	return top
}

func (m *machine) clone() *machine {
	return &machine{cur: m.cur, cont: append([]pstate(nil), m.cont...)}
}

// step consumes one complete token; it reports whether the token is
// acceptable in the current state.
func (m *machine) step(kind tokKind) bool {
	switch m.cur {
	case sValue, sArrValOrEnd, sArrValue:
		if kind == kRBracket && m.cur == sArrValOrEnd {
			m.cur = m.pop()
			return true
		}
		switch kind {
		case kLBrace:
			if m.cur != sValue {
				m.cont = append(m.cont, sArrAfterValue)
			}
			m.cur = sObjKeyOrEnd
		case kLBracket:
			if m.cur != sValue {
				m.cont = append(m.cont, sArrAfterValue)
			}
			m.cur = sArrValOrEnd
		case kString, kNumber, kTrue, kFalse, kNull:
			if m.cur == sValue {
				m.cur = m.pop()
			} else {
				m.cur = sArrAfterValue
			}
		default:
			return false
		}
		return true
	case sObjKeyOrEnd:
		switch kind {
		case kRBrace:
			m.cur = m.pop()
		case kString:
			m.cur = sObjColon
		default:
			return false
		}
		return true
	case sObjKey:
		if kind != kString {
			return false
		}
		m.cur = sObjColon
		return true
	case sObjColon:
		if kind != kColon {
			return false
		}
		m.cont = append(m.cont, sObjAfterValue)
		m.cur = sValue
		return true
	case sObjAfterValue:
		switch kind {
		case kComma:
			m.cur = sObjKey
		case kRBrace:
			m.cur = m.pop()
		default:
			return false
		}
		return true
	case sArrAfterValue:
		switch kind {
		case kComma:
			m.cur = sArrValue
		case kRBracket:
			m.cur = m.pop()
		default:
			return false
		}
		return true
	}
	return false // sDone: no token may follow
}

// stateCategories returns the categories of tokens that may legally start
// at the cursor when the machine is in state s.
func stateCategories(s pstate) []TokenCategory {
	switch s {
	case sValue, sArrValue:
		return []TokenCategory{CatObjectStart, CatArrayStart, CatString, CatNumber, CatBoolean, CatNull}
	case sArrValOrEnd:
		return []TokenCategory{CatObjectStart, CatArrayStart, CatArrayEnd, CatString, CatNumber, CatBoolean, CatNull}
	case sObjKeyOrEnd:
		return []TokenCategory{CatObjectEnd, CatString}
	case sObjKey:
		return []TokenCategory{CatString}
	case sObjColon:
		return []TokenCategory{CatColon}
	case sObjAfterValue:
		return []TokenCategory{CatObjectEnd, CatComma}
	case sArrAfterValue:
		return []TokenCategory{CatArrayEnd, CatComma}
	}
	return nil // sDone
}

// minComplete returns the shortest suffix that completes the document
// from the given machine state. Closing the innermost open construct is
// always possible and never longer than extending it, so a direct
// recursion yields the minimum.
func minComplete(m *machine) string {
	var b strings.Builder
	cur := m.cur
	cont := append([]pstate(nil), m.cont...)
	pop := func() pstate {
		top := cont[len(cont)-1]
		cont = cont[:len(cont)-1]
		return top
	}
	for {
		switch cur {
		case sDone:
			return b.String()
		case sObjKeyOrEnd, sObjAfterValue:
			b.WriteByte('}')
			cur = pop()
		case sObjKey:
			b.WriteString(`"":1`) // shortest key, colon, shortest value
			cur = sObjAfterValue
		case sObjColon:
			b.WriteString(`:1`)
			cur = sObjAfterValue
		case sArrValOrEnd, sArrAfterValue:
			b.WriteByte(']')
			cur = pop()
		case sArrValue:
			b.WriteByte('1') // shortest value
			cur = sArrAfterValue
		case sValue:
			b.WriteByte('1')
			cur = pop()
		}
	}
}

func mergeCategories(a, b []TokenCategory) []TokenCategory {
	set := make(map[TokenCategory]bool, len(a)+len(b))
	for _, c := range a {
		set[c] = true
	}
	for _, c := range b {
		set[c] = true
	}
	var out []TokenCategory
	for _, c := range categoryOrder {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

// Complete analyses text up to cursor (a byte offset) and reports the
// legal token categories at the cursor plus the shortest legal completion
// suffix. Text after the cursor is ignored; the completion never modifies
// anything before the cursor.
func Complete(text string, cursor int) Result {
	if cursor < 0 || cursor > len(text) {
		return Result{ErrOffset: cursor, Err: "cursor out of range"}
	}
	src := text[:cursor]
	toks, open, lerr := lex(src)
	m := newMachine()
	for _, t := range toks {
		if !m.step(t.kind) {
			return Result{ErrOffset: t.start, Err: "unexpected token"}
		}
	}
	if lerr != nil {
		return Result{ErrOffset: lerr.off, Err: lerr.msg}
	}
	if open != nil {
		return completeOpen(m, open)
	}
	return Result{
		OK:         true,
		Categories: stateCategories(m.cur),
		Suffix:     minComplete(m),
	}
}

// completeOpen handles a prefix that ends in the middle of a token.
func completeOpen(m *machine, o *openTok) Result {
	switch o.kind {
	case openString, openStringEscape, openStringUnicode:
		next := m.clone()
		if !next.step(kString) {
			return Result{ErrOffset: o.start, Err: "unexpected string"}
		}
		closeStr := `"`
		switch o.kind {
		case openStringEscape:
			closeStr = `n"` // cheapest escape char, then close
		case openStringUnicode:
			closeStr = strings.Repeat("0", o.hexNeed) + `"`
		}
		return Result{
			OK:         true,
			Categories: []TokenCategory{CatString},
			Suffix:     closeStr + minComplete(next),
		}
	case openNumber:
		next := m.clone()
		if !next.step(kNumber) {
			return Result{ErrOffset: o.start, Err: "unexpected number"}
		}
		if o.needDigit {
			return Result{
				OK:         true,
				Categories: []TokenCategory{CatNumber},
				Suffix:     "0" + minComplete(next),
			}
		}
		// The number is complete but extendable: the next byte may either
		// continue it or start a fresh token.
		return Result{
			OK:         true,
			Categories: mergeCategories([]TokenCategory{CatNumber}, stateCategories(next.cur)),
			Suffix:     minComplete(next),
		}
	case openKeyword:
		next := m.clone()
		if !next.step(o.kw) {
			return Result{ErrOffset: o.start, Err: "unexpected keyword"}
		}
		cat := CatBoolean
		if o.kw == kNull {
			cat = CatNull
		}
		return Result{
			OK:         true,
			Categories: []TokenCategory{cat},
			Suffix:     o.remain + minComplete(next),
		}
	}
	return Result{ErrOffset: o.start, Err: "unexpected token"}
}
