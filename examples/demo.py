#!/usr/bin/env python3
"""End-to-end demo: lex, parse, recover, repair, and re-parse incrementally."""
import os
import sys

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from parserlib import IncrementalParser, Origin, PredictiveParser, RepairEngine, Token
from parserlib import demolang
from parserlib.tokens import EOF_KIND


def banner(title):
    print()
    print("=" * 68)
    print(title)
    print("=" * 68)


def render_source(tokens):
    parts = []
    for tok in tokens:
        if tok.kind == EOF_KIND:
            continue
        if tok.origin is Origin.INSERTED:
            parts.append(f"<<{tok.text}>>")
        elif tok.origin is Origin.SUBSTITUTED:
            parts.append(f"[{tok.text}]")
        else:
            parts.append(tok.text)
    return " ".join(parts)


def main():
    grammar = demolang.program_grammar()
    parser = PredictiveParser(grammar)

    banner("1. Hand-rolled automaton lexer (explicit alphabet + transitions)")
    source = "x = 41 + 1 ;\nprint x ;\n"
    tokens = demolang.lex(source)
    print(f"source: {source!r}")
    print("tokens:", " ".join(f"{t.kind}@{t.start}..{t.end}" for t in tokens))

    banner("2. Conflict-free predictive table parse -> tree")
    result = parser.parse(tokens)
    print(f"ok={result.ok}")
    print(result.root.pretty())

    banner("3. FOLLOW-synchronised recovery (error nodes keep skipped tokens)")
    broken = demolang.tokens_from_kinds("id = num ; print + id ; id = num ;")
    recovered = parser.parse(broken)
    for diag in recovered.diagnostics:
        print(f"  [{diag.kind}] token {diag.position}: {diag.message}")
    print(recovered.root.pretty())
    print(f"statements parsed: {len(recovered.root.find_all('statement'))} of 3")

    banner("4. Budgeted minimum-cost repair -> shared candidate forest")
    damaged = demolang.tokens_from_kinds("id = ;")
    forest = RepairEngine(grammar, budget=2).repair(damaged)
    print(f"input kinds : id = ;")
    print(f"min cost    : {forest.min_cost} (budget 2)")
    print(f"forest      : {forest.node_count} shared nodes, {forest.edge_count} edges")
    print(f"candidates  : {len(forest.candidates)} (all min-cost repairs, none 'the' unique one)")
    for candidate in forest.candidates:
        edits = "; ".join(e.describe() for e in candidate.edits)
        print(f"  cost {candidate.cost}: {edits}")
        print(f"    repaired source: {render_source(candidate.repaired)}")
        mapping = ", ".join(
            f"{sym}@{span}[{origin}]"
            for sym, span, origin in PredictiveParser(grammar, recover=False)
            .parse(list(candidate.repaired))
            .root.source_map()
        )
        print(f"    source map: {mapping}")

    banner("5. Incremental edit: only the affected block is invalidated")
    inc = IncrementalParser(demolang.statement_grammar())
    kinds = "id = num ; print num ; id = id ; print id ;"
    report = inc.load(demolang.tokens_from_kinds(kinds))
    print(f"loaded {len(inc.blocks)} blocks, invalidated={report.invalidated}")
    report = inc.replace(2, Token(kind="num", text="7", start=2, end=3))
    print(f"edit token 2 (num -> 7): invalidated={report.invalidated} reused={report.reused}")
    report = inc.replace(5, Token(kind="+", text="+", start=5, end=6))
    print(f"edit token 5 (num -> +): invalidated={report.invalidated} reused={report.reused}")
    for block in inc.blocks:
        status = "ok" if not block.diagnostics else f"{len(block.diagnostics)} error(s)"
        print(f"  block {block.index}: tokens {block.start}..{block.end - 1}: {status}")
    print("later blocks stay legal even though block 1 is broken")


if __name__ == "__main__":
    main()
