# declang — declaration list with sync-point recovery and minimal repairs

A small declaration-list language implemented from scratch (no `eval`, no
external compiler; lexer, parser and interpreter are hand-written). Positions
are **UTF-8 byte offsets**.

```
program := decl*
decl    := "let" NAME "=" expr ";"
expr    := primary ("+" primary)*
primary := INT | STRING | NAME | "(" expr ")"
```

## Recovery design

- **Sync points**: after a failed declaration, the repair window ends at the
  next top-level `;` (consumed) or the next `let` (not consumed). A following
  valid declaration is never inside the window, so error ranges never span it.
- **Minimal repairs** (`declang/repair.py`): BFS over edit sequences of cost 1
  edits — insert a virtual token, delete a token, or replace a token kind.
  The first BFS layer that parses a complete declaration *proves* minimum
  cost; every parseable state of that layer is returned as an equal-cost
  candidate. `truncated=True` marks candidate caps or search-state caps;
  budget exhaustion falls back to panic sync and is labelled
  `"not a minimal repair"` — greedy sync is never called minimal.
- **Token identity**: recovered trees reuse the original `Token` objects;
  inserted/replaced tokens are virtual (`virtual=True`, zero-width insertion
  or `replaces=<original>` for replacements).
- **Diagnostics update**: `Document.edit(start_byte, end_byte, text)` re-runs
  lex → recover-parse → execute and refreshes diagnostics.
- **Cascade control**: one diagnostic per failed declaration plus grouped
  lexer runs; an unterminated string is a single error ending at the newline.

## Run

```
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```
