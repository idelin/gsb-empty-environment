"""Synthetic reference-graph replay validation.

Validates, on a replayed engine:
1. Cross-version reference consistency: every child FK pseudonym resolves
   to the referenced parent PK pseudonym under the child's policy version,
   regardless of which version the parent row was written under.
2. Invisible-field non-interference: stripping invisible fields from the
   input stream yields byte-identical output.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .engine import CdcEvent, Engine


@dataclass(frozen=True)
class Reference:
    child_table: str
    child_field: str
    parent_table: str
    parent_field: str


@dataclass
class ReplayReport:
    ok: bool
    failures: list = field(default_factory=list)
    checked_events: int = 0
    checked_references: int = 0


def validate_replay(engine: Engine, events, references) -> ReplayReport:
    failures: list = []
    checked_refs = 0

    # Registry of known parent logical keys (from any version's events).
    parent_keys: dict = {}
    for event, out in engine.journal:
        if out is None:
            continue
        for ref in references:
            if ref.parent_table == event.table and ref.parent_field in event.pk:
                parent_keys.setdefault(ref.parent_table, set()).add(
                    event.pk[ref.parent_field]
                )

    # Cross-version reference consistency.
    for event, out in engine.journal:
        if out is None:
            continue
        image = event.after if event.after is not None else event.before
        if not image:
            continue
        for ref in references:
            if ref.child_table != event.table:
                continue
            if ref.child_field not in image:
                continue
            checked_refs += 1
            fk_value = image[ref.child_field]
            if fk_value not in parent_keys.get(ref.parent_table, set()):
                failures.append(
                    f"unresolved reference {ref.child_table}.{ref.child_field} "
                    f"at seq {event.commit_seq}"
                )
                continue
            pseudonymizer = engine._pseudonymizers[out["policy_version"]]
            policy = engine._history.at(event.commit_seq)
            parent_fp = policy.field_policy(ref.parent_table, ref.parent_field)
            scope = (
                parent_fp.scope(ref.parent_table, ref.parent_field)
                if parent_fp is not None
                else f"{ref.parent_table}.{ref.parent_field}"
            )
            expected = pseudonymizer.pseudonym(scope, fk_value)
            masked_image = out.get("after") or out.get("before") or {}
            actual = masked_image.get(ref.child_field)
            if actual != expected:
                failures.append(
                    f"cross-version mismatch {ref.child_table}.{ref.child_field} "
                    f"at seq {event.commit_seq}"
                )

    # Invisible-field non-interference.
    stripped = [_strip_invisible(engine, e) for e in events]
    clone = engine.replay_clone()
    clone.process_all(stripped)
    if clone.output != engine.output:
        failures.append("invisible fields interfered with masked output")

    return ReplayReport(
        ok=not failures,
        failures=failures,
        checked_events=len(engine.journal),
        checked_references=checked_refs,
    )


def _strip_invisible(engine: Engine, event: CdcEvent) -> CdcEvent:
    policy = engine._history.at(event.commit_seq)
    if policy is None:
        return event

    def strip(image):
        if image is None:
            return None
        return {
            k: v
            for k, v in image.items()
            if not (
                policy.field_policy(event.table, k) is not None
                and policy.field_policy(event.table, k).strategy == "invisible"
            )
        }

    return CdcEvent(
        txid=event.txid,
        table=event.table,
        pk=strip(event.pk),
        before=strip(event.before),
        after=strip(event.after),
        commit_seq=event.commit_seq,
    )
