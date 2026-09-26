"""Independent per-document verification harness.

brute_force_field re-derives matches straight from the stored token
sequence with an explicit Cartesian-product enumeration (itertools),
completely independent of the index matcher and its block skipping.
verify_snapshot cross-checks every live document and field, and records
how many posting blocks the index matcher had to decode -- the number
that matters for very high-frequency repeated terms.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from typing import Dict, List, Sequence, Set, Tuple

from .index import Snapshot
from .postings import DecodeCounter
from .query import PhraseQuery
from .tokenizer import Token


def brute_force_field(
    tokens: Sequence[Token], query: PhraseQuery
) -> List[Tuple[int, ...]]:
    """Reference matcher: full Cartesian enumeration, then filter.

    Independent of BlockedPositions / skipping. A combination is a match
    iff positions strictly increase (no reuse) and every adjacent pair
    respects its gap bound.
    """
    candidates = [
        [i for i, tok in enumerate(tokens) if tok.term in terms]
        for terms in query.elements
    ]
    if any(not c for c in candidates):
        return []
    results: List[Tuple[int, ...]] = []
    for combo in itertools.product(*candidates):
        ok = True
        for i in range(len(combo) - 1):
            if combo[i + 1] <= combo[i]:
                ok = False
                break
            if combo[i + 1] - combo[i] - 1 > query.gaps[i]:
                ok = False
                break
        if ok:
            results.append(tuple(combo))
    return results


@dataclass
class VerifyReport:
    docs_checked: int = 0
    fields_checked: int = 0
    matches_expected: int = 0
    matches_actual: int = 0
    blocks_decoded: int = 0
    positions_scanned: int = 0
    mismatches: List[tuple] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.mismatches


def verify_snapshot(snapshot: Snapshot, query: PhraseQuery) -> VerifyReport:
    """Cross-check index results against brute force for every live doc."""
    report = VerifyReport()
    counter = DecodeCounter()
    actual: Dict[str, Dict[str, Set[Tuple[int, ...]]]] = {}
    for m in snapshot.search(query, counter=counter):
        actual.setdefault(m.doc_id, {}).setdefault(m.field, set()).add(
            m.positions
        )
    report.blocks_decoded = counter.blocks_decoded
    report.positions_scanned = counter.positions_scanned
    report.matches_actual = sum(
        len(pos) for fields in actual.values() for pos in fields.values()
    )
    for doc_id, entry in snapshot.iter_live_docs():
        report.docs_checked += 1
        for fname, tokens in entry.tokens.items():
            report.fields_checked += 1
            expected = set(map(tuple, brute_force_field(tokens, query)))
            got = actual.get(doc_id, {}).get(fname, set())
            report.matches_expected += len(expected)
            if expected != got:
                report.mismatches.append((doc_id, fname, expected, got))
    return report
