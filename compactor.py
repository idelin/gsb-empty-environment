"""Boundary-aware compaction of consecutive same-key changes.

Merges consecutive changes for the same (table, primary key) without ever
crossing a declared transaction-visibility boundary, a schema barrier, or a
delete/recreate identity boundary.  Produces independently replayable
equivalence certificates covering final state, entity identity, before-value
validation and downstream incremental aggregation effects.

Pure Python 3.11 standard library.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, replace
from itertools import combinations_with_replacement, product
from typing import Any, Iterable, Optional, Sequence

CERT_FORMAT = "change-compaction-cert/v1"


class ChainError(ValueError):
    """Before/after values of the stream do not chain correctly."""


class CertificateError(ValueError):
    """A certificate failed independent replay."""


# ---------------------------------------------------------------------------
# Change model
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Change:
    """One row-level change from a local fixture.

    before=None  -> row absent before (insert)
    after=None   -> row absent after  (delete)
    origin       -> indices of source changes this (merged) change stems from
    """

    tx_id: str
    table: str
    pk: str
    before: Any
    after: Any
    commit_seq: int
    schema: str = "v1"
    origin: tuple = ()

    @property
    def kind(self) -> str:
        if self.before is None and self.after is not None:
            return "insert"
        if self.before is not None and self.after is None:
            return "delete"
        if self.before is None and self.after is None:
            return "noop"
        return "update"

    def to_dict(self) -> dict:
        return {
            "tx_id": self.tx_id,
            "table": self.table,
            "pk": self.pk,
            "before": self.before,
            "after": self.after,
            "commit_seq": self.commit_seq,
            "schema": self.schema,
            "origin": list(self.origin),
        }

    @staticmethod
    def from_dict(d: dict) -> "Change":
        return Change(
            tx_id=d["tx_id"],
            table=d["table"],
            pk=d["pk"],
            before=d.get("before"),
            after=d.get("after"),
            commit_seq=d["commit_seq"],
            schema=d.get("schema", "v1"),
            origin=tuple(d.get("origin", ())),
        )


# ---------------------------------------------------------------------------
# Stream validation (before-value verification)
# ---------------------------------------------------------------------------

def validate_chain(stream: Sequence[Change]) -> None:
    """Verify before-values chain against replayed state and commit order."""
    last_commit: Optional[int] = None
    state: dict = {}
    for i, c in enumerate(stream):
        if last_commit is not None and c.commit_seq < last_commit:
            raise ChainError(
                f"change #{i}: commit_seq {c.commit_seq} goes backwards "
                f"(previous {last_commit})"
            )
        last_commit = c.commit_seq
        key = (c.table, c.pk)
        if key not in state:
            # First time this key appears in the captured segment: accept its
            # declared before-value as the pre-existing initial state.
            state[key] = c.before
        current = state[key]
        if current != c.before:
            raise ChainError(
                f"change #{i} ({c.table}/{c.pk}): before={c.before!r} does not "
                f"match replayed value {current!r}"
            )
        if c.after is None:
            state.pop(key, None)
        else:
            state[key] = c.after


# ---------------------------------------------------------------------------
# Compaction
# ---------------------------------------------------------------------------

_ANNIHILATE = object()  # sentinel: insert+delete cancel out


def _try_merge(a: Change, b: Change, visible: frozenset):
    """Return merged Change, _ANNIHILATE, or None if a barrier forbids merging."""
    if a.table != b.table or a.pk != b.pk:
        return None
    if a.schema != b.schema:
        return None  # schema barrier
    if a.after != b.before:
        raise ChainError(
            f"adjacent changes for {a.table}/{a.pk} do not chain: "
            f"{a.after!r} != {b.before!r}"
        )
    if a.after is None and b.before is None:
        # delete followed by insert (recreate): entity identity changes.
        # Merging into an update would hide the identity boundary.
        return None
    if a.tx_id != b.tx_id and a.commit_seq in visible:
        # Transaction-visibility barrier: a's commit is a declared visible
        # point, so the intermediate version must remain observable.
        return None
    if a.before is None and b.after is None:
        # insert then delete: the entity never visibly existed.
        return _ANNIHILATE
    return Change(
        tx_id=a.tx_id if a.tx_id == b.tx_id else b.tx_id,
        table=a.table,
        pk=a.pk,
        before=a.before,          # earliest before-value preserved
        after=b.after,            # final state preserved
        commit_seq=b.commit_seq,  # becomes visible at the later commit
        schema=a.schema,
        origin=a.origin + b.origin,
    )


def compact(
    changes: Sequence[Change],
    visible_commit_points: Iterable[int] = (),
    *,
    audit: bool = False,
    budget: Optional[int] = None,
) -> dict:
    """Compact consecutive same-key changes.

    visible_commit_points: commit sequences whose post-commit state is
        observable downstream; merging may not hide those versions.
    audit:   retain evidence for annihilated insert+delete pairs.
    budget:  maximum number of merge operations; once exceeded, the
        remaining stream is kept uncompressed.

    Returns {"output": [...], "audit_log": [...], "merges": n}.
    """
    validate_chain(changes)
    changes = [replace(c, origin=(i,)) if not c.origin else c
               for i, c in enumerate(changes)]
    visible = frozenset(visible_commit_points)
    out: list[Change] = []
    audit_log: list[dict] = []
    merges = 0
    for change in changes:
        pending: Optional[Change] = change
        while pending is not None and out:
            if budget is not None and merges >= budget:
                break  # budget exceeded: keep the rest uncompressed
            merged = _try_merge(out[-1], pending, visible)
            if merged is None:
                break
            merges += 1
            if merged is _ANNIHILATE:
                victim = out.pop()
                if audit:
                    audit_log.append({
                        "event": "annihilated_insert_delete",
                        "table": victim.table,
                        "pk": victim.pk,
                        "value": victim.after,
                        "insert_origin": list(victim.origin),
                        "delete_origin": list(pending.origin),
                        "commit_seq": pending.commit_seq,
                    })
                pending = None
            else:
                out[-1] = merged
                pending = None
        if pending is not None:
            out.append(pending)
        # A merged out[-1] is re-tried against the next incoming change on
        # the following iteration, so A->B->C collapses to A->C.
    return {"output": out, "audit_log": audit_log, "merges": merges}


# ---------------------------------------------------------------------------
# Replay: visible versions + downstream incremental aggregation
# ---------------------------------------------------------------------------

def _is_num(v: Any) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def replay_checkpoints(
    stream: Sequence[Change],
    visible_commit_points: Iterable[int] = (),
) -> list[dict]:
    """Replay the stream as a downstream incremental consumer would.

    Returns one checkpoint per declared visible commit point plus a final
    checkpoint.  Each checkpoint carries the full visible state (entity
    identity = set of live (table, pk) keys) and incrementally maintained
    aggregates (row_count, total over numeric values).
    """
    validate_chain(stream)
    points = sorted(set(visible_commit_points))
    state: dict = {}
    row_count = 0
    total: float = 0.0
    checkpoints: list[dict] = []
    pi = 0

    def snapshot(label) -> dict:
        return {
            "commit_seq": label,
            "state": {f"{t}|{k}": v for (t, k), v in sorted(state.items())},
            "aggregates": {"row_count": row_count, "total": total},
        }

    last_idx = len(stream) - 1
    for i, c in enumerate(stream):
        # Flush visible points that precede this change's commit BEFORE
        # applying it; they describe earlier commits.
        while pi < len(points) and points[pi] < c.commit_seq:
            checkpoints.append(snapshot(points[pi]))
            pi += 1
        key = (c.table, c.pk)
        if c.before is None and c.after is not None:        # insert
            row_count += 1
            if _is_num(c.after):
                total += c.after
            state[key] = c.after
        elif c.before is not None and c.after is None:      # delete
            row_count -= 1
            if _is_num(c.before):
                total -= c.before
            state.pop(key, None)
        elif c.before is not None and c.after is not None:  # update
            if _is_num(c.before):
                total -= c.before
            if _is_num(c.after):
                total += c.after
            state[key] = c.after
        # Snapshot only after the whole commit is applied (a commit may span
        # several changes across keys).
        commit_done = (i == last_idx) or (stream[i + 1].commit_seq > c.commit_seq)
        if commit_done:
            while pi < len(points) and points[pi] <= c.commit_seq:
                checkpoints.append(snapshot(points[pi]))
                pi += 1
    while pi < len(points):  # visible points beyond the last change
        checkpoints.append(snapshot(points[pi]))
        pi += 1
    checkpoints.append(snapshot("final"))
    return checkpoints


@dataclass(frozen=True)
class EquivalenceReport:
    equivalent: bool
    diffs: tuple = ()

    def to_dict(self) -> dict:
        return {"equivalent": self.equivalent, "diffs": list(self.diffs)}


def check_equivalence(
    original: Sequence[Change],
    compacted: Sequence[Change],
    visible_commit_points: Iterable[int] = (),
) -> EquivalenceReport:
    """Compare two streams at every declared visible commit point (+final).

    Equivalence covers visible state (hence entity identity), incremental
    aggregates, and before-value chain validity of both streams.
    """
    diffs: list[str] = []
    for name, stream in (("original", original), ("compacted", compacted)):
        try:
            validate_chain(stream)
        except ChainError as exc:
            diffs.append(f"{name} stream fails before-value validation: {exc}")
    if diffs:
        return EquivalenceReport(False, tuple(diffs))
    points = sorted(set(visible_commit_points))
    a = replay_checkpoints(original, points)
    b = replay_checkpoints(compacted, points)
    for ca, cb in zip(a, b):
        label = ca["commit_seq"]
        if ca["state"] != cb["state"]:
            diffs.append(
                f"visible state differs at commit {label}: "
                f"{ca['state']} != {cb['state']}"
            )
        if ca["aggregates"] != cb["aggregates"]:
            diffs.append(
                f"incremental aggregates differ at commit {label}: "
                f"{ca['aggregates']} != {cb['aggregates']}"
            )
    return EquivalenceReport(not diffs, tuple(diffs))


# ---------------------------------------------------------------------------
# Equivalence certificates (independently replayable)
# ---------------------------------------------------------------------------

def _canon(obj: Any) -> bytes:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"),
                      ensure_ascii=False).encode("utf-8")


def _digest(obj: Any) -> str:
    return hashlib.sha256(_canon(obj)).hexdigest()


def build_certificate(
    changes: Sequence[Change],
    visible_commit_points: Iterable[int] = (),
    *,
    audit: bool = False,
    budget: Optional[int] = None,
) -> dict:
    """Compact and emit a self-contained, independently replayable proof."""
    points = sorted(set(visible_commit_points))
    indexed = [replace(c, origin=(i,)) for i, c in enumerate(changes)]
    result = compact(indexed, points, audit=audit, budget=budget)
    output = result["output"]
    report = check_equivalence(indexed, output, points)
    cert = {
        "format": CERT_FORMAT,
        "options": {
            "visible_commit_points": points,
            "audit": audit,
            "budget": budget,
        },
        "input": [c.to_dict() for c in indexed],
        "output": [c.to_dict() for c in output],
        "audit_log": result["audit_log"],
        "merges": result["merges"],
        "checkpoints": replay_checkpoints(indexed, points),
        "equivalent": report.equivalent,
        "equivalence_diffs": list(report.diffs),
        "input_digest": _digest([c.to_dict() for c in indexed]),
        "output_digest": _digest([c.to_dict() for c in output]),
    }
    cert["certificate_digest"] = _digest(cert)
    return cert


def verify_certificate(cert: dict) -> bool:
    """Independently replay a certificate; raise CertificateError on failure."""
    if cert.get("format") != CERT_FORMAT:
        raise CertificateError(f"unsupported format: {cert.get('format')!r}")
    body = {k: v for k, v in cert.items() if k != "certificate_digest"}
    if _digest(body) != cert.get("certificate_digest"):
        raise CertificateError("certificate digest mismatch (tampered?)")

    opts = cert["options"]
    inputs = [Change.from_dict(d) for d in cert["input"]]
    if _digest(cert["input"]) != cert["input_digest"]:
        raise CertificateError("input digest mismatch")

    # 1. Replay compaction from inputs + options.
    result = compact(inputs, opts["visible_commit_points"],
                     audit=opts["audit"], budget=opts["budget"])
    replayed = [c.to_dict() for c in result["output"]]
    if replayed != cert["output"]:
        raise CertificateError("replayed compaction does not match certificate output")
    if _digest(replayed) != cert["output_digest"]:
        raise CertificateError("output digest mismatch")
    if result["audit_log"] != cert["audit_log"]:
        raise CertificateError("audit log mismatch")

    # 2. Replay equivalence at every declared visible commit point.
    output = [Change.from_dict(d) for d in cert["output"]]
    report = check_equivalence(inputs, output, opts["visible_commit_points"])
    if not report.equivalent:
        raise CertificateError(f"equivalence violated: {list(report.diffs)}")
    if replay_checkpoints(inputs, opts["visible_commit_points"]) != cert["checkpoints"]:
        raise CertificateError("checkpoint log mismatch")
    if not cert["equivalent"]:
        raise CertificateError("certificate itself records non-equivalence")
    return True


# ---------------------------------------------------------------------------
# Exhaustive verification over small change sequences
# ---------------------------------------------------------------------------

def _enumerate_streams(max_len: int, values: tuple, commits: tuple):
    """Yield all valid single-key streams up to max_len changes.

    Ops: set(v) for each v in values, plus delete.  Chains are valid by
    construction (before-values come from replay).  Each commit gets its own
    transaction (tx_id == f"tx@{commit}").
    """
    ops = [("set", v) for v in values] + [("delete", None)]
    for n in range(1, max_len + 1):
        for op_seq in product(ops, repeat=n):
            for commit_seq in combinations_with_replacement(commits, n):
                stream = []
                current = None
                for (op, v), cs in zip(op_seq, commit_seq):
                    after = v if op == "set" else None
                    stream.append(Change(
                        tx_id=f"tx@{cs}", table="t", pk="k",
                        before=current, after=after, commit_seq=cs,
                    ))
                    current = after
                yield stream


def exhaustive_verify(max_len: int = 3, values=(0, 1), commits=(1, 2)) -> dict:
    """Exhaustively check boundary-aware compaction on small streams.

    For every enumerated stream and every subset of declared visible commit
    points, compaction must preserve equivalence.  Also counts cases where a
    naive (boundary-ignoring) compaction would be caught violating
    equivalence -- those are the counterexample witnesses.
    """
    from itertools import combinations

    cases = 0
    failures: list[str] = []
    naive_violations = 0
    for stream in _enumerate_streams(max_len, values, commits):
        present = sorted({c.commit_seq for c in stream})
        for r in range(len(present) + 1):
            for subset in combinations(present, r):
                visible = frozenset(subset)
                cases += 1
                out = compact(stream, visible)["output"]
                report = check_equivalence(stream, out, visible)
                if not report.equivalent:
                    failures.append(
                        f"stream={[(c.before, c.after, c.commit_seq) for c in stream]} "
                        f"visible={sorted(visible)} diffs={list(report.diffs)}"
                    )
                naive = compact(stream, ())["output"]
                naive_report = check_equivalence(stream, naive, visible)
                if not naive_report.equivalent:
                    naive_violations += 1
    return {
        "cases": cases,
        "failures": failures,
        "naive_violations": naive_violations,
    }


# ---------------------------------------------------------------------------
# Counterexample: same final state, different intermediate semantics
# ---------------------------------------------------------------------------

def find_counterexample() -> dict:
    """Witness that final-state-only comparison is unsound.

    Streams A and B end in the identical final state, but differ at visible
    commit 1.  A naive compaction that merges across the visible commit
    boundary turns A into B; boundary-aware compaction refuses the merge.
    """
    stream_a = [
        Change(tx_id="tx1", table="acct", pk="k", before=None, after="A",
               commit_seq=1),
        Change(tx_id="tx2", table="acct", pk="k", before="A", after="B",
               commit_seq=2),
    ]
    stream_b = [
        Change(tx_id="tx2", table="acct", pk="k", before=None, after="B",
               commit_seq=2),
    ]
    visible = (1, 2)
    naive_report = check_equivalence(stream_a, stream_b, visible)
    safe = compact(stream_a, visible)["output"]
    safe_report = check_equivalence(stream_a, safe, visible)
    return {
        "stream_a": [c.to_dict() for c in stream_a],
        "stream_b": [c.to_dict() for c in stream_b],
        "final_state_a": replay_checkpoints(stream_a, visible)[-1]["state"],
        "final_state_b": replay_checkpoints(stream_b, visible)[-1]["state"],
        "naive_merge_equivalent": naive_report.equivalent,
        "naive_merge_diffs": list(naive_report.diffs),
        "boundary_aware_output": [c.to_dict() for c in safe],
        "boundary_aware_equivalent": safe_report.equivalent,
    }


# ---------------------------------------------------------------------------
# Local fixtures
# ---------------------------------------------------------------------------

def load_fixture(path: str):
    """Load a local JSON fixture: {"visible_commit_points": [...], "changes": [...]}."""
    with open(path, "r", encoding="utf-8") as fh:
        doc = json.load(fh)
    changes = [Change.from_dict(d) for d in doc["changes"]]
    return changes, doc.get("visible_commit_points", [])
