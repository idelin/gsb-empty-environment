package main

import (
	"fmt"
	"strings"
)

// Patch describes a single byte-range replacement in the source.
type Patch struct {
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
}

// Checks reports the three invariants verified after formatting.
type Checks struct {
	OutsideUnchanged   bool `json:"outside_unchanged"`   // bytes outside the patch are identical
	Idempotent         bool `json:"idempotent"`          // formatting the replacement again is a no-op
	SemanticsPreserved bool `json:"semantics_preserved"` // token stream (incl. comments) is unchanged
}

// Result is the full output of one formatting run.
type Result struct {
	Patch     Patch    `json:"patch"`
	Rationale []string `json:"rationale"`
	Checks    Checks   `json:"checks"`
	Patched   string   `json:"-"`
}

// formatSelection formats the smallest syntax node covering [selStart, selEnd)
// and nothing else. It returns the patch, why the selection was expanded, and
// the verification results.
func formatSelection(src string, selStart, selEnd int) (*Result, error) {
	if selStart < 0 || selEnd < selStart || selEnd > len(src) {
		return nil, fmt.Errorf("invalid selection [%d, %d) for input of %d bytes", selStart, selEnd, len(src))
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	root, err := parse(toks)
	if err != nil {
		return nil, err
	}

	res := &Result{}
	start, end := selStart, selEnd

	// Step 1: a selection that cuts through a token grows to that token's
	// full extent first.
	start, end = expandToTokens(toks, start, end, src, res)

	// Step 2: grow to the smallest syntax node that fully contains the
	// (token-aligned) selection.
	node := findSmallestNode(root, start, end)
	if node == nil {
		return nil, fmt.Errorf("selection [%d, %d) is not inside any expression", selStart, selEnd)
	}
	if node.Start == start && node.End == end {
		res.Rationale = append(res.Rationale,
			fmt.Sprintf("selection already matches a %s at [%d, %d); no expansion needed", node.Kind, node.Start, node.End))
	} else {
		res.Rationale = append(res.Rationale,
			fmt.Sprintf("selection [%d, %d) does not align with a syntax node; expanded to enclosing %s at [%d, %d)",
				start, end, node.Kind, node.Start, node.End))
	}

	// Step 3: render only the node's own tokens (comments inside the node
	// travel with it; everything outside its byte span is untouched).
	nodeToks := toksInRange(toks, node.Start, node.End)
	replacement := render(nodeToks)
	if replacement == src[node.Start:node.End] {
		res.Rationale = append(res.Rationale, "node is already canonically formatted; patch is a no-op")
	}

	res.Patch = Patch{
		Start:       node.Start,
		End:         node.End,
		Original:    src[node.Start:node.End],
		Replacement: replacement,
	}
	res.Patched = src[:node.Start] + replacement + src[node.End:]

	// Verification 1: bytes outside the patch are unchanged.
	res.Checks.OutsideUnchanged =
		res.Patched[:node.Start] == src[:node.Start] &&
			res.Patched[len(res.Patched)-(len(src)-node.End):] == src[node.End:]

	// Verification 2: idempotency — re-lexing and re-rendering the
	// replacement must reproduce it byte for byte.
	res.Checks.Idempotent = checkIdempotent(replacement)

	// Verification 3: semantics — the token stream (operators, operands,
	// parens, commas and comments, ignoring whitespace) of the whole file
	// must be identical before and after.
	res.Checks.SemanticsPreserved = tokenStreamEqual(src, res.Patched)

	return res, nil
}

// expandToTokens widens [start, end) so it never cuts through a token.
func expandToTokens(toks []Token, start, end int, src string, res *Result) (int, int) {
	if start == end {
		// Cursor: if it sits inside a token, select that whole token.
		for _, t := range toks {
			if t.Start < start && start < t.End {
				res.Rationale = append(res.Rationale,
					fmt.Sprintf("cursor at byte %d falls inside %s %q at [%d, %d); expanded to the whole token",
						start, t.Kind, t.Text, t.Start, t.End))
				return t.Start, t.End
			}
		}
		return start, end
	}
	for _, t := range toks {
		if t.Start < start && start < t.End {
			res.Rationale = append(res.Rationale,
				fmt.Sprintf("selection start %d cuts through %s %q at [%d, %d); expanded to token boundary %d",
					start, t.Kind, t.Text, t.Start, t.End, t.Start))
			start = t.Start
		}
		if t.Start < end && end < t.End {
			res.Rationale = append(res.Rationale,
				fmt.Sprintf("selection end %d cuts through %s %q at [%d, %d); expanded to token boundary %d",
					end, t.Kind, t.Text, t.Start, t.End, t.End))
			end = t.End
		}
	}
	return start, end
}

// findSmallestNode returns the deepest node whose span contains [s, e).
func findSmallestNode(n *Node, s, e int) *Node {
	if n.Start > s || n.End < e {
		return nil
	}
	for _, c := range n.children() {
		if r := findSmallestNode(c, s, e); r != nil {
			return r
		}
	}
	return n
}

// toksInRange returns every token (comments included) fully inside [s, e).
func toksInRange(toks []Token, s, e int) []Token {
	var out []Token
	for _, t := range toks {
		if t.Start >= s && t.End <= e {
			out = append(out, t)
		}
	}
	return out
}

// render emits tokens with canonical spacing. It is a pure function of the
// token stream, which is what makes re-formatting idempotent.
func render(toks []Token) string {
	var b strings.Builder
	var prev *Token
	for i := range toks {
		t := &toks[i]
		if prev != nil && needSpace(prev, t) {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
		if t.Kind == kLineComment {
			// A line comment swallows the rest of its line, so the
			// following token must start on a fresh line.
			b.WriteByte('\n')
		}
		prev = t
	}
	return b.String()
}

func needSpace(prev, cur *Token) bool {
	// Spacing around comments.
	if cur.isComment() {
		return prev.Kind != kLParen
	}
	if prev.Kind == kLineComment {
		return false // newline was already emitted
	}
	if prev.Kind == kBlockComment {
		return cur.Kind != kRParen && cur.Kind != kComma
	}
	// No space before closers and separators.
	if cur.Kind == kRParen || cur.Kind == kComma {
		return false
	}
	// '(': tight after a callee, tight after another '('.
	if cur.Kind == kLParen {
		if cur.CallOpen {
			return false
		}
		return prev.Kind != kLParen
	}
	// No space after '('; one space after ','.
	if prev.Kind == kLParen {
		return false
	}
	if prev.Kind == kComma {
		return true
	}
	if cur.Kind == kOp && cur.Unary {
		// Prefix position follows an operator, '(' or ','; '(' and ','
		// were handled above, so only operators remain: keep a space
		// ("a * -b", "- -b") to avoid fused tokens like "*-" or "--".
		return true
	}
	// Unary minus binds tight to its operand.
	if prev.Kind == kOp && prev.Unary {
		return false
	}
	// Binary operators get a space on both sides.
	if prev.Kind == kOp || cur.Kind == kOp {
		return true
	}
	return false
}

// checkIdempotent parses the replacement as a standalone expression and
// re-renders it; a canonical form must be a fixed point.
func checkIdempotent(replacement string) bool {
	toks, err := lex(replacement)
	if err != nil {
		return false
	}
	root, err := parse(toks)
	if err != nil {
		return false
	}
	again := render(toksInRange(toks, root.Start, root.End))
	return again == replacement
}

// tokenStreamEqual compares the significant token stream (kind + text,
// comments included, whitespace excluded) of two sources.
func tokenStreamEqual(a, b string) bool {
	ta, err := lex(a)
	if err != nil {
		return false
	}
	tb, err := lex(b)
	if err != nil {
		return false
	}
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if ta[i].Kind != tb[i].Kind || ta[i].Text != tb[i].Text {
			return false
		}
	}
	return true
}
