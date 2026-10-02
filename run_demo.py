#!/usr/bin/env python3
"""End-to-end demo: lex, parse with recovery, minimal repairs, incremental
update.  Run: python3 run_demo.py"""
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))

from errparse.demo_grammar import lex, make_parser
from errparse.incremental import IncrementalParser
from errparse.repair import minimal_repairs


def section(title):
    print("\n" + "=" * 64 + f"\n{title}\n" + "=" * 64)


parser = make_parser()

section("1. Clean parse of `x = 1 + 2 ;`")
res = parser.parse(lex("x = 1 + 2 ;"))
print("ok:", res.ok)
print(res.tree.pretty())

section("2. Recovery: `print ( x ;` / `if y = 3 ;` / `z = 4 ;`")
tokens = lex("print ( x ;\nif y = 3 ;\nz = 4 ;")
res = parser.parse(tokens)
for e in res.errors:
    print(f"  pos {e.pos}: {e.detail}")
print(res.tree.pretty())
leaves = [leaf.token for leaf in res.tree.iter_tokens()]
print("source map (type, uid, origin, pos):")
for t in leaves:
    print(f"  {t.type:8} {t.uid:8} {t.origin:12} @{t.pos}")

section("3. Minimal repairs of `y = x + ;` (shared forest)")
tokens = lex("y = x + ;")
forest = minimal_repairs(tokens, parser, budget=3)
print("minimal cost:", forest.cost, "| candidates:", forest.count())
for script in forest.enumerate(limit=10):
    print("  repair:", "; ".join(e.describe() for e in script))
repaired = forest.apply(forest.enumerate(limit=1)[0])
print("repaired stream:", [(t.type, t.origin) for t in repaired])
print("repaired stream re-parses cleanly:", parser.parse(repaired).ok)

section("4. Incremental update of `a = 1 ; b = 2 ; c = 3 ; d = 4 ;`")
inc = IncrementalParser(parser, "stmt")
r1 = inc.parse(lex("a = 1 ; b = 2 ; c = 3 ; d = 4 ;"))
print(f"initial: {len(r1.blocks)} blocks, ok={r1.ok}")
r2 = inc.update(lex("a = 1 ; b = 99 ; c = 3 ; d = 4 ;"))
print(f"edit value in stmt 2: reused={r2.reused} reparsed={r2.reparsed}")
print("stmt 1 tree reused by identity:", r2.blocks[0].tree is r1.blocks[0].tree)
r3 = inc.update(lex("a = 1 ; b = ; c = 3 ; d = 4 ;"))
print(f"break stmt 2 (`b = ;`): reused={r3.reused} reparsed={r3.reparsed} "
      f"errors={len(r3.errors)}")
for e in r3.errors:
    print(f"  pos {e.pos}: {e.detail}")
print("stmt 3 still intact:",
      [l.token.type for l in r3.blocks[2].tree.iter_tokens()])
