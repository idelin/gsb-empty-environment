package localfmt

import (
	"fmt"
	"strings"
)

// Patch replaces Src[Start:End] with Replacement.
type Patch struct {
	Start, End  int
	Replacement string
}

// Result bundles the patch with the expansion rationale and verifications.
type Result struct {
	Patch       Patch
	Rationale   []string
	NodeKind    NodeKind
	NodeSpan    [2]int
	Formatted   string // formatted text of the node itself (no attached ws)
	Verified    bool
	VerifyNotes []string
}

// FormatSelection formats the smallest complete syntax node covering the
// byte selection [selStart, selEnd) of src, touching only that node and its
// attached horizontal whitespace, then runs all verifications.
func FormatSelection(src string, selStart, selEnd int) (*Result, error) {
	res, err := formatSelection(src, selStart, selEnd)
	if err != nil {
		return nil, err
	}
	out := Apply(src, res.Patch)
	res.VerifyNotes = append(res.VerifyNotes, verifyOutside(src, out, res.Patch)...)
	res.VerifyNotes = append(res.VerifyNotes, verifyIdempotent(out, res.Patch.Start, res.Patch.Start+len(res.Patch.Replacement))...)
	res.VerifyNotes = append(res.VerifyNotes, verifySemantics(src[res.NodeSpan[0]:res.NodeSpan[1]], res.Formatted)...)
	res.Verified = true
	for _, n := range res.VerifyNotes {
		if strings.HasPrefix(n, "FAIL") {
			res.Verified = false
		}
	}
	return res, nil
}

// formatSelection computes the patch without running verifications.
func formatSelection(src string, selStart, selEnd int) (*Result, error) {
	if selStart < 0 || selEnd > len(src) || selStart > selEnd {
		return nil, fmt.Errorf("invalid selection [%d,%d) for %d-byte source", selStart, selEnd, len(src))
	}
	root, toks, err := Parse(src)
	if err != nil {
		return nil, err
	}
	res := &Result{}

	// Step 1: snap the selection outward to whole tokens. A selection that
	// cuts through a token grows to cover that token entirely.
	expS, expE := selStart, selEnd
	for _, t := range toks {
		if t.Kind == TEOF {
			continue
		}
		if t.Start < expE && t.End > expS { // overlaps selection
			if t.Start < expS {
				expS = t.Start
			}
			if t.End > expE {
				expE = t.End
			}
		}
	}
	if expS != selStart || expE != selEnd {
		res.Rationale = append(res.Rationale, fmt.Sprintf(
			"选区 [%d,%d) 切到 token 中间，外扩到完整 token 边界 [%d,%d)", selStart, selEnd, expS, expE))
	} else {
		res.Rationale = append(res.Rationale, fmt.Sprintf(
			"选区 [%d,%d) 已对齐 token 边界", selStart, selEnd))
	}

	// Step 2: find the smallest AST node fully covering the expanded range.
	node := SmallestCovering(root, expS, expE)
	if node == nil {
		return nil, fmt.Errorf("no syntax node covers [%d,%d)", expS, expE)
	}
	res.NodeKind = node.Kind
	res.NodeSpan = [2]int{node.Start, node.End}
	if node.Start != expS || node.End != expE {
		res.Rationale = append(res.Rationale, fmt.Sprintf(
			"[%d,%d) 不是完整语法节点，扩到最小覆盖节点 %s [%d,%d): %q",
			expS, expE, node.Kind, node.Start, node.End, src[node.Start:node.End]))
	} else {
		res.Rationale = append(res.Rationale, fmt.Sprintf(
			"命中完整节点 %s [%d,%d): %q", node.Kind, node.Start, node.End, src[node.Start:node.End]))
	}

	// Step 3: extend the patch range over attached horizontal whitespace
	// (never across newlines, so line structure outside the node is intact).
	patchS, patchE := node.Start, node.End
	for patchS > 0 && (src[patchS-1] == ' ' || src[patchS-1] == '\t') {
		patchS--
	}
	for patchE < len(src) && (src[patchE] == ' ' || src[patchE] == '\t') {
		patchE++
	}
	if patchS != node.Start || patchE != node.End {
		res.Rationale = append(res.Rationale, fmt.Sprintf(
			"补丁范围并入附着水平空白: [%d,%d)", patchS, patchE))
	}

	// Step 4: re-emit the node's tokens with canonical spacing. Comments are
	// tokens too, so their attachment inside the node is preserved verbatim.
	var inner []Token
	for _, t := range toks {
		if t.Start >= node.Start && t.End <= node.End && t.Kind != TEOF {
			inner = append(inner, t)
		}
	}
	res.Formatted = formatTokens(inner)

	var repl strings.Builder
	if src[patchS:node.Start] != "" {
		repl.WriteString(" ") // attached whitespace normalizes to one space
	}
	repl.WriteString(res.Formatted)
	if src[node.End:patchE] != "" {
		repl.WriteString(" ")
	}
	res.Patch = Patch{Start: patchS, End: patchE, Replacement: repl.String()}
	return res, nil
}

