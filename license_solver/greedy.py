"""A deliberately naive per-package greedy selector.

It exists to demonstrate why OR branches cannot be decided locally: it picks
the highest admissible version and the locally cheapest branch for each
package, never looks ahead and never backtracks - so locally valid picks can
paint it into a corner on the full graph.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product

from .expression import evaluate, or_nodes
from .solver import evaluate_selection


@dataclass
class GreedyResult:
    choices: dict
    unresolved: list = field(default_factory=list)
    conflicts: list = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.unresolved and not self.conflicts


def _local_obligation_count(repo, build, pv, combo):
    lics, _ = evaluate(
        pv.expr, combo, 0, build.attributes, repo.licenses, repo.exceptions
    )
    return sum(len(l.obligations) + len(l.tags) for l in lics)


def greedy_select(repo, build) -> GreedyResult:
    choices = {}
    unresolved = []
    queue = list(build.roots)
    seen = set()
    while queue:
        name = queue.pop(0)
        if name in seen:
            continue
        seen.add(name)
        if name not in repo.packages:
            unresolved.append(name)
            continue
        ranges = []
        if name in build.roots:
            ranges.append(build.roots[name])
        for _parent, (pv_parent, _c) in choices.items():
            spec = pv_parent.deps.get(name)
            if spec is not None:
                ranges.append(spec.range)
        candidates = [
            pv
            for pv in sorted(repo.packages[name], key=lambda p: p.version, reverse=True)
            if all(r.contains(pv.version) for r in ranges)
        ]
        if not candidates:
            unresolved.append(name)
            continue
        pv = candidates[0]  # highest version, zero lookahead
        n_or = len(or_nodes(pv.expr))
        combos = list(product((0, 1), repeat=n_or)) if n_or else [()]
        combo = min(combos, key=lambda c: _local_obligation_count(repo, build, pv, c))
        choices[name] = (pv, combo)
        queue.extend(pv.deps)
    conflicts = []
    if not unresolved:
        conflicts = evaluate_selection(repo, build, choices).conflicts
    return GreedyResult(choices, unresolved, conflicts)
