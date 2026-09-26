"""On-demand subset construction (powerset determinisation) with tau closure.

Only subsets actually reached by queried words are computed, so the
determinisation cost is paid lazily during the product search.
"""
from __future__ import annotations


class SubsetCache:
    """Caches explored subset-transition tables per model version.

    Keys are ``(version, fingerprint)``: editing a model or bumping its
    version yields a different key, so stale entries are never served after
    an update.  Entries of a superseded version can also be dropped
    explicitly via :meth:`invalidate_version`.
    """

    def __init__(self):
        self._entries = {}

    @staticmethod
    def key(machine):
        return (machine.version, machine.fingerprint())

    def get(self, machine):
        return self._entries.get(self.key(machine))

    def put(self, machine, table):
        self._entries[self.key(machine)] = table

    def invalidate_version(self, version) -> int:
        doomed = [key for key in self._entries if key[0] == version]
        for key in doomed:
            del self._entries[key]
        return len(doomed)

    def __len__(self):
        return len(self._entries)


class Determinizer:
    """On-demand powerset construction over one machine."""

    def __init__(self, machine, cache: SubsetCache | None = None):
        self.machine = machine
        self._tau_succ = {state: set() for state in machine.states}
        self._vis_succ = {state: {} for state in machine.states}
        for src, label, dst in machine.transitions:
            if label is None:
                self._tau_succ[src].add(dst)
            else:
                self._vis_succ[src].setdefault(label, set()).add(dst)
        self._closure_memo = {}
        self._table = {}  # frozenset(states) -> {action: frozenset(states)}
        self._cache = cache
        self.cache_hit = False
        if cache is not None:
            stored = cache.get(machine)
            if stored is not None:
                self.cache_hit = True
                self._table = {subset: dict(row) for subset, row in stored.items()}

    def closure(self, subset):
        """Tau-reachability fixpoint; tau cycles terminate via the seen set."""
        key = frozenset(subset)
        memo = self._closure_memo.get(key)
        if memo is not None:
            return memo
        seen = set(key)
        stack = list(key)
        while stack:
            state = stack.pop()
            for nxt in self._tau_succ.get(state, ()):
                if nxt not in seen:
                    seen.add(nxt)
                    stack.append(nxt)
        result = frozenset(seen)
        self._closure_memo[key] = result
        return result

    def initial_subset(self):
        return self.closure({self.machine.initial})

    def step(self, subset, action):
        """Visible step: move on ``action`` from every state, then tau-close."""
        subset = frozenset(subset)
        row = self._table.get(subset)
        if row is not None and action in row:
            return row[action]
        moved = set()
        for state in subset:
            moved.update(self._vis_succ.get(state, {}).get(action, ()))
        target = self.closure(moved) if moved else frozenset()
        self._table.setdefault(subset, {})[action] = target
        return target

    def flush(self):
        """Persist the explored table into the cache (if any)."""
        if self._cache is not None:
            self._cache.put(
                self.machine, {subset: dict(row) for subset, row in self._table.items()}
            )
