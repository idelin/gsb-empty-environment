// Package configcomplete provides context-aware completion for a
// JSON-like configuration language with nested objects, arrays and
// escaped strings:
//
//	document := value
//	value    := object | array | string | number | "true" | "false" | "null"
//	object   := "{" [ string ":" value { "," string ":" value } ] "}"
//	array    := "[" [ value { "," value } ] "]"
//	string   := '"' { char | "\" escape | "\u" hex hex hex hex } '"'
//	number   := [-] ( "0" | digit1-9 {digit} ) [ "." digit {digit} ]
//	            [ ("e"|"E") ["+"|"-"] digit {digit} ]
//
// Whitespace (space, tab, CR, LF) may appear between tokens. All
// positions are byte offsets into the raw text; in particular a cursor
// inside a string is located by raw bytes, so the two bytes of a "\n"
// escape are two distinct positions.
//
// Completion is prefix-based: given the text before the cursor, Complete
// reports which token categories may legally follow and the shortest
// suffix that turns the prefix into a complete valid document. Appending
// the suffix never alters tokens that were already valid before the
// cursor; an incomplete token at the cursor is completed, not rewritten.
// If the prefix itself cannot be extended to any valid document, the
// result carries the position of the earliest conflict instead.
package configcomplete

import "strings"

// Conflict describes a position where the text before the cursor can no
// longer be extended into a valid document. Pos is the byte offset of the
// earliest such position.
type Conflict struct {
	Pos int
	Msg string
}

// Result is the outcome of a completion query. When Conflict is non-nil
// the prefix is invalid and the other fields are meaningless. Otherwise
// Categories lists the token categories that may legally appear at the
// cursor (empty when the document is already complete) and Suffix is the
// shortest string that completes the document.
type Result struct {
	Conflict   *Conflict
	Categories []TokenCategory
	Suffix     string
}

// CompleteAt analyzes text up to the cursor (a byte offset) and ignores
// everything after it.
func CompleteAt(text string, cursor int) Result {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}
	return Complete(text[:cursor])
}

// Complete analyzes a document prefix.
func Complete(prefix string) Result {
	lr := lex(prefix)
	if lr.conflict >= 0 {
		return Result{Conflict: &Conflict{Pos: lr.conflict, Msg: lr.msg}}
	}
	p := &parser{state: psStart}
	for _, t := range lr.tokens {
		if !p.feed(t.cat) {
			return Result{Conflict: &Conflict{Pos: t.start, Msg: "unexpected " + t.cat.String()}}
		}
	}
	if lr.partial != nil {
		return completePartial(p, lr.partial)
	}
	return Result{
		Categories: legalCategories(p.state),
		Suffix:     minComplete(p.state, p.stack),
	}
}

// Valid reports whether text is a complete, well-formed document.
func Valid(text string) bool {
	lr := lex(text)
	if lr.conflict >= 0 || lr.partial != nil {
		return false
	}
	p := &parser{state: psStart}
	for _, t := range lr.tokens {
		if !p.feed(t.cat) {
			return false
		}
	}
	return p.state == psDone
}

func completePartial(p *parser, pt *partialToken) Result {
	keyPos := p.state == psObjKeyOrEnd || p.state == psObjKey
	if keyPos && pt.cat != CatString {
		return Result{Conflict: &Conflict{Pos: pt.start, Msg: "expected string key, got " + pt.cat.String()}}
	}
	if !keyPos && !valueState(p.state) {
		return Result{Conflict: &Conflict{Pos: pt.start, Msg: "unexpected " + pt.cat.String()}}
	}
	after := returnState(p.state)
	if keyPos {
		after = psObjColon
	}
	switch pt.cat {
	case CatString:
		var cats []TokenCategory
		var close string
		switch pt.str {
		case strNormal:
			cats = []TokenCategory{CatStringChar, CatEscape, CatStringEnd}
			close = `"`
		case strEscape:
			cats = []TokenCategory{CatEscape}
			close = `n"`
		case strUnicode:
			cats = []TokenCategory{CatEscapeHex}
			close = strings.Repeat("0", 4-pt.hexHave) + `"`
		}
		return Result{Categories: cats, Suffix: close + minComplete(after, p.stack)}
	case CatNumber:
		return Result{
			Categories: []TokenCategory{CatNumber},
			Suffix:     "0" + minComplete(after, p.stack),
		}
	default:
		var word string
		switch pt.cat {
		case CatTrue:
			word = "true"
		case CatFalse:
			word = "false"
		default:
			word = "null"
		}
		rest := word[pt.end-pt.start:]
		return Result{
			Categories: []TokenCategory{pt.cat},
			Suffix:     rest + minComplete(after, p.stack),
		}
	}
}
