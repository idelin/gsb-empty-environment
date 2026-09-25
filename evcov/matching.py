"""Joint source-to-category assignment.

Satisfaction is NOT per-category counting: a source may back at most one
category at a time (cross-category exclusivity), and several items from the
same source collapse into a single independent source. This is a bipartite
b-matching solved with Kuhn's augmenting-path algorithm.
"""

from __future__ import annotations

from .model import EvidenceItem, Rule


def active_items(
    items: list[EvidenceItem], start: int, end: int
) -> list[EvidenceItem]:
    """Items that are unretracted and valid across the whole [start, end)."""
    return [
        it
        for it in items
        if not it.retracted and it.valid_from <= start and it.valid_until >= end
    ]


def support_map(items: list[EvidenceItem]) -> dict[str, set[str]]:
    """source_id -> categories it can back. Duplicate same-source items merge."""
    support: dict[str, set[str]] = {}
    for it in items:
        support.setdefault(it.source_id, set()).update(it.categories)
    return support


def is_satisfied(rule: Rule, items: list[EvidenceItem]) -> bool:
    """True iff all category requirements are jointly satisfiable.

    Each requirement contributes `min_sources` slots; every slot must be
    matched to a distinct source that supports the slot's category.
    """
    support = support_map(items)
    slots: list[str] = []
    for req in rule.requirements:
        slots.extend([req.category] * req.min_sources)

    match_of: dict[str, int] = {}  # source_id -> slot index

    def try_kuhn(slot: int, seen: set[str]) -> bool:
        category = slots[slot]
        for source_id in sorted(support):
            if category not in support[source_id] or source_id in seen:
                continue
            seen.add(source_id)
            if source_id not in match_of or try_kuhn(match_of[source_id], seen):
                match_of[source_id] = slot
                return True
        return False

    return all(try_kuhn(slot, set()) for slot in range(len(slots)))
