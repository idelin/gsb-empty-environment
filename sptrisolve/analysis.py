"""Dependency analysis and level scheduling for triangular solves.

The substitution order is derived purely from the dependency graph of the
matrix structure, never from the storage order of the entries.  Nodes whose
dependencies are all satisfied form one *level*; every node inside a level is
independent of the others and could be evaluated in parallel.
"""

from __future__ import annotations


class SolveAnalysis:
    """Compiled dependency schedule for one orientation of one matrix.

    Attributes
    ----------
    levels : list[list[int]]
        Nodes grouped by dependency level, in evaluation order.
    deps : list[set[int]]
        ``deps[i]`` is the set of unknowns that ``x[i]`` depends on
        (dependency evidence).
    terms : list[list[tuple[int, tuple[int, int]]]]
        ``terms[i]`` lists ``(dependency, (row, col))`` coefficient keys used
        to gather current values from the matrix at solve time.
    last_recomputed : list[int]
        Nodes re-analysed by the most recent (partial) invalidation.
    """

    __slots__ = ("n", "transpose", "deps", "dependents", "terms",
                 "level_of", "levels", "last_recomputed")

    def __init__(self, n, transpose, deps, terms):
        self.n = n
        self.transpose = transpose
        self.deps = deps
        self.terms = terms
        self.dependents = [set() for _ in range(n)]
        for node in range(n):
            for dep in deps[node]:
                self.dependents[dep].add(node)
        self.level_of = [0] * n
        self.levels = []
        self.last_recomputed = []
        self._recompute_levels(set(range(n)))

    def _recompute_levels(self, affected):
        """Recompute levels for ``affected`` nodes only (iterative Kahn).

        Dependencies outside ``affected`` keep their previously computed
        levels, so a structural change only re-analyses the nodes it can
        actually influence.
        """
        indegree = {}
        for node in affected:
            indegree[node] = sum(1 for d in self.deps[node] if d in affected)
        ready = [node for node in affected if indegree[node] == 0]
        while ready:
            node = ready.pop()
            best = 0
            for dep in self.deps[node]:
                candidate = self.level_of[dep] + 1
                if candidate > best:
                    best = candidate
            self.level_of[node] = best
            for nxt in self.dependents[node]:
                if nxt in affected:
                    indegree[nxt] -= 1
                    if indegree[nxt] == 0:
                        ready.append(nxt)
        buckets = {}
        for node in range(self.n):
            buckets.setdefault(self.level_of[node], []).append(node)
        self.levels = [sorted(buckets[k]) for k in sorted(buckets)]
        self.last_recomputed = sorted(affected)

    def apply_edge_change(self, dependent_node, dep_node, key, added):
        """Incrementally absorb one structural edge change.

        Only the changed node and its transitive dependents are re-analysed;
        every other node keeps its previously computed level.  Returns the
        number of nodes that were re-analysed.
        """
        if added:
            self.deps[dependent_node].add(dep_node)
            self.dependents[dep_node].add(dependent_node)
            self.terms[dependent_node].append((dep_node, key))
        else:
            self.deps[dependent_node].discard(dep_node)
            self.dependents[dep_node].discard(dependent_node)
            self.terms[dependent_node] = [
                (d, k) for d, k in self.terms[dependent_node] if k != key
            ]
        affected = set()
        stack = [dependent_node]
        while stack:
            node = stack.pop()
            if node in affected:
                continue
            affected.add(node)
            stack.extend(self.dependents[node])
        self._recompute_levels(affected)
        return len(affected)

    def dependency_evidence(self):
        """Map each node to the sorted list of unknowns it depends on."""
        return {node: sorted(self.deps[node]) for node in range(self.n)}

    def parallel_widths(self):
        """Number of independently computable nodes per level."""
        return [len(level) for level in self.levels]
