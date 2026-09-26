# Streaming longest-match lexer (Python 3.11, stdlib only)

A streaming tokenizer with explicit DFA rules, lexical states, checkpoints
and bounded buffering. No regex or automata libraries are used; alphabets
and transition tables are plain data passed in by the caller.

## Files

- `lexer.py` — the engine (`Lexer`, `Rule`, `DFA`, `Action`, `Status`).
- `reference.py` — slow O(n^2) whole-input reference lexer used as ground truth.
- `sample_lang.py` — an example language (keywords, idents, ints, operators,
  nestable `/* */` comments via the state stack).
- `test_lexer.py` — unittest suite, including randomized chunking fuzz
  against the reference.
- `counterexamples.py` — independently executable failure modes of naive
  approaches, each contrasted with this lexer.
- `run_sample.py` — end-to-end demo.

## Semantics

- **Token selection**: longest match; ties broken by explicit rule priority
  (lower wins), then rule list order. Several rules may accept the same
  lexeme with different actions (e.g. different state transitions); the
  priority order selects exactly one action. Keywords beat identifiers at
  equal length because they carry a better priority.
- **No early delivery**: a token is committed only when the match is
  *determined* — every live DFA died (or cannot extend) past the accepting
  position, or `finish()` was called. A tail that may still extend
  (`if` -> `ifx`) is held back across chunk boundaries.
- **Lexical states**: rules are grouped per state; actions KEEP / PUSH /
  POP / SWITCH a state stack (used for nestable comments in the sample).
- **Bounded buffering**: while undetermined, the uncommitted window may
  grow to `max_pending` chars; beyond that `feed()` returns
  `Status.INCOMPLETE` with the buffered size and work spent — it never
  mis-cuts a shorter token. Work per `feed()` is bounded by
  `(buffered + len(chunk)) * (#rules of the current state)` transition
  lookups. The over-long match can still complete later.
- **Errors**: no matching rule reports `Status.ERROR` with the precise
  absolute stream offset; `recover()` skips one char and resumes.
- **Time travel**: `checkpoint()` / `rollback(cp)` restore scan offset,
  state stack, token log and pull cursor; `undo_input(n)` retracts the
  last n fed characters so corrected input can be re-fed.

## Run

```
python3.11 -m unittest -v      # test suite (26 tests)
python3.11 run_sample.py       # demo: chunked feed, checkpoint, recovery
python3.11 counterexamples.py  # naive-approach failures vs. this lexer
```
