"""Standalone full-model consistency verification.

``verify`` recomputes every derived structure from the base rows and
reports any divergence.  It is deliberately independent of the mutation
code paths, so it can audit the live database or any frozen snapshot and
confirm that -- after swaps, key cycles, or rolled-back transactions --
indexes and references agree in every readable state.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Dict, List, Set, Tuple

if TYPE_CHECKING:
    from .core import Database


def verify(db: "Database") -> List[str]:
    """Return a list of consistency problems; empty means consistent."""
    problems: List[str] = []
    expected_refs: Dict[Tuple[str, int], Set[Tuple[str, int, str]]] = {}

    for table_name, t in db.tables.items():
        pk_col = t.primary_key

        # 1. Index and rows must be a bijection on stable entity ids.
        for pk, entity_id in t.index.items():
            row = t.rows.get(entity_id)
            if row is None:
                problems.append(
                    f"{table_name}: index key {pk!r} points at missing "
                    f"entity {entity_id}"
                )
            elif row.get(pk_col) != pk:
                problems.append(
                    f"{table_name}: index key {pk!r} but row {entity_id} "
                    f"has {pk_col}={row.get(pk_col)!r}"
                )
        for entity_id, row in t.rows.items():
            pk = row.get(pk_col)
            if t.index.get(pk) != entity_id:
                problems.append(
                    f"{table_name}: row {entity_id} with {pk_col}={pk!r} "
                    f"is not reachable through the index"
                )

        # 2. Stable identity: retired ids must never come back.
        reused = t.retired_ids.intersection(t.rows)
        if reused:
            problems.append(
                f"{table_name}: retired entity ids reused: {sorted(reused)}"
            )
        if t.rows and t._next_entity_id <= max(t.rows):
            problems.append(
                f"{table_name}: entity id allocator regressed below live ids"
            )

        # 3. Foreign keys must resolve; rebuild expected reverse refs.
        for entity_id, row in t.rows.items():
            for column, fk in t.foreign_keys.items():
                value = row.get(column)
                if value is None:
                    continue
                target = db.tables[fk.ref_table]
                target_id = target.index.get(value)
                if target_id is None:
                    problems.append(
                        f"{table_name}.{column}={value!r} (entity {entity_id}) "
                        f"dangles: no {fk.ref_table}.{fk.ref_column} match"
                    )
                    continue
                expected_refs.setdefault((fk.ref_table, target_id), set()).add(
                    (table_name, entity_id, column)
                )

    # 4. Stored reverse references must match the recomputed ones exactly.
    for key in set(expected_refs) | set(db.reverse_refs):
        expected = expected_refs.get(key, set())
        stored = db.reverse_refs.get(key, set())
        if expected != stored:
            problems.append(
                f"reverse refs for {key}: stored {sorted(stored)} != "
                f"expected {sorted(expected)}"
            )

    # 5. Commit ordering: no entity may carry a seq beyond the database.
    for table_name, t in db.tables.items():
        for entity_id, seq in t.commit_seqs.items():
            if seq > db.commit_seq:
                problems.append(
                    f"{table_name}: entity {entity_id} has commit seq {seq} "
                    f"ahead of database seq {db.commit_seq}"
                )

    return problems
