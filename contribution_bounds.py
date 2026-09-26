"""Per-subject contribution bounding with deterministic shard merging.

Synthetic data only. All parameters and adjacency definitions are documented
in README.md. The small-domain enumeration is a sanity check on synthetic
data, NOT a formal privacy proof.

Design invariants:
  * Selection is a pure function of the candidate record set, so it is
    independent of record order and shard order.
  * Dedup by record_id is global: moving a record to another shard cannot
    bypass it.
  * Global caps (records per subject, groups per subject, value range) are
    applied after merging, so shard merging cannot bypass them.
  * Withdrawal / correction re-run selection over the retained candidate
    pool, so freed slots are deterministically backfilled.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, replace
from itertools import product


# --------------------------------------------------------------------------
# Configuration
# --------------------------------------------------------------------------

@dataclass(frozen=True)
class BoundConfig:
    """Global contribution bounds applied per subject_id."""

    max_records_per_subject: int = 4
    max_groups_per_subject: int = 2
    value_lower: float = -5.0
    value_upper: float = 5.0

    def __post_init__(self) -> None:
        if self.max_records_per_subject < 1:
            raise ValueError("max_records_per_subject must be >= 1")
        if self.max_groups_per_subject < 1:
            raise ValueError("max_groups_per_subject must be >= 1")
        if not self.value_lower <= self.value_upper:
            raise ValueError("value_lower must be <= value_upper")

    @property
    def max_abs_value(self) -> float:
        return max(abs(self.value_lower), abs(self.value_upper))

    @property
    def l1_bound_add_remove(self) -> float:
        """L1 sensitivity of the clipped kept-sum under add/remove-one-subject."""
        return self.max_records_per_subject * self.max_abs_value

    @property
    def l1_bound_replace(self) -> float:
        """L1 sensitivity of the clipped kept-sum under replace-one-subject."""
        return self.max_records_per_subject * (self.value_upper - self.value_lower)


# --------------------------------------------------------------------------
# Records and evidence
# --------------------------------------------------------------------------

@dataclass(frozen=True)
class Record:
    record_id: str
    subject_id: str
    group_id: str
    value: float
    shard_id: str = ""

    def dedup_key(self) -> tuple:
        # Canonical tie-break when the same record_id appears with different
        # content (e.g. in two shards): fully deterministic, order-free.
        return (self.record_id, self.shard_id, self.subject_id,
                self.group_id, self.value)


@dataclass(frozen=True)
class Evidence:
    """Per-record clipping / selection evidence."""

    record_id: str
    subject_id: str
    shard_id: str
    decision: str            # "kept" | "dropped"
    reason: str              # kept | record_cap | group_cap | duplicate_id | withdrawn
    raw_value: float
    clipped_value: float
    was_clipped: bool


@dataclass(frozen=True)
class SubjectAudit:
    """Per-subject contribution audit before and after bounding."""

    subject_id: str
    candidate_count: int
    kept_count: int
    groups_kept: int
    raw_sum_all: float        # contribution before clipping/capping
    clipped_sum_kept: float   # contribution after clipping/capping
    l1_contribution: float    # |clipped_sum_kept|
    l1_bound_add_remove: float
    within_bound: bool


@dataclass(frozen=True)
class SelectionResult:
    kept: tuple
    evidence: tuple
    audit: tuple

    @property
    def kept_ids(self) -> tuple:
        return tuple(r.record_id for r in self.kept)

    @property
    def total_sum(self) -> float:
        return sum(r.value for r in self.kept)


# --------------------------------------------------------------------------
# Core primitives
# --------------------------------------------------------------------------

def clamp(value: float, lower: float, upper: float) -> float:
    return min(max(value, lower), upper)


def dedup_records(records) -> tuple:
    """Global dedup by record_id; deterministic canonical winner."""
    best = {}
    for rec in records:
        cur = best.get(rec.record_id)
        if cur is None or rec.dedup_key() < cur.dedup_key():
            best[rec.record_id] = rec
    return tuple(best[k] for k in sorted(best))


def select_records(records, config: BoundConfig,
                   withdrawn_subjects=frozenset()) -> SelectionResult:
    """Deterministic global selection.

    Pure function of the input record *set*: output is invariant under
    permutation of `records` and under any shard assignment, because all
    ordering decisions use record_id / shard_id sort keys only.
    """
    records = list(records)
    deduped = dedup_records(records)
    winners = {id(r) for r in deduped}

    by_subject = {}
    for rec in deduped:
        by_subject.setdefault(rec.subject_id, []).append(rec)

    kept = []
    evidence = []
    audit = []
    for subject_id in sorted(by_subject):
        recs = sorted(by_subject[subject_id], key=lambda r: r.record_id)
        raw_sum_all = sum(r.value for r in recs)
        chosen_groups = set()
        kept_count = 0
        clipped_sum = 0.0
        if subject_id in withdrawn_subjects:
            for rec in recs:
                evidence.append(Evidence(
                    rec.record_id, subject_id, rec.shard_id,
                    "dropped", "withdrawn", rec.value,
                    clamp(rec.value, config.value_lower, config.value_upper),
                    False))
            audit.append(SubjectAudit(
                subject_id, len(recs), 0, 0, raw_sum_all, 0.0, 0.0,
                config.l1_bound_add_remove, True))
            continue
        for rec in recs:
            clipped = clamp(rec.value, config.value_lower, config.value_upper)
            was_clipped = clipped != rec.value
            if kept_count >= config.max_records_per_subject:
                decision, reason = "dropped", "record_cap"
            elif (rec.group_id not in chosen_groups
                  and len(chosen_groups) >= config.max_groups_per_subject):
                decision, reason = "dropped", "group_cap"
            else:
                decision, reason = "kept", "kept"
                chosen_groups.add(rec.group_id)
                kept_count += 1
                clipped_sum += clipped
                kept.append(replace(rec, value=clipped))
            evidence.append(Evidence(
                rec.record_id, subject_id, rec.shard_id,
                decision, reason, rec.value, clipped, was_clipped))
        l1 = abs(clipped_sum)
        audit.append(SubjectAudit(
            subject_id, len(recs), kept_count, len(chosen_groups),
            raw_sum_all, clipped_sum, l1,
            config.l1_bound_add_remove,
            l1 <= config.l1_bound_add_remove + 1e-9))

    for rec in records:
        if id(rec) not in winners:
            evidence.append(Evidence(
                rec.record_id, rec.subject_id, rec.shard_id,
                "dropped", "duplicate_id", rec.value,
                clamp(rec.value, config.value_lower, config.value_upper),
                False))

    kept.sort(key=lambda r: r.record_id)
    evidence.sort(key=lambda e: (e.record_id, e.shard_id, e.reason))
    return SelectionResult(tuple(kept), tuple(evidence), tuple(audit))


# --------------------------------------------------------------------------
# Shard states and merging
# --------------------------------------------------------------------------

@dataclass(frozen=True)
class ShardState:
    """Serializable per-shard merge state.

    Carries the shard's canonical (deduped, sorted) candidate records plus
    per-subject tallies, so a merger can enforce *global* caps after
    combining any number of shards in any order.
    """

    shard_id: str
    records: tuple
    subject_record_counts: tuple  # sorted (subject_id, count) pairs

    def to_json(self) -> str:
        return json.dumps({
            "shard_id": self.shard_id,
            "records": [vars(r) for r in self.records],
            "subject_record_counts": list(self.subject_record_counts),
        }, sort_keys=True)

    @property
    def size_bytes(self) -> int:
        return len(self.to_json().encode("utf-8"))

    @property
    def record_count(self) -> int:
        return len(self.records)


def build_shard_state(records, shard_id: str) -> ShardState:
    tagged = [replace(r, shard_id=shard_id) for r in records]
    canonical = dedup_records(tagged)
    counts = {}
    for rec in canonical:
        counts[rec.subject_id] = counts.get(rec.subject_id, 0) + 1
    return ShardState(
        shard_id=shard_id,
        records=canonical,
        subject_record_counts=tuple(sorted(counts.items())),
    )


@dataclass(frozen=True)
class MergeReport:
    result: SelectionResult
    shard_state_sizes: tuple   # (shard_id, size_bytes, record_count)
    merged_state_size: int     # bytes of the merged candidate state
    merged_record_count: int


def merge_shard_states(states, config: BoundConfig,
                       withdrawn_subjects=frozenset()) -> MergeReport:
    """Merge shard states; global caps are enforced *after* merging.

    Commutative in `states`: selection depends only on the union of
    canonical records, never on shard order.
    """
    all_records = []
    for state in states:
        all_records.extend(state.records)
    # Pass the un-deduped union so cross-shard duplicate_ids appear in the
    # evidence trail; selection itself dedups deterministically.
    result = select_records(all_records, config, withdrawn_subjects)
    merged_records = dedup_records(all_records)
    merged_json = json.dumps(
        [vars(r) for r in merged_records], sort_keys=True).encode("utf-8")
    sizes = tuple((s.shard_id, s.size_bytes, s.record_count) for s in states)
    return MergeReport(result, sizes, len(merged_json), len(merged_records))


# --------------------------------------------------------------------------
# Stateful bounder: withdrawal and correction with backfill
# --------------------------------------------------------------------------

class ContributionBounder:
    """Holds the full candidate pool so freed slots can be backfilled."""

    def __init__(self, config: BoundConfig):
        self.config = config
        self._candidates = {}          # record_id -> Record
        self._withdrawn = set()        # subject_ids

    def add_records(self, records) -> None:
        for rec in records:
            cur = self._candidates.get(rec.record_id)
            if cur is None or rec.dedup_key() < cur.dedup_key():
                self._candidates[rec.record_id] = rec

    def withdraw_subject(self, subject_id: str) -> None:
        self._withdrawn.add(subject_id)

    def correct_record(self, record_id: str, *, value=None,
                       group_id=None) -> None:
        if record_id not in self._candidates:
            raise KeyError(f"unknown record_id: {record_id}")
        rec = self._candidates[record_id]
        if value is not None:
            rec = replace(rec, value=value)
        if group_id is not None:
            rec = replace(rec, group_id=group_id)
        self._candidates[record_id] = rec

    def select(self) -> SelectionResult:
        # Re-selection over the retained candidate pool deterministically
        # backfills slots freed by withdrawal or by cap-relevant corrections.
        return select_records(
            list(self._candidates.values()), self.config,
            frozenset(self._withdrawn))


# --------------------------------------------------------------------------
# Small-domain enumeration: empirical L1 contribution check
# --------------------------------------------------------------------------

def enumerate_subject_l1(config: BoundConfig, value_domain, group_domain,
                         max_records=None, shard_ids=("",)):
    """Exhaustively enumerate one subject's possible datasets on a small
    finite domain and measure the actual L1 contribution of the clipped
    kept-sum statistic.

    Returns a dict with the observed max |sum| (add/remove view), the
    observed max pairwise difference (replace view), the theoretical
    bounds, and how many datasets were enumerated. Because the statistic
    is scalar, the replace-view max equals max(sum) - min(sum); no
    quadratic pair loop is needed.

    Synthetic check only -- not a formal privacy proof.
    """
    if max_records is None:
        max_records = config.max_records_per_subject + 1
    cells = list(product(value_domain, group_domain, shard_ids))
    sums = []
    count = 0
    for n in range(0, max_records + 1):
        for combo in product(cells, repeat=n):
            recs = [
                Record(record_id=f"r{i}", subject_id="subj",
                       group_id=g, value=v, shard_id=s)
                for i, (v, g, s) in enumerate(combo)
            ]
            res = select_records(recs, config)
            sums.append(res.total_sum)
            count += 1
    observed_add_remove = max(abs(s) for s in sums)
    observed_replace = max(sums) - min(sums)
    return {
        "datasets_enumerated": count,
        "observed_l1_add_remove": observed_add_remove,
        "bound_add_remove": config.l1_bound_add_remove,
        "add_remove_ok": observed_add_remove <= config.l1_bound_add_remove + 1e-9,
        "observed_l1_replace": observed_replace,
        "bound_replace": config.l1_bound_replace,
        "replace_ok": observed_replace <= config.l1_bound_replace + 1e-9,
    }
