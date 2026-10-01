"""Immutable compile snapshots with independent self-verification."""
from __future__ import annotations

from dataclasses import dataclass

from .reference import compute_sets
from .table import build_table


@dataclass(frozen=True)
class Snapshot:
    revision: int
    start: object
    productions: tuple            # all registered Production objects
    enabled_conditions: frozenset
    nullable: frozenset
    first: tuple                  # sorted (Sym, frozenset) pairs
    follow: tuple                 # sorted (Sym, frozenset) pairs
    table: tuple                  # sorted (Sym, Sym, tuple-of-pids) rows
    conflicts: tuple              # ConflictReport objects

    def active_productions(self):
        return [p for p in self.productions
                if p.condition is None or p.condition in self.enabled_conditions]

    def table_dict(self):
        return {(lhs, tok): pids for lhs, tok, pids in self.table}

    def production_map(self):
        return {p.pid: p for p in self.productions}

    def verify(self):
        """Recompute everything from the stored productions and compare."""
        active = self.active_productions()
        nullable, first, follow = compute_sets(active, self.start)
        if frozenset(nullable) != self.nullable:
            raise ValueError("snapshot nullable set does not verify")
        if tuple(sorted((k, frozenset(v)) for k, v in first.items())) != self.first:
            raise ValueError("snapshot FIRST sets do not verify")
        if tuple(sorted((k, frozenset(v)) for k, v in follow.items())) != self.follow:
            raise ValueError("snapshot FOLLOW sets do not verify")
        table, conflicts = build_table(
            set(nullable), first, follow, active, self.start)
        rows = tuple(sorted((k[0], k[1], v) for k, v in table.items()))
        if rows != self.table:
            raise ValueError("snapshot predict table does not verify")
        if conflicts != self.conflicts:
            raise ValueError("snapshot conflict reports do not verify")
        return True
