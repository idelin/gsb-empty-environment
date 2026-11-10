# sql-firewall-lexdiff

Differential lexer tester for SQL firewalls. It searches a finite dictionary of
byte fragments for the shortest concatenation whose `;` is parsed as string
content by one single-quote string lexer and as a statement separator by the
other.

## Lexer semantics

Both lexers consume a byte string through a small deterministic automaton:

- `O` outside any string literal
- `I` inside a string, ordinary byte
- `Q` inside, last byte was a single quote (pending close / doubled quote)
- `B` inside, last byte was a backslash (escape-on mode only)

They differ only in backslash handling:

- `escapes=off` (standard SQL): backslash is an ordinary byte; the only way to
  embed a quote is the doubled form `''`. A quote after `\` closes the string.
- `escapes=on` (MySQL-style): `\` escapes the next byte, so `\'` never closes a
  string. The doubled form `''` is still valid and behaves identically on both
  sides, so differences only arise through backslash/quote interaction.

A byte is annotated `IN` when it is read while inside a string (including an
escaped byte or the second quote of a doubled quote) and `OUT` otherwise. The
opening quote itself is a delimiter (`OUT`, note `opening quote`).

## Search

Enumerating every fragment sequence is exponential. Instead the tool runs both
lexers together as a product automaton with at most 4 x 4 = 16 states. A
priority queue ordered by `(byte length, lexicographic byte order)` performs a
uniform-cost search over product states:

- each frontier node is a fragment boundary plus the concatenations used;
- each `(product state, depth)` pair is settled at most once, shortest-first;
- applying a fragment that contains a differential `;` enqueues a candidate
  truncated at that byte; the first candidate popped is therefore the shortest
  byte string, with byte order breaking ties;
- the maximum number of concatenations (`-n`) is treated as a remaining-budget
  dimension, so depth limits are never collapsed across distinct states.

## Usage

```sh
go build -buildvcs=false -o lexdiff .

# default dictionary: ' \ ; ';
./lexdiff

# custom byte fragments and a tighter concatenation budget
./lexdiff -n 2 "a'\\" "';"

# machine-readable report
./lexdiff -json
```

Exit codes: `0` differential found, `2` none exists within the budget,
`1` usage or I/O error.
