// Command demo parses "a + b * c" against the ambiguous arithmetic
// grammar E -> E + E | E * E | id, prints the full Earley chart,
// enumerates all parse trees, and disambiguates by precedence and
// left associativity.
package main

import (
	"fmt"
	"strings"

	"earley"
)

// ArithmeticGrammar returns E -> E + E | E * E | id.
func ArithmeticGrammar() *earley.Grammar {
	return earley.NewGrammar("E",
		earley.R("E", "E", "+", "E"),
		earley.R("E", "E", "*", "E"),
		earley.R("E", "id"),
	)
}

// Tokenize splits an arithmetic expression into tokens, classifying
// identifiers as "id".
func Tokenize(input string) []earley.Token {
	var tokens []earley.Token
	for _, word := range strings.Fields(input) {
		switch word {
		case "+", "*":
			tokens = append(tokens, earley.Token{Type: word, Value: word})
		default:
			tokens = append(tokens, earley.Token{Type: "id", Value: word})
		}
	}
	return tokens
}

func main() {
	g := ArithmeticGrammar()
	input := "a + b * c"
	tokens := Tokenize(input)
	fmt.Printf("Grammar: E -> E + E | E * E | id\nInput:   %s\n\n", input)

	chart := g.Parse(tokens)
	fmt.Println("--- Earley chart ---")
	fmt.Print(chart.String())

	if !chart.Accepted() {
		fmt.Println("input rejected")
		return
	}
	fmt.Println("--- input accepted ---")

	trees := chart.ParseTrees()
	fmt.Printf("\n--- %d parse tree(s) ---\n", len(trees))
	for i, t := range trees {
		fmt.Printf("tree %d: %s\n", i+1, t.String())
		fmt.Print(t.Pretty())
	}

	prec := map[string]int{"+": 1, "*": 2}
	kept := earley.Disambiguate(trees, prec)
	fmt.Printf("--- disambiguated (* > +, left associative): %d tree(s) ---\n", len(kept))
	for _, t := range kept {
		fmt.Println(t.String())
	}
}
