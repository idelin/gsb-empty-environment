package docexec

import (
	"fmt"
	"strings"
)

// Report renders a plain-text execution report. Every block is listed with
// its source line range so output can be traced back to the document.
func Report(res *RunResult) string {
	var b strings.Builder
	for _, err := range res.Errors {
		fmt.Fprintf(&b, "validation: %v\n", err)
	}
	for _, r := range res.Results {
		fmt.Fprintf(&b, "lines %d-%d  %-10s  %s\n",
			r.Block.StartLine, r.Block.EndLine, r.Status, r.Block.Name)
		for _, line := range r.Output {
			fmt.Fprintf(&b, "  | %s\n", line)
		}
		if r.Err != nil {
			fmt.Fprintf(&b, "  ! %v\n", r.Err)
		}
		if r.Reason != "" {
			fmt.Fprintf(&b, "  ~ %s\n", r.Reason)
		}
	}
	return b.String()
}

// Annotate renders the original document with each block's output inserted
// immediately after its closing fence, as an "output" fenced block. The
// source text itself is left untouched, so positions stay traceable.
func Annotate(doc *Document, res *RunResult) string {
	byEnd := make(map[int]*BlockResult, len(res.Results))
	for _, r := range res.Results {
		byEnd[r.Block.EndLine] = r
	}
	var b strings.Builder
	for i, line := range doc.Lines {
		b.WriteString(line)
		b.WriteByte('\n')
		r, ok := byEnd[i+1] // lines are 1-based
		if !ok {
			continue
		}
		b.WriteString("```output\n")
		fmt.Fprintf(&b, "# %s", r.Status)
		if r.Err != nil {
			fmt.Fprintf(&b, ": %v", r.Err)
		}
		if r.Reason != "" {
			fmt.Fprintf(&b, ": %s", r.Reason)
		}
		b.WriteByte('\n')
		for _, out := range r.Output {
			b.WriteString(out)
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}
	return b.String()
}
