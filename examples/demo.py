"""End-to-end demo: diff -> reversible patch -> apply/inverse -> compose ->
three-way replay -> baseline verification (incl. a forged digest collision)."""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import graphdiff.hashing as hashing
from graphdiff import (
    Graph, Interval, diff, compose, replay, RenameNode, Patch,
    BaseMismatchError, ContentMismatchError,
)


def show(title):
    print(f"\n=== {title} " + "=" * max(0, 60 - len(title)))


# ---------------------------------------------------------------- base graph
base = Graph()
base.add_vertex("api", {"lang": "py"}, Interval(0, 100))
base.add_vertex("db", {"lang": "sql"}, Interval(0, 100))
base.add_vertex("cache")                      # isolated vertex
base.add_edge("e1", "api", "db", {"qps": 10})
base.add_edge("e2", "api", "db")              # parallel edge (multiplicity 2)

# ------------------------------------------------------------- target graph
target = base.copy()
RenameNode("api", "gateway").apply(target)    # rename, edges unchanged
target.vertices["db"].attrs["lang"] = "rust"  # attribute change
target.add_vertex("worker", {"lang": "go"})
target.add_edge("e3", "gateway", "worker")
del target.edges["e2"]                        # one parallel edge removed

show("1. diff(base, target) with explicit rename map {'api': 'gateway'}")
patch = diff(base, target, {"api": "gateway"})
for op in patch.ops:
    print("  ", op)
print("note: no DelEdge/AddEdge for e1 -- the rename does not re-create edges")

show("2. apply / inverse")
out = patch.apply(base)
print("  apply(patch, base) == target:", out == target)
print("  apply(inverse, target) == base:", patch.inverse().apply(target) == base)
print("  base unchanged after apply:", out is not base)

show("3. reversible patch as JSON (excerpt)")
data = json.loads(patch.to_json())
print("  base_digest: ", data["base_digest"][:16], "...")
print("  target_digest:", data["target_digest"][:16], "...")
print("  ops serialized:", len(data["ops"]))
restored = Patch.from_json(patch.to_json())
print("  json round-trip apply == target:", restored.apply(base) == target)

show("4. wrong baseline is rejected and left unchanged")
wrong = base.copy()
wrong.vertices["db"].attrs["lang"] = "java"
try:
    patch.apply(wrong)
except BaseMismatchError as e:
    print("  rejected:", e)
print("  wrong baseline still has lang=java:", wrong.vertices["db"].attrs["lang"])

show("5. forged digest collision is caught by content re-check")
real = hashing.digest
hashing.digest = lambda content: "0" * 64       # weak digest: everything collides
try:
    forged = diff(base, target, {"api": "gateway"})
    try:
        forged.apply(wrong)                      # digest "matches"!
    except ContentMismatchError as e:
        print("  rejected despite matching digest:", e)
    print("  true baseline still accepted:", forged.apply(base) == target)
finally:
    hashing.digest = real

show("6. compose(base->mid->target) == direct patch")
mid = base.copy()
RenameNode("api", "gateway").apply(mid)
p1 = diff(base, mid, {"api": "gateway"})
p2 = diff(mid, target)
pc = compose(p1, p2)
print("  composed ops:", len(pc.ops))
for op in pc.ops:
    print("  ", op)
print("  apply(composed, base) == target:", pc.apply(base) == target)
print("  apply(composed^-1, target) == base:", pc.inverse().apply(target) == base)

show("7. three-way replay onto a moved baseline")
other = base.copy()
RenameNode("db", "store").apply(other)        # other branch renamed db
other.vertices["cache"].attrs["ttl"] = 60     # ... and touched cache
res = replay(patch, other, {"db": "store"})
print("  ok:", res.ok)
for r in res.relocations:
    print("  relocated:", r.op, "-", r.detail)
print("  edge e1 endpoints after replay:",
      (res.graph.edges["e1"].src, res.graph.edges["e1"].dst))
print("  other's changes kept:",
      res.graph.vertices["cache"].attrs, "store" in res.graph.vertices)

show("8. structured conflicts")
other2 = base.copy()
del other2.edges["e1"]
del other2.vertices["api"]                    # other deleted what patch renames
res2 = replay(patch, other2)
for c in res2.conflicts:
    print(f"  conflict: kind={c.kind} entity={c.entity} {c.entity_id!r}")
    print(f"            ours={c.ours} theirs={c.theirs}")

other3 = base.copy()
other3.add_vertex("worker", {"lang": "rs"})   # same name, different identity
res3 = replay(patch, other3)
for c in res3.conflicts:
    print(f"  conflict: kind={c.kind} entity={c.entity} {c.entity_id!r}")

show("9. half-open intervals [start, end)")
g = Graph()
g.add_vertex("v", interval=Interval(10, 20))
print("  at t=10:", sorted(g.at(10).vertices), "(left closed)")
print("  at t=20:", sorted(g.at(20).vertices), "(right open)")
