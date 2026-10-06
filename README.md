# editfa

Minimum-cost edit scripts into the language of a DFA.

Given a deterministic finite automaton, an input string, and positive
integer costs for insert / delete / replace, `MinEditScript` finds the
cheapest edit script that turns the input into an accepted string.
Ties are broken by shortest output, then lexicographic order.

```go
d := &editfa.DFA{
    Start:  0,
    Accept: []bool{true, false},                 // accepts (ab)*
    Trans:  []map[byte]int{{'a': 1}, {'b': 0}},
}
res, err := editfa.MinEditScript(d, "aba", editfa.Costs{Insert: 1, Delete: 1, Replace: 1})
// res.Cost == 1, res.Output == "ab", res.Script == [keep a, keep b, delete a]
out, err := editfa.Apply("aba", res.Script)     // out == "ab"
```

- `DFA.Accepts` checks membership; `DFA.Alphabet` lists transition symbols.
- `Apply` replays a script on the input and validates every step.
- `ScriptCost` totals the cost of a script.
- Returns `ErrNoSolution` when no accepted string is reachable.

The solver runs Dijkstra over (input position × DFA state) with labels
ordered by (cost, output length, output), so the first accepting state
settled is optimal under all three criteria. Tests cross-check against
brute-force enumeration of accepted strings for small automata,
including empty inputs and cyclic automata.
