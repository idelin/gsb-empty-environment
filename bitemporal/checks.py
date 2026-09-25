"""Independent invariant checker.

This module never trusts the rectangle index.  It replays the append-only
record log version by version, recomputes the canonical visible
decomposition for every observation version from scratch, and compares it
against what the index actually serves.  It verifies:

* coverage   -- index and log replay agree on every (key, version) slice;
* boundaries -- fragments are half-open, non-empty and non-overlapping;
* history invariance -- every rectangle is visible for its whole version
  interval, i.e. a closed rectangle is never rewritten by later corrections.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .decompose import decompose
from .model import LiveRecord, from_key, to_key


@dataclass
class CheckReport:
    versions_checked: int = 0
    keys_checked: int = 0
    fragments_checked: int = 0
    problems: list[str] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.problems

    def __str__(self) -> str:
        if self.ok:
            return (f"OK: {self.keys_checked} keys, {self.versions_checked} "
                    f"version slices, {self.fragments_checked} rectangles verified")
        return (f"FAILED with {len(self.problems)} problem(s):\n"
                + "\n".join(self.problems))


def replay_visible(store, key: str, version: int):
    """Canonical visible pieces at ``version``, derived from the log only."""
    live = []
    for rid in store.record_ids(key):
        rec = store.record(rid)
        if rec.version > version:
            continue
        retracted = store.retracted_at(rid)
        if retracted is not None and retracted <= version:
            continue
        live.append(LiveRecord(rid, rec.valid_from, rec.valid_to, rec.value,
                               store.priority_at(rid, version)))
    return decompose(live)


def _norm(valid_from, valid_to, value, priority, record_id):
    return (from_key(valid_from), to_key(valid_to), repr(value), priority, record_id)


def check_store(store) -> CheckReport:
    report = CheckReport()
    for key in store.keys():
        report.keys_checked += 1
        per_version: dict[int, set] = {}
        for v in range(1, store.version + 1):
            report.versions_checked += 1
            expected = sorted(_norm(vf, vt, r.value, r.priority, r.record_id)
                              for vf, vt, r in replay_visible(store, key, v))
            actual_frags = store.range_query(key, None, None, v, v + 1)
            actual = sorted(_norm(f.valid_from, f.valid_to, f.value,
                                  f.priority, f.record_id) for f in actual_frags)
            if actual != expected:
                report.problems.append(
                    f"key={key!r} version={v}: index serves {actual}, "
                    f"log replay expects {expected}")
            ordered = sorted(actual_frags, key=lambda f: from_key(f.valid_from))
            for frag in ordered:
                if not from_key(frag.valid_from) < to_key(frag.valid_to):
                    report.problems.append(
                        f"key={key!r} version={v}: empty/inverted rectangle {frag}")
            for a, b in zip(ordered, ordered[1:]):
                if to_key(a.valid_to) > from_key(b.valid_from):
                    report.problems.append(
                        f"key={key!r} version={v}: overlapping rectangles "
                        f"{a.identity()} and {b.identity()} (boundary double-hit)")
            per_version[v] = set(actual)
        for frag in store.fragments(key):
            report.fragments_checked += 1
            end = frag.ver_to if frag.ver_to is not None else store.version + 1
            norm = _norm(frag.valid_from, frag.valid_to, frag.value,
                         frag.priority, frag.record_id)
            for v in range(frag.ver_from, end):
                if norm not in per_version.get(v, ()):
                    report.problems.append(
                        f"key={key!r}: rectangle {norm} missing at version {v} "
                        f"of its own interval [{frag.ver_from}, {end}) -- "
                        f"history was rewritten")
    return report
