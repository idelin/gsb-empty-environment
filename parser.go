package docexec

import (
	"fmt"
	"strings"
)

// Instr is a single instruction line inside a runnable block.
type Instr struct {
	Text string // raw instruction text (trimmed)
	Line int    // 1-based line number in the source document
}

// Block is a fenced ```run code block in the document.
type Block struct {
	ID        string
	Needs     []string // ids of blocks that must succeed first
	Defines   []string // namespace names this block declares it defines
	Lines     []Instr  // instruction lines (comments/blank kept for line mapping)
	StartLine int      // line of the opening fence
	EndLine   int      // line of the closing fence
	Index     int      // document order among runnable blocks
}

// Document is a parsed markdown-like document.
type Document struct {
	Blocks []*Block
}

const fence = "```"

// Parse scans src and collects all ```run fenced blocks.
// Fence info string format: run id=NAME needs=a,b defines=X,Y
func Parse(src string) (*Document, error) {
	doc := &Document{}
	lines := strings.Split(src, "\n")
	var cur *Block    // non-nil while inside a run block
	skipping := false // true while inside a non-run fence
	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		isFence := strings.HasPrefix(trimmed, fence)
		switch {
		case skipping:
			if isFence {
				skipping = false
			}
		case cur != nil:
			if isFence {
				cur.EndLine = lineNo
				cur = nil
			} else {
				cur.Lines = append(cur.Lines, Instr{Text: trimmed, Line: lineNo})
			}
		default:
			if !isFence {
				continue
			}
			info := strings.TrimSpace(trimmed[len(fence):])
			attrs, err := parseInfo(info)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			if attrs == nil {
				skipping = true // ordinary code fence: ignore its body
				continue
			}
			cur = &Block{
				ID:        attrs["id"],
				StartLine: lineNo,
				Index:     len(doc.Blocks),
			}
			cur.Needs = splitList(attrs["needs"])
			cur.Defines = splitList(attrs["defines"])
			if cur.ID == "" {
				cur.ID = fmt.Sprintf("block-%d", cur.Index+1)
			}
			doc.Blocks = append(doc.Blocks, cur)
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("line %d: unterminated fence for block %q", cur.StartLine, cur.ID)
	}
	return doc, nil
}

// parseInfo parses the fence info string. Returns nil attrs (and nil error)
// when the fence is not a runnable block.
func parseInfo(info string) (map[string]string, error) {
	fields := strings.Fields(info)
	if len(fields) == 0 || fields[0] != "run" {
		return nil, nil
	}
	attrs := map[string]string{}
	for _, f := range fields[1:] {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("malformed fence attribute %q (want key=value)", f)
		}
		attrs[kv[0]] = kv[1]
	}
	return attrs, nil
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
