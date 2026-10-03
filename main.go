package main

import (
	"fmt"
	"strings"
	"unicode"
)

// tokenMatcher matches the terminal "id" against identifier tokens and
// every other terminal by exact string equality.
func tokenMatcher(terminal, tok string) bool {
	if terminal == "id" {
		return isIdent(tok)
	}
	return terminal == tok
}

func isIdent(tok string) bool {
	if tok == "" {
		return false
	}
	for i, r := range tok {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

// arithmeticGrammar is the ambiguous expression grammar
//
//	E -> E + E | E * E | id
//
// used in the acceptance criteria.
func arithmeticGrammar() *Grammar {
	g := NewGrammar("E",
		Rule{LHS: "E", RHS: []string{"E", "+", "E"}},
		Rule{LHS: "E", RHS: []string{"E", "*", "E"}},
		Rule{LHS: "E", RHS: []string{"id"}},
	)
	g.MatchTerminal = tokenMatcher
	return g
}

// exprPrecedence encodes the usual arithmetic conventions:
// '*' binds tighter than '+', both are left associative.
var exprPrecedence = &Precedence{
	Level:      map[string]int{"+": 1, "*": 2},
	RightAssoc: map[string]bool{},
}

func main() {
	g := arithmeticGrammar()
	input := "a + b * c"
	tokens := strings.Fields(input)
	fmt.Printf("Grammar:\n  E -> E + E | E * E | id\n")
	fmt.Printf("Input: %q  tokens: %v\n\n", input, tokens)

	chart := Parse(g, tokens)
	fmt.Print(chart.String())
	fmt.Println("Accepted:", chart.Accepted())

	trees := chart.Trees()
	fmt.Printf("\nAmbiguous grammar: %d legal parse tree(s)\n", len(trees))
	for i, t := range trees {
		fmt.Printf("\n-- tree %d: %s\n", i+1, t.String())
		fmt.Print(t.Pretty())
	}

	filtered := exprPrecedence.Filter(trees)
	fmt.Printf("\nAfter disambiguation (* > +, left associative): %d tree(s)\n", len(filtered))
	for _, t := range filtered {
		fmt.Printf("\n%s\n", t.String())
		fmt.Print(t.Pretty())
	}
}
