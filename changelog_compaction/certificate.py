"""Independently replayable equivalence certificates."""

from __future__ import annotations

import copy
import hashlib
import hmac
from typing import Any

from .changelog import (
    Change,
    dump_change,
    load_changes,
    replay,
)
from .compaction import compact, dump_change as dump_compacted

SCHEMA = "pairwise-changelog-compaction/v1"
BARRIER_POLICY = [
    "no merge across commit_seq (declared visibility point)",
    "no merge across table (schema barrier)",
    "no merge across primary key (entity barrier)",
    "no merge across txn_id (transaction barrier)",
    "delete/recreate is split on ABSENT so identity generation is preserved",
]


def canonical_json(data: Any) -> str:
    return __import__("json").dumps(
        data, sort_keys=True, ensure_ascii=False, separators=(",", ":")
    )


def _digest(data: Any) -> str:
    return hashlib.sha256(canonical_json(data).encode("utf-8")).hexdigest()


def _snapshot_view(snapshots: dict[int, dict]) -> list[dict]:
    return [
        {
            "commit_seq": seq,
            "state": snapshots[seq]["state"],
            "aggregate": snapshots[seq]["aggregate"],
        }
        for seq in sorted(snapshots)
    ]


def build_certificate(
    raw: list[dict],
    budget: int | None = None,
    audit: bool = False,
    secret: str = "local-fixture",
) -> dict:
    changes = load_changes(raw)
    result = compact(changes, budget=budget, audit=audit)
    commit_points = sorted({change.commit_seq for change in changes})

    raw_snapshots = replay(changes, commit_points)
    compacted_snapshots = replay(result.changes, commit_points)

    if raw_snapshots != compacted_snapshots:
        raise ValueError("compaction is not equivalent at declared commit points")

    payload = {
        "schema": SCHEMA,
        "barrier_policy": BARRIER_POLICY,
        "source": "local_fixture",
        "audit_mode": audit,
        "budget": budget,
        "merges_used": result.merges_used,
        "budget_exhausted": result.budget_exhausted,
        "commit_points": commit_points,
        "raw_changes": [dump_change(change) for change in changes],
        "compacted_changes": [dump_compacted(change) for change in result.changes],
        "elisions": [elision.to_dict() for elision in result.elisions],
        "declared_views": _snapshot_view(raw_snapshots),
    }
    payload["raw_digest"] = _digest(payload["raw_changes"])
    payload["compacted_digest"] = _digest(payload["compacted_changes"])
    payload["view_digest"] = _digest(payload["declared_views"])
    signature = hmac.new(
        secret.encode("utf-8"),
        payload["view_digest"].encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()
    payload["signature"] = signature
    return payload


def verify_certificate(
    certificate: dict, secret: str = "local-fixture"
) -> tuple[bool, list[str]]:
    """Replay both embedded feeds from scratch and prove equivalence.

    Nothing in the certificate is trusted: snapshots are recomputed,
    before-values are validated by the replayer, digests and the HMAC are
    checked, and fused records are refused if they cross any barrier.
    """
    problems: list[str] = []
    certificate = copy.deepcopy(certificate)

    if certificate.get("schema") != SCHEMA:
        problems.append("unknown certificate schema")

    try:
        raw = load_changes(certificate["raw_changes"])
        compacted = load_changes(certificate["compacted_changes"])
    except (KeyError, ValueError, TypeError) as exc:
        problems.append(f"malformed embedded feed: {exc}")
        return False, problems

    raw_index = {change.idx: change for change in raw}
    for change in compacted:
        if change.idx < 0:
            continue
        if change.idx not in raw_index:
            problems.append(f"compacted idx {change.idx} is not in raw feed")

    for change in compacted:
        if change.idx >= 0:
            continue
        source = next(
            (
                entry
                for entry in certificate.get("elisions", [])
                if entry.get("commit_seq") == change.commit_seq
                and entry.get("table") == change.table
                and canonical_json(entry.get("key"))
                == canonical_json(change.key)
                and entry.get("kind") != "INSERT_DELETE_CANCEL"
            ),
            None,
        )
        if source is None:
            problems.append(f"fused change at idx {change.idx} lacks provenance")
            continue
        source_ids = source.get("sources", [])
        if any(i not in raw_index for i in source_ids):
            problems.append("fused provenance references unknown raw idx")
        referenced = [raw_index[i] for i in source_ids if i in raw_index]
        seqs = {entry.commit_seq for entry in referenced}
        tables = {entry.table for entry in referenced}
        keys = {canonical_json(entry.key) for entry in referenced}
        txns = {entry.txn_id for entry in referenced}
        if len(seqs) > 1:
            problems.append("fused record crosses commit visibility barrier")
        if len(tables) > 1:
            problems.append("fused record crosses schema/table barrier")
        if len(keys) > 1:
            problems.append("fused record crosses primary-key barrier")
        if len(txns) > 1:
            problems.append("fused record crosses transaction id barrier")
        if referenced:
            if referenced[0].before != change.before or referenced[-1].after != change.after:
                problems.append(
                    "fused record endpoints disagree with provenance"
                )

    # Audit/cancel elisions must not cross barriers either.
    for entry in certificate.get("elisions", []):
        ids = entry.get("sources", [])
        referenced = [raw_index[i] for i in ids if i in raw_index]
        if len(ids) != len(referenced):
            problems.append("elision references unknown raw idx")
        if len({entry2.commit_seq for entry2 in referenced}) > 1:
            problems.append("elision crosses commit visibility barrier")
        if len({entry2.table for entry2 in referenced}) > 1:
            problems.append("elision crosses schema/table barrier")
        if len({canonical_json(entry2.key) for entry2 in referenced}) > 1:
            problems.append("elision crosses primary-key barrier")
        if len({entry2.txn_id for entry2 in referenced}) > 1:
            problems.append("elision crosses transaction id barrier")

    points = sorted(
        {change.commit_seq for change in raw}
        | {change.commit_seq for change in compacted}
    )
    declared = sorted(certificate.get("commit_points", []))
    if declared != points:
        problems.append("declared commit points differ from embedded feeds")

    try:
        raw_views = replay(raw, points)
    except ValueError as exc:
        problems.append(f"raw feed before-value failure: {exc}")
        raw_views = {}
    try:
        compacted_views = replay(compacted, points)
    except ValueError as exc:
        problems.append(f"compacted feed before-value failure: {exc}")
        compacted_views = {}

    if raw_views and compacted_views and raw_views != compacted_views:
        for seq in points:
            if raw_views.get(seq) != compacted_views.get(seq):
                problems.append(
                    f"commit {seq} diverges: state/identity/aggregate mismatch"
                )

    declared_views = certificate.get("declared_views")
    if declared_views is not None and raw_views:
        recomputed = _snapshot_view(raw_views)
        if declared_views != recomputed:
            problems.append("embedded declared views disagree with fresh replay")

    embedded_raw = certificate.get("raw_changes")
    embedded_compacted = certificate.get("compacted_changes")
    embedded_views = certificate.get("declared_views")
    if embedded_raw is not None and _digest(embedded_raw) != certificate.get(
        "raw_digest"
    ):
        problems.append("raw_digest mismatch")
    if embedded_compacted is not None and _digest(embedded_compacted) != certificate.get(
        "compacted_digest"
    ):
        problems.append("compacted_digest mismatch")
    if embedded_views is not None and _digest(embedded_views) != certificate.get(
        "view_digest"
    ):
        problems.append("view_digest mismatch")

    expected_sig = hmac.new(
        str(secret).encode("utf-8"),
        str(certificate.get("view_digest", "")).encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()
    if not hmac.compare_digest(expected_sig, str(certificate.get("signature", ""))):
        problems.append("signature mismatch")

    return not problems, problems


def assert_inequivalent(
    raw: list[dict],
    candidate: list[dict],
) -> list[str]:
    """Return the concrete reasons two feeds differ at declared points.

    Used to document counter-examples where the final value is the same but
    intermediate visibility/identity/aggregate semantics are not.
    """
    raw_changes = load_changes(raw)
    candidate_changes = load_changes(candidate)
    points = sorted(
        {change.commit_seq for change in raw_changes}
        | {change.commit_seq for change in candidate_changes}
    )
    raw_views = replay(raw_changes, points)
    candidate_views = replay(candidate_changes, points)
    reasons: list[str] = []
    for seq in points:
        left = raw_views.get(seq)
        right = candidate_views.get(seq)
        if left != right:
            reasons.append(f"commit {seq} is not equivalent")
            if left and right and left["state"] != right["state"]:
                reasons.append("  -> final state or entity identity differs")
            if left and right and left["aggregate"] != right["aggregate"]:
                reasons.append("  -> downstream incremental aggregate differs")
    return reasons or ["feeds are equivalent"]
