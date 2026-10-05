// Package docexec executes fenced example blocks in a Markdown document.
//
// Blocks are declared with a fence whose info string starts with "run",
// followed by key=value options:
//
//	```run name=init needs=base,other
//	set greeting hello
//	```
//
// Blocks run in dependency order against a shared namespace, and each
// block's output is mapped back to its location in the source document.
package docexec

import (
	"fmt"
	"strings"
)

// Block is a single runnable code block in a document.
type Block struct {
	Name      string   // unique block name from the info string
	Needs     []string // names of blocks that must succeed first
	Code      string   // block body (mini instruction language)
	StartLine int      // 1-based line of the opening fence
	EndLine   int      // 1-based line of the closing fence
	Index     int      // order of appearance in the document
}

// Document is a parsed Markdown source with its runnable blocks.
type Document struct {
	Lines  []string // original source lines (1-based via Lines[i-1])
	Blocks []*Block
}

// Parse scans Markdown text and collects fenced blocks whose info string
// starts with "run". Non-runnable fences and prose are preserved as lines.
func Parse(text string) *Document {
	text = strings.Replace(text, "\r\n", "\n", -1)
	lines := strings.Split(text, "\n")
	doc := &Document{Lines: lines}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, "```") {
			continue
		}
		info := strings.TrimSpace(strings.TrimPrefix(line, "```"))
		name, needs, ok := parseInfo(info)
		if !ok {
			continue
		}
		start := i + 1 // 1-based opening fence line
		var body []string
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "```") {
				end = j + 1 // 1-based closing fence line
				i = j
				break
			}
			body = append(body, lines[j])
		}
		if end < 0 {
			// Unterminated fence: treat the rest of the file as the body.
			end = len(lines)
			i = len(lines)
		}
		if name == "" {
			// Runnable fence without a name gets a stable synthetic one.
			name = fmt.Sprintf("block#%d", len(doc.Blocks))
		}
		doc.Blocks = append(doc.Blocks, &Block{
			Name:      name,
			Needs:     needs,
			Code:      strings.Join(body, "\n"),
			StartLine: start,
			EndLine:   end,
			Index:     len(doc.Blocks),
		})
	}
	return doc
}

// parseInfo parses an info string like `run name=x needs=a,b`.
// It reports ok=false for fences that are not runnable blocks.
func parseInfo(info string) (name string, needs []string, ok bool) {
	fields := strings.Fields(info)
	if len(fields) == 0 || fields[0] != "run" {
		return "", nil, false
	}
	for _, f := range fields[1:] {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "name":
			name = kv[1]
		case "needs":
			for _, n := range strings.Split(kv[1], ",") {
				if n = strings.TrimSpace(n); n != "" {
					needs = append(needs, n)
				}
			}
		}
	}
	return name, needs, true
}
