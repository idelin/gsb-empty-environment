"""Proof-package builder and incremental retraction.

A package proves, against a committed ordered dataset root:
  * membership completeness: the revealed contributions are exactly the
    records whose key falls in the rule interval [lo, hi] -- excluded regions
    are represented by covering subtree digests, and the boundary records
    adjacent to the selected range are revealed and shown to fall outside
    the interval (valid because the commitment tree is ordered by key);
  * aggregation arithmetic: sum / count / weighted-average numerator and
    denominator recomputed from the revealed contribution list;
  * replayability: every computation step is hash-chained, so the whole
    package can be replayed from the genesis hash.

No wall-clock or randomness is used: packages are deterministic.
"""
from __future__ import annotations

import copy
import hashlib
from bisect import bisect_left, bisect_right

from .commitments import CommitmentTree, canonical, leaf_hash
from .model import FilterRule, Record, aggregate, contribution_summary

GENESIS_HASH = hashlib.sha256(b"proofpack/genesis/v1").hexdigest()


def step_hash(prev_hex: str, body: dict) -> str:
    return hashlib.sha256(
        b"proofpack/step/v1:" + prev_hex.encode() + b":" + canonical(body)
    ).hexdigest()


def _append_step(steps: list[dict], body: dict) -> dict:
    prev = steps[-1]["hash"] if steps else GENESIS_HASH
    step = dict(body)
    step["prev_hash"] = prev
    step["hash"] = step_hash(prev, body)
    steps.append(step)
    return step


def build_package(
    records: list[Record],
    rule: FilterRule,
    package_id: str,
    version: int = 1,
    dataset_id: str = "ds:synthetic:v1",
) -> dict:
    if rule.field != "key":
        raise ValueError("only the ordered 'key' field is supported")
    keys = [r.key for r in records]
    if keys != sorted(keys):
        raise ValueError("records must be ordered by key (ordered commitment tree)")

    facts = [r.to_fact() for r in records]
    leaves = [leaf_hash(f) for f in facts]
    tree = CommitmentTree(leaves)
    n = len(records)

    lo_i = bisect_left(keys, rule.lo)
    hi_i = bisect_right(keys, rule.hi)

    contributions = [
        {"index": i, "record": facts[i], "leaf": leaves[i]}
        for i in range(lo_i, hi_i)
    ]
    boundary_left = (
        {"index": lo_i - 1, "record": facts[lo_i - 1], "leaf": leaves[lo_i - 1]}
        if lo_i > 0 else None
    )
    boundary_right = (
        {"index": hi_i, "record": facts[hi_i], "leaf": leaves[hi_i]}
        if hi_i < n else None
    )

    segments: list[dict] = []
    if lo_i > 0:
        segments += tree.covering_digests(0, lo_i - 1)
    right_start = hi_i + 1 if hi_i < n else n
    segments += tree.covering_digests(right_start, tree.size)

    selected = [records[i] for i in range(lo_i, hi_i)]
    summary = contribution_summary([c["leaf"] for c in contributions])
    aggregates = aggregate(selected)

    steps: list[dict] = []
    _append_step(steps, {
        "step_id": f"{package_id}:S1:select",
        "version": 1,
        "kind": "select",
        "rule": rule.to_dict(),
        "contribution_rids": [c["record"]["rid"] for c in contributions],
        "contribution_summary": summary,
    })
    _append_step(steps, {
        "step_id": f"{package_id}:S2:aggregate",
        "version": 1,
        "kind": "aggregate",
        "aggregates": aggregates,
    })

    return {
        "package_id": package_id,
        "version": version,
        "dataset": {
            "dataset_id": dataset_id,
            "leaf_count": n,
            "padded_size": tree.size,
            "root": tree.root,
        },
        "rule": rule.to_dict(),
        "evidence": {
            "segments": segments,
            "boundary_left": boundary_left,
            "boundary_right": boundary_right,
            "contributions": contributions,
        },
        "retractions": [],
        "contribution_summary": summary,
        "aggregates": aggregates,
        "steps": steps,
        "package_digest": steps[-1]["hash"],
    }


def retract_contribution(pkg: dict, rid: str) -> dict:
    """Incrementally retract one contribution; returns a new package version.

    The range-completeness evidence (segments/boundaries/contributions) still
    proves the *original* selection; the retraction is applied as a new
    hash-chained step that updates numerator, denominator, sum, count, and
    the contribution summary in sync.
    """
    new = copy.deepcopy(pkg)
    new["version"] = pkg["version"] + 1

    contribs = new["evidence"]["contributions"]
    matches = [c for c in contribs if c["record"]["rid"] == rid]
    if len(matches) != 1:
        raise ValueError(f"rid {rid!r} is not a contribution of this package")
    if any(r["rid"] == rid for r in new["retractions"]):
        raise ValueError(f"rid {rid!r} was already retracted")
    removed = matches[0]["record"]

    retracted_rids = {r["rid"] for r in new["retractions"]} | {rid}
    effective = [c for c in contribs if c["record"]["rid"] not in retracted_rids]
    new_aggregates = aggregate([Record.from_fact(c["record"]) for c in effective])
    new_summary = contribution_summary([c["leaf"] for c in effective])

    step_body = {
        "step_id": f"{pkg['package_id']}:S{len(pkg['steps']) + 1}:retract",
        "version": 1,
        "kind": "retract",
        "removed_rid": rid,
        "removed_record": removed,
        "delta": {
            "sum": -removed["value"],
            "count": -1,
            "wavg_numerator": -removed["value"] * removed["weight"],
            "wavg_denominator": -removed["weight"],
        },
        "updated_aggregates": new_aggregates,
        "updated_summary": new_summary,
        "previous_package_digest": pkg["package_digest"],
    }
    new["retractions"].append({
        "rid": rid, "record": removed, "step_id": step_body["step_id"],
    })
    new["aggregates"] = new_aggregates
    new["contribution_summary"] = new_summary
    _append_step(new["steps"], step_body)
    new["package_digest"] = new["steps"][-1]["hash"]
    return new
