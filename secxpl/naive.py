"""INSECURE naive renderer, kept as the counterexample.

Strategy: run the derivation on the FULL world, then "delete" the
restricted fields from the output.  The deletion is only skin-deep:

* ``steps_total`` counts steps over the whole (unprojected) tree,
* ``depth`` is measured over the whole tree, so hidden chain length
  leaks through it,
* ``deleted`` reports how many nodes were hidden,
* the debug trace is captured from the full-world run.

Two low-equivalent worlds therefore produce different public outputs,
which the noninterference tester in ``secxpl.noninterference`` detects.
Do not use this module for anything except demonstrating the leak.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

from .engine import derive
from .model import World


@dataclass
class NaiveOutput:
    query: str
    status: str  # 'ok' | 'error'
    tree_lines: tuple[str, ...]
    counts: dict
    error: str | None
    debug: tuple[str, ...]

    def transcript(self) -> str:
        lines = [f"query: {self.query}", f"status: {self.status}"]
        if self.error is not None:
            lines.append(f"error: {self.error}")
        if self.tree_lines:
            lines.append("tree:")
            lines.extend(self.tree_lines)
        lines.append("counts: " + json.dumps(self.counts, sort_keys=True))
        lines.append("debug:")
        lines.extend(f"  {line}" for line in self.debug)
        return "\n".join(lines)


def naive_explain(world: World, clearance: int, query: str) -> NaiveOutput:
    debug: list[str] = []
    closure = derive(world.facts, world.rules, debug=debug)  # FULL world!
    if query not in closure:
        return NaiveOutput(
            query=query,
            status="error",
            tree_lines=(),
            counts={},
            error=f"ERROR[not-found]: '{query}'",
            debug=tuple(debug),
        )
    lines: list[str] = []
    counts = {"steps_total": 0, "deleted": 0, "depth": 0}

    def visit(node, depth: int) -> None:
        counts["depth"] = max(counts["depth"], depth)
        hidden = node.fact.level > clearance
        pad = "  " * depth + "- "
        if hidden:
            counts["deleted"] += 1
            lines.append(pad + "<deleted>")
        elif node.step is None:
            lines.append(pad + f"fact {node.fact.fact_id}@{node.fact.version}")
        else:
            lines.append(pad + f"step {node.step.step_id} -> {node.step.conclusion}")
        if node.step is not None:
            counts["steps_total"] += 1  # counts hidden steps too: LEAK
            for premise in node.premises:
                visit(premise, depth + 1)

    visit(closure[query], 0)
    return NaiveOutput(
        query=query,
        status="ok",
        tree_lines=tuple(lines),
        counts=counts,
        error=None,
        debug=tuple(debug),
    )
