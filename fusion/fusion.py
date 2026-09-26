"""Core fusion: weight validation, aggregation, threshold TopK, exhaustive
reference fusion.

Aggregation (public rule)
-------------------------
For a document d and the active fields (weight > 0)::

    score(d) = sum(w_i * n_i(d) for present i) / sum(w_i for present i)

A field is *present* for d when d actually has that field.  A missing field
is EXCLUDED from both numerator and denominator -- it is never punished as a
zero score.  A present field with no query match is a genuine 0 raw score
and participates normally.

Upper bound used by the threshold algorithm
-------------------------------------------
Let t_i be the current normalized threshold of field i (the normalized raw
score of the last item pulled from sorted access; initially the normalized
stats maximum).  Sorted access is non-increasing and normalization is
monotone, so any document d not yet fully known satisfies n_i(d) <= t_i for
every field i.  Because all active weights are > 0, score(d) is a weighted
average of values each <= max_i t_i (the average runs over a subset of the
fields when some are missing, which can only keep it <= the same bound).
Hence every unseen document satisfies::

    score(d) <= B := max_i t_i

The algorithm scores every document it pulls exactly (random access on the
remaining fields) and stops as soon as the k-th best exact score is strictly
greater than B.  The comparison is strict so that the deterministic
(-score, doc_id) tie-break can never be upset by an unseen document.  If the
streams are exhausted first, the remaining documents are scored exactly and
the result coincides with the exhaustive reference.

If any assumption fails (negative weights, or a sorted stream that is not
non-increasing), the bound is no longer valid: the caller must reject the
configuration or degrade to the exhaustive reference (see engine.py).
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Mapping, Optional, Protocol

from .normalize import FieldStats, normalize


class UnknownFieldError(ValueError):
    """A weight was given for a field that the index does not declare."""


class NegativeWeightError(ValueError):
    """Negative weights invalidate the upper bound; rejected in strict mode."""


class AssumptionViolatedError(RuntimeError):
    """A sorted-access stream was not non-increasing: bound assumption broken."""


class View(Protocol):
    """Structural type of per-field query access objects."""

    field: str
    stats: FieldStats

    @property
    def has_field(self) -> frozenset[str]: ...

    def sorted_access(self): ...

    def random_access(self, doc_id: str) -> Optional[float]: ...


@dataclass(frozen=True)
class FieldScore:
    """Per-field sub-score of one fused result."""

    field: str
    weight: float
    present: bool
    raw: Optional[float]
    normalized: Optional[float]


@dataclass(frozen=True)
class ScoredDoc:
    doc_id: str
    score: float
    field_scores: tuple[FieldScore, ...]

    def sub_scores(self) -> dict[str, Optional[float]]:
        return {fs.field: fs.normalized for fs in self.field_scores}


def validate_weights(
    weights: Mapping[str, float], declared_fields: tuple[str, ...]
) -> tuple[dict[str, float], dict[str, float]]:
    """Validate weights; return ``(active, negatives)``.

    * unknown field names -> :class:`UnknownFieldError`
    * NaN weights -> :class:`ValueError` (never degradable)
    * weight == 0 fields are dropped from the active set: they do not affect
      the ranking and are never accessed.
    * negative weights are reported separately so the caller can reject or
      degrade; they make the TopK upper bound invalid.
    """
    if not weights:
        raise ValueError("weights must not be empty")
    for name, weight in weights.items():
        if name not in declared_fields:
            raise UnknownFieldError(f"unknown field: {name!r}")
        if isinstance(weight, bool) or not isinstance(weight, (int, float)):
            raise TypeError(f"weight for {name!r} must be a number")
        if math.isnan(weight):
            raise ValueError(f"weight for {name!r} is NaN")
    negatives = {f: float(w) for f, w in weights.items() if w < 0}
    active = {f: float(w) for f, w in weights.items() if w > 0}
    return active, negatives


def aggregate(field_scores: tuple[FieldScore, ...]) -> Optional[float]:
    """Weighted average over present fields; None if denominator <= 0."""
    numerator = 0.0
    denominator = 0.0
    for fs in field_scores:
        if fs.present:
            numerator += fs.weight * fs.normalized
            denominator += fs.weight
    if denominator <= 0.0:
        return None
    return numerator / denominator


def score_document(
    doc_id: str, views: Mapping[str, View], weights: Mapping[str, float]
) -> Optional[ScoredDoc]:
    """Exact fused score for one document (None if it must be dropped)."""
    parts: list[FieldScore] = []
    for field in sorted(weights):
        raw = views[field].random_access(doc_id)
        if raw is None:
            parts.append(FieldScore(field, weights[field], False, None, None))
        else:
            parts.append(
                FieldScore(
                    field, weights[field], True, raw, normalize(raw, views[field].stats)
                )
            )
    score = aggregate(tuple(parts))
    if score is None:
        return None
    return ScoredDoc(doc_id, score, tuple(parts))


def _rank(docs: list[ScoredDoc]) -> list[ScoredDoc]:
    """Deterministic order: score descending, doc_id ascending."""
    return sorted(docs, key=lambda sd: (-sd.score, sd.doc_id))


def threshold_topk(
    views: Mapping[str, View], weights: Mapping[str, float], k: int
) -> tuple[list[ScoredDoc], dict]:
    """Fagin-style threshold algorithm over per-field sorted streams.

    Pulls candidates from each field on demand (round robin, one item per
    field per round) and stops as soon as the remaining-score upper bound
    proves the current top-k final.  See the module docstring for the proof
    sketch.  Raises :class:`AssumptionViolatedError` if a stream is not
    non-increasing.
    """
    if k < 0:
        raise ValueError("k must be >= 0")
    fields = sorted(weights)
    meta: dict = {
        "fields": fields,
        "pulls": {f: 0 for f in fields},
        "stopped_early": False,
        "bound": None,
        "kth_score": None,
    }
    if k == 0:
        return [], meta

    stats = {f: views[f].stats for f in fields}
    thresholds = {f: normalize(stats[f].maximum, stats[f]) for f in fields}
    streams = {f: iter(views[f].sorted_access()) for f in fields}
    previous_raw: dict[str, float] = {}
    exhausted: set[str] = set()
    exact: dict[str, ScoredDoc] = {}

    while True:
        if len(exact) >= k:
            bound = max(thresholds.values())
            kth = sorted((sd.score for sd in exact.values()), reverse=True)[k - 1]
            meta["bound"] = bound
            meta["kth_score"] = kth
            if kth > bound:
                meta["stopped_early"] = True
                break
        if len(exhausted) == len(fields):
            break
        progressed = False
        for field in fields:
            if field in exhausted:
                continue
            try:
                doc_id, raw = next(streams[field])
            except StopIteration:
                exhausted.add(field)
                # Unseen documents in this field have raw score exactly 0.
                thresholds[field] = normalize(0.0, stats[field])
                continue
            progressed = True
            meta["pulls"][field] += 1
            if field in previous_raw and raw > previous_raw[field] + 1e-9:
                raise AssumptionViolatedError(
                    f"sorted access for field {field!r} is not non-increasing"
                )
            previous_raw[field] = raw
            thresholds[field] = normalize(raw, stats[field])
            if doc_id not in exact:
                scored = score_document(doc_id, views, weights)
                if scored is not None:
                    exact[doc_id] = scored
        if not progressed:
            break

    if len(exhausted) == len(fields):
        # Streams drained: score every remaining document that has at least
        # one active field so the tail order matches the exhaustive reference.
        for field in fields:
            for doc_id in views[field].has_field:
                if doc_id not in exact:
                    scored = score_document(doc_id, views, weights)
                    if scored is not None:
                        exact[doc_id] = scored

    meta["candidates_scored"] = len(exact)
    return _rank(list(exact.values()))[:k], meta


def exhaustive_topk(
    views: Mapping[str, View], weights: Mapping[str, float], k: Optional[int]
) -> tuple[list[ScoredDoc], dict]:
    """Reference fusion: score every candidate exactly, no early stop.

    This is the ground truth used to verify the threshold algorithm, and the
    degradation target when the upper-bound assumptions do not hold (e.g.
    negative weights -- in which case documents whose present-weight sum is
    <= 0 are dropped).
    """
    if k is not None and k < 0:
        raise ValueError("k must be >= 0 or None")
    candidates: set[str] = set()
    for field in weights:
        candidates |= views[field].has_field
    scored: list[ScoredDoc] = []
    for doc_id in candidates:
        sd = score_document(doc_id, views, weights)
        if sd is not None:
            scored.append(sd)
    ranked = _rank(scored)
    meta = {"candidates_scored": len(scored), "fields": sorted(weights)}
    if k is None:
        return ranked, meta
    return ranked[:k], meta
