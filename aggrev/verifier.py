"""Independent verification: recompute the aggregate from the raw change
events and cross-check both sides, without trusting their internal logic.

Checks:
- recomputed aggregate == publisher value == downstream value;
- before-image integrity per source (catches out-of-order / lost events and
  broken retract->re-add sequences);
- downstream applied set is ancestor-closed (no silent gaps) and acyclic;
- every applied revision is acknowledged exactly once, checkpoints never
  regress, and cyclically-rejected revisions are not applied;
- publisher invariant: base snapshot + live revision deltas == current value.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .models import ChangeEvent, GENESIS_VERSION


@dataclass
class VerificationReport:
    ok: bool
    problems: list[str] = field(default_factory=list)
    expected: dict = field(default_factory=dict)
    publisher_value: dict = field(default_factory=dict)
    downstream_value: dict = field(default_factory=dict)


def recompute(events: list[ChangeEvent]) -> tuple[dict[str, int], list[str]]:
    """Fold raw contributions per source in commit-seq order into row state,
    then sum live rows per table. Before images are validated, so a missing
    or mis-ordered event (e.g. retract before re-add) is reported."""
    problems: list[str] = []
    by_source: dict[str, list[ChangeEvent]] = {}
    for event in events:
        by_source.setdefault(event.source, []).append(event)

    rows: dict[tuple[str, str], int] = {}
    for source, source_events in sorted(by_source.items()):
        ordered = sorted(source_events, key=lambda e: e.commit_seq)
        seqs = [e.commit_seq for e in ordered]
        if len(set(seqs)) != len(seqs):
            problems.append(f"{source}: duplicate commit_seq values {sorted(seqs)}")
        for event in ordered:
            key = (event.table, event.pk)
            current = rows.get(key)
            before_amt = (event.before or {}).get("amount")
            if event.op == "insert":
                if current is not None:
                    problems.append(
                        f"{source}@{event.commit_seq}: insert of existing row {key}"
                    )
                rows[key] = (event.after or {}).get("amount", 0)
            elif event.op in ("update", "delete"):
                if current is None or current != before_amt:
                    problems.append(
                        f"{source}@{event.commit_seq}: before-image mismatch on {key} "
                        f"(state={current}, before={before_amt})"
                    )
                if event.op == "update":
                    rows[key] = (event.after or {}).get("amount", 0)
                else:
                    rows.pop(key, None)
            else:
                problems.append(f"{source}@{event.commit_seq}: unknown op {event.op}")

    totals: dict[str, int] = {}
    for (table, _pk), amount in rows.items():
        totals[table] = totals.get(table, 0) + amount
    return totals, problems


def _find_dag_cycles(versions: dict) -> list[str]:
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {vid: WHITE for vid in versions}
    problems: list[str] = []

    def visit(vid: str, trail: list[str]) -> None:
        color[vid] = GRAY
        for parent in versions[vid]["parents"]:
            if parent not in versions:
                continue
            if color[parent] == GRAY:
                problems.append(f"cycle in version DAG: {' -> '.join(trail + [parent])}")
            elif color[parent] == WHITE:
                visit(parent, trail + [parent])
        color[vid] = BLACK

    for vid in versions:
        if color[vid] == WHITE:
            visit(vid, [vid])
    return problems


def verify(
    events: list[ChangeEvent],
    publisher,
    downstream,
    expect_complete: bool = True,
) -> VerificationReport:
    problems: list[str] = []
    expected, rec_problems = recompute(events)
    problems.extend(rec_problems)

    pub_state = publisher.store.state
    ds_state = downstream.store.state
    pub_value = dict(pub_state["value"])
    ds_value = dict(ds_state["value"])

    if pub_value != expected:
        problems.append(f"publisher value {pub_value} != recomputed {expected}")
    if ds_value != expected:
        problems.append(f"downstream value {ds_value} != recomputed {expected}")

    folded = dict(pub_state["base"])
    for rev in pub_state["revisions"].values():
        for table, delta in rev["correction"]["deltas"].items():
            folded[table] = folded.get(table, 0) + delta
    if folded != pub_value:
        problems.append(
            f"publisher invariant broken: base+live deltas {folded} != value {pub_value}"
        )

    versions = ds_state["versions"]
    for vid, node in versions.items():
        for parent in node["parents"]:
            if parent not in versions:
                problems.append(f"downstream gap: {vid} misses parent {parent}")
    problems.extend(_find_dag_cycles(versions))

    for rid in ds_state["applied"]:
        if rid not in ds_state["acks"]:
            problems.append(f"revision {rid} applied without acknowledgement")
    for rid, ack in ds_state["acks"].items():
        for source, seq in ack["checkpoints"].items():
            if ds_state["checkpoints"].get(source, 0) < seq:
                problems.append(
                    f"checkpoint for {source} regressed below acked {seq} (revision {rid})"
                )
    for rid in ds_state["rejected"]:
        if rid in ds_state["applied"]:
            problems.append(f"rejected revision {rid} was applied anyway")

    if expect_complete:
        max_seq: dict[str, int] = {}
        for event in events:
            max_seq[event.source] = max(max_seq.get(event.source, 0), event.commit_seq)
        for source, seq in sorted(max_seq.items()):
            if ds_state["checkpoints"].get(source) != seq:
                problems.append(
                    f"downstream checkpoint for {source} is "
                    f"{ds_state['checkpoints'].get(source)}, expected {seq}"
                )
            if pub_state["checkpoints"].get(source) != seq:
                problems.append(
                    f"publisher checkpoint for {source} is "
                    f"{pub_state['checkpoints'].get(source)}, expected {seq}"
                )

    return VerificationReport(
        ok=not problems,
        problems=problems,
        expected=expected,
        publisher_value=pub_value,
        downstream_value=ds_value,
    )
