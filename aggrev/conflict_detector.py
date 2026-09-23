"""Conflict detection, kept separate from gap waiting.

Currently detects dependency cycles among parked corrections: correction X
waits for the version produced by correction Y while Y (transitively) waits
for X's version. Such revisions can never become applicable and are rejected.
"""
from __future__ import annotations

from .models import Correction, version_id_for


class ConflictDetector:
    def find_cycles(self, waiting: dict[str, Correction]) -> list[list[str]]:
        """Return the cyclic strongly-connected components among parked corrections."""
        graph: dict[str, list[str]] = {rid: [] for rid in waiting}
        for rid, correction in waiting.items():
            for parent in correction.parents:
                for other_id, other in waiting.items():
                    if parent == other.version_id and other_id != rid:
                        graph[rid].append(other_id)
                # self-dependency: waits for its own version
                if parent == version_id_for(rid):
                    graph[rid].append(rid)

        index: dict[str, int] = {}
        lowlink: dict[str, int] = {}
        on_stack: set[str] = set()
        stack: list[str] = []
        cycles: list[list[str]] = []
        counter = [0]

        def strongconnect(node: str) -> None:
            index[node] = lowlink[node] = counter[0]
            counter[0] += 1
            stack.append(node)
            on_stack.add(node)
            for nxt in graph[node]:
                if nxt not in index:
                    strongconnect(nxt)
                    lowlink[node] = min(lowlink[node], lowlink[nxt])
                elif nxt in on_stack:
                    lowlink[node] = min(lowlink[node], index[nxt])
            if lowlink[node] == index[node]:
                scc = []
                while True:
                    member = stack.pop()
                    on_stack.discard(member)
                    scc.append(member)
                    if member == node:
                        break
                if len(scc) > 1 or scc[0] in graph[scc[0]]:
                    cycles.append(sorted(scc))

        for rid in graph:
            if rid not in index:
                strongconnect(rid)
        return cycles
