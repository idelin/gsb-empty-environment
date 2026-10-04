package macroexp

import "fmt"

// Pos is a source position (1-based line and column).
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

func (p Pos) IsZero() bool { return p.File == "" && p.Line == 0 && p.Col == 0 }

// Kind classifies tokens.
type Kind int

const (
	Ident Kind = iota
	Number
	Str
	Punct
	Hash // '#'
)

// Frame is one level of macro expansion. The full expansion chain of a
// token is a list of frames, outermost first.
type Frame struct {
	Macro   string
	DefPos  Pos  // where the macro was defined
	CallPos Pos  // where the macro was called
	InArg   bool // expansion happened while expanding this call's actual argument
}

// Token is a lexical token carrying full provenance: where its text was
// defined, which call brought it into the output, and the complete chain
// of macro expansions that produced it.
type Token struct {
	Kind Kind
	Text string

	Pos     Pos     // physical location of this occurrence
	DefPos  Pos     // where the token text was written (macro body or source)
	CallPos Pos     // call site of the expansion that emitted it; zero for source tokens
	Chain   []Frame // full expansion chain, outermost first; nil for source tokens
	Sealed  bool    // already fully expanded; must not be expanded again
}
