#!/usr/bin/env python3
"""Counterexample: inserted tokens must not impersonate original tokens.

Repairing "id = num" inserts a ';'.  The inserted token must have a fresh
identity (uid), an INSERTED origin and a zero-width span at the insertion
point, while every original token keeps its identity and source span after
recovery and re-parsing.
"""
import _bootstrap  # noqa: F401

from parserlib import Origin, PredictiveParser, RepairEngine
from parserlib import demolang


def main() -> None:
    grammar = demolang.program_grammar()
    original = demolang.tokens_from_kinds("id = num")
    original_uids = {t.uid for t in original}
    forest = RepairEngine(grammar, budget=1).repair(original)
    assert forest.min_cost == 1, forest.min_cost
    candidate = forest.candidates[0]

    inserted = [t for t in candidate.repaired if t.origin is Origin.INSERTED]
    assert len(inserted) == 1, "expected exactly one inserted token"
    assert inserted[0].uid not in original_uids, "inserted token reuses an identity"
    assert inserted[0].span == (3, 3), f"inserted token must map to a point: {inserted[0].span}"

    kept = [t for t in candidate.repaired if t.origin is Origin.ORIGINAL]
    assert {t.uid for t in kept} == original_uids, "original identities changed"

    result = PredictiveParser(grammar, recover=False).parse(list(candidate.repaired))
    assert result.ok
    mapping = result.root.source_map()
    assert (";", (3, 3), "inserted") in mapping, mapping
    assert ("num", (2, 3), "original") in mapping, mapping
    print("PASS ce_04: inserted vs original identities distinct, source map preserved")


if __name__ == "__main__":
    main()