// formatTokens re-emits tokens with canonical spacing for arithmetic and
// call syntax. Comment tokens are kept verbatim; a // comment keeps its
// terminating newline so it cannot swallow following tokens.
func formatTokens(toks []Token) string {
	var b strings.Builder
	for i, t := range toks {
		if i > 0 && needSpace(toks, i) {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
		if t.Kind == TComment && strings.HasPrefix(t.Text, "//") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// isUnaryMinus reports whether toks[i] is a unary minus: a '-' with no
// operand-ending token immediately before it.
func isUnaryMinus(toks []Token, i int) bool {
	if toks[i].Kind != TOp || toks[i].Text != "-" {
		return false
	}
	if i == 0 {
		return true
	}
	prev := toks[i-1]
	return prev.Kind == TOp || prev.Kind == TLParen || prev.Kind == TComma
}

func needSpace(toks []Token, i int) bool {
	prev, cur := toks[i-1], toks[i]
	// A line comment ended the line; the newline is already emitted.
	if prev.Kind == TComment && strings.HasPrefix(prev.Text, "//") {
		return false
	}
	// No space just inside parens or before a comma.
	if prev.Kind == TLParen || cur.Kind == TRParen || cur.Kind == TComma {
		return false
	}
	// One space after a comma.
	if prev.Kind == TComma {
		return true
	}
	// Call: no space between callee and '('.
	if cur.Kind == TLParen && prev.Kind == TIdent {
		return false
	}
	// Unary minus binds tight to its operand on the right; its left side
	// falls through to the operator rules (space after a binary operator,
	// no space after '(' or start).
	if prev.Kind == TOp && prev.Text == "-" && isUnaryMinus(toks, i-1) {
		return false
	}
	// Binary operators get one space on each side.
	if cur.Kind == TOp || prev.Kind == TOp {
		return true
	}
	// Comments act as operands: keep a single space around them.
	if prev.Kind == TComment || cur.Kind == TComment {
		return true
	}
	return false
}

func verifyOutside(src, out string, p Patch) []string {
	ok := out[:p.Start] == src[:p.Start] &&
		out[p.Start+len(p.Replacement):] == src[p.End:]
	if ok {
		return []string{"OK 补丁之外的字节逐字节不变"}
	}
	return []string{"FAIL 补丁之外的字节发生变化"}
}

func verifyIdempotent(out string, start, end int) []string {
	res2, err := formatSelection(out, start, end)
	if err != nil {
		return []string{"FAIL 二次格式化出错: " + err.Error()}
	}
	p2 := res2.Patch
	if out[p2.Start:p2.End] == p2.Replacement {
		return []string{"OK 幂等: 再次格式化无改动"}
	}
	return []string{fmt.Sprintf("FAIL 非幂等: 二次格式化仍产生补丁 [%d,%d)", p2.Start, p2.End)}
}

// verifySemantics re-parses the original node text and the formatted text and
// compares ASTs structurally, ignoring positions and comments. This proves
// comments wedged between operators do not alter meaning.
func verifySemantics(origText, formatted string) []string {
	a, _, errA := Parse(origText)
	b, _, errB := Parse(formatted)
	if errA != nil || errB != nil {
		return []string{fmt.Sprintf("FAIL 重新解析失败: %v / %v", errA, errB)}
	}
	if sameAST(a, b) {
		return []string{"OK 语义不变: 注释夹在运算符两侧时 AST 结构一致"}
	}
	return []string{"FAIL 语义改变: AST 结构不一致"}
}

func sameAST(a, b *Node) bool {
	if a.Kind != b.Kind || a.Op != b.Op || a.Name != b.Name {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !sameAST(a.Args[i], b.Args[i]) {
			return false
		}
	}
	eq := func(x, y *Node) bool {
		if x == nil || y == nil {
			return x == y
		}
		return sameAST(x, y)
	}
	return eq(a.L, b.L) && eq(a.R, b.R) && eq(a.X, b.X)
}

// Apply returns src with the patch applied.
func Apply(src string, p Patch) string {
	return src[:p.Start] + p.Replacement + src[p.End:]
}
