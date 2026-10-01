"""Incremental partition repair after local transition modifications.

The repair can both *split* blocks that became inhomogeneous and *merge*
blocks that became equivalent.  The number of state pairs touched during a
repair is reported so the incremental cost can be compared against a full
re-minimization.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .core import (
    DFA,
    canonical_form,
    complete,
    quotient_with_map,
    refine_partition,
    trim,
)
from .verify import states_equivalent


@dataclass
class RepairReport:
    modified: list[tuple[str, str, str]]          # applied (state, symbol, target)
    removed_unreachable: tuple[str, ...]          # states dropped by re-trimming
    splits: list[tuple[tuple[str, ...], ...]]     # each split: its new pieces
    merges: list[tuple[tuple[str, ...], ...]]     # each merge: the fused old blocks
    touched_pairs: int                            # state pairs examined
    partition: tuple[frozenset[str], ...]         # resulting partition


class IncrementalMinimizer:
    """Maintains the Nerode partition of a DFA under local transition edits."""

    def __init__(self, dfa: DFA):
        self.dfa = trim(complete(dfa))
        self.partition: list[frozenset[str]] = refine_partition(self.dfa)
        self._reindex()

    # -- inspection ------------------------------------------------------

    def _reindex(self) -> None:
        self.block_of: dict[str, int] = {}
        for i, b in enumerate(self.partition):
            for s in b:
                self.block_of[s] = i

    def quotient(self) -> DFA:
        """Canonical (renaming-invariant) quotient of the current partition."""
        quot, _ = quotient_with_map(self.dfa, self.partition)
        canon, _ = canonical_form(quot)
        return canon

    def mapping(self) -> dict[str, str]:
        quot, block_of = quotient_with_map(self.dfa, self.partition)
        canon, rename = canonical_form(quot)
        return {s: rename[str(block_of[s])] for s in sorted(self.dfa.states)}

    # -- modification ----------------------------------------------------

    def modify(self, mods) -> RepairReport:
        """Apply ``(state, symbol, new_target)`` edits and repair the partition."""
        mods = list(mods)
        trans = {s: dict(row) for s, row in self.dfa.transitions.items()}
        dirty: set[str] = set()
        for s, a, t in mods:
            if s not in self.dfa.states:
                raise ValueError(f"unknown state {s!r}")
            if a not in self.dfa.alphabet:
                raise ValueError(f"unknown symbol {a!r}")
            if t not in self.dfa.states:
                raise ValueError(f"unknown target state {t!r}")
            if trans[s][a] != t:
                trans[s][a] = t
                dirty.add(s)
        new_dfa = DFA(self.dfa.alphabet, self.dfa.states, trans,
                      self.dfa.start, self.dfa.accepting)
        trimmed = trim(new_dfa)
        removed = tuple(sorted(new_dfa.states - trimmed.states))
        self.dfa = trimmed
        if removed:
            gone = set(removed)
            self.partition = [b - gone for b in self.partition]
            self.partition = [b for b in self.partition if b]
            self._reindex()
        dirty &= self.dfa.states
        touched = 0
        splits: list = []
        merges: list = []
        if dirty:
            cost, fresh = self._split_phase(dirty, splits)
            touched += cost
            touched += self._merge_phase(dirty, fresh, merges)
        self._reindex()
        return RepairReport(mods, removed, splits, merges, touched,
                            tuple(self.partition))

    # -- split phase -----------------------------------------------------

    def _predecessors(self) -> dict[str, set[str]]:
        preds = {s: set() for s in self.dfa.states}
        for s in self.dfa.states:
            for a in self.dfa.alphabet:
                preds[self.dfa.transitions[s][a]].add(s)
        return preds

    def _split_phase(self, dirty: set[str], splits: list):
        """Worklist refinement seeded from blocks containing dirty states.

        Returns ``(touched_pairs, new_block_indices)``.
        """
        touched = 0
        preds = self._predecessors()
        self._reindex()
        fresh: set[int] = set()
        queue = deque(sorted({self.block_of[s] for s in dirty}))
        in_queue = set(queue)
        while queue:
            bi = queue.popleft()
            in_queue.discard(bi)
            block = self.partition[bi]
            if len(block) < 2:
                continue
            touched += len(block) * (len(block) - 1) // 2
            groups: dict[tuple[int, ...], list[str]] = {}
            for s in sorted(block):
                sig = tuple(
                    self.block_of[self.dfa.transitions[s][a]]
                    for a in self.dfa.alphabet
                )
                groups.setdefault(sig, []).append(s)
            if len(groups) == 1:
                continue
            pieces = sorted(groups.values(), key=lambda g: min(g))
            self.partition[bi] = frozenset(pieces[0])
            new_indices = [bi]
            for piece in pieces[1:]:
                self.partition.append(frozenset(piece))
                new_indices.append(len(self.partition) - 1)
            fresh.update(new_indices)
            self._reindex()
            splits.append(tuple(tuple(sorted(p)) for p in pieces))
            affected = set()
            for s in block:
                affected |= preds[s]
            for s in affected:
                bj = self.block_of[s]
                if bj not in in_queue:
                    in_queue.add(bj)
                    queue.append(bj)
        return touched, fresh

    # -- merge phase -----------------------------------------------------

    def _merge_phase(self, dirty: set[str], fresh: set[int], merges: list) -> int:
        """Merge blocks whose representatives became equivalent.

        Only pairs involving an *affected* block are tested: a block is
        affected if one of its states can reach a modified transition, or if
        the block was created during the split phase.  Unchanged pairs keep
        their previous (in)equivalence, so skipping them is sound.
        """
        touched = 0
        preds = self._predecessors()
        affected_states = set(dirty)
        queue = deque(dirty)
        while queue:
            x = queue.popleft()
            for p in preds[x]:
                if p not in affected_states:
                    affected_states.add(p)
                    queue.append(p)
        affected_blocks = {self.block_of[s] for s in affected_states} | set(fresh)

        n = len(self.partition)
        parent = list(range(n))

        def find(x: int) -> int:
            while parent[x] != x:
                parent[x] = parent[parent[x]]
                x = parent[x]
            return x

        reps = [min(b) for b in self.partition]
        acc_of = [bool(b & self.dfa.accepting) for b in self.partition]
        for i in range(n):
            for j in range(i + 1, n):
                if i not in affected_blocks and j not in affected_blocks:
                    continue
                if acc_of[i] != acc_of[j]:
                    continue
                equiv, _w, pairs = states_equivalent(self.dfa, reps[i], reps[j])
                touched += pairs
                if equiv:
                    ri, rj = find(i), find(j)
                    if ri != rj:
                        parent[rj] = ri

        groups: dict[int, list[int]] = {}
        for i in range(n):
            groups.setdefault(find(i), []).append(i)
        new_partition = []
        for idxs in sorted(groups.values(),
                           key=lambda g: min(min(self.partition[i]) for i in g)):
            if len(idxs) > 1:
                merges.append(tuple(tuple(sorted(self.partition[i])) for i in idxs))
            new_partition.append(
                frozenset().union(*(self.partition[i] for i in idxs))
            )
        self.partition = new_partition
        self._reindex()
        return touched
