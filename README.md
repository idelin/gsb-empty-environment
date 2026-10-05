# expr-syntax-repairer

Min-cost syntax repairer for a small expression language (integers,
identifiers, function calls, arrays, `+ - * /`, parentheses), written in
TypeScript with no dependencies.

```
node src/cli.ts "1 + * f(2,]"
# cost: 3 (429 expansions)
#   delete token 1 ('+' at pos 2)
#   insert '[' before token 7 (']' at pos 10)
#   insert ')' at end of input
# repaired: 1 * f ( 2 , [ ] )
```

Run the tests (Node >= 22.6, runs TypeScript directly):

```
npm test
```

## Language

```
Expr  -> Term (('+'|'-') Term)*
Term  -> Atom (('*'|'/') Atom)*
Atom  -> INT | IDENT ['(' [Expr (',' Expr)*] ')']
       | '(' Expr ')' | '[' [Expr (',' Expr)*] ']'
```

Unknown characters become `INVALID` tokens that the repairer can delete,
so lexical errors are handled by the same mechanism as syntax errors.

## Repair model

- Edits: **insert** a token at any gap, **delete** any input token.
  Both cost **1**. (Keeping a token costs 0.)
- The repair script is reported in canonical left-to-right walk order:
  at each gap, insertions first, then the keep/delete decision on the
  following token. Each physical repair has exactly one canonical script
  (an insertion never immediately follows a deletion in the script).
- The result is the repair with the **smallest cost**; ties are broken by
  the **lexicographically smallest script**, where edits compare by
  position, then kind (insert before delete), then inserted token text.
- Kept tokens retain their original source positions; inserted tokens are
  anchored at the insertion point. The result includes the edit script,
  the repaired token stream, and the parse tree (`ast`).

## Algorithm

`repairTokens` (`src/repair.ts`) runs Dijkstra over configurations
`(input index, LL(1) parser stack, justDeleted)`, prioritized by
`(cost, script)`. Because every edit costs exactly 1, competing scripts
for a configuration have equal length, which makes lexicographic Dijkstra
sound: the first accepting configuration popped from the priority queue
is globally optimal. The search never treats the first parseable script
it happens to generate as the answer.

Pruning (both sound):

- upper bound: cost of a greedy repair (two policies) and the trivial
  "delete everything, insert one `INT`" repair;
- lower bound: unmatched `)`/`]` already pending on the parser stack must
  be matched, and the remaining input can supply only so many closers.

## Budget and bounds

`repair(source, { budget })` caps the number of expanded configurations
(default 100 000). On exhaustion it returns **only** bounds:

```ts
{ status: "budget-exceeded", lowerBound, upperBound, expansions }
```

- `lowerBound` = min `cost + lowerBoundEstimate` over the search queue
  (every undiscovered repair passes through the queue);
- `upperBound` = cost of the best repair known (greedy/trivial).

## Verification

`src/bruteforce.ts` is an independent enumerator: it does not simulate
the parser during the search, materializes output token strings, and
checks grammaticality only on complete outputs. `test/repair.test.ts`
cross-checks the main algorithm against it on:

- all 156 token strings of length <= 3 over a 5-symbol alphabet
  (cost and exact edit script must match);
- random inputs of length 4-5;
- hand-picked cases (empty input, lexical errors, position preservation,
  budget exhaustion).

The brute force is given the main algorithm's cost as its cap, which is a
sound optimality check: any cheaper or lexicographically smaller script
the main search could have missed still lies inside the enumerated space.
