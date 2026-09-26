"""Independent ledger state machine checker.

Replays the public event log and cross-checks it against the query table
to verify, independently of the write path in ``budget_ledger``:

1. budget conservation: total == reserved + spent + available,
2. at-most-once publication identity per query ID,
3. legal state-machine transitions (RESERVE -> PUBLISH | CANCEL, terminal),
4. no budget refund after publication,
5. log hygiene: no secret/random state in the public event log.
"""

from __future__ import annotations

import json
import math
import sqlite3
from dataclasses import dataclass, field

#: Independent definition of the legal per-query event sequences:
#: exactly one RESERVE, then at most one terminal PUBLISH or CANCEL.
_ALLOWED_TRANSITIONS = {
    "START": {"RESERVE"},
    "RESERVE": {"PUBLISH", "CANCEL"},
    "PUBLISH": set(),
    "CANCEL": set(),
}

FORBIDDEN_LOG_KEYS = frozenset({"noise", "seed", "secret", "rng", "random_state"})


@dataclass
class CheckReport:
    violations: list[str] = field(default_factory=list)
    events_replayed: int = 0
    queries_checked: int = 0
    spent: float = 0.0
    reserved: float = 0.0
    available: float = 0.0

    @property
    def ok(self) -> bool:
        return not self.violations


class LedgerChecker:
    def __init__(self, tolerance: float = 1e-6):
        self.tolerance = tolerance

    def check(self, db_path) -> CheckReport:
        report = CheckReport()
        conn = sqlite3.connect(str(db_path))
        conn.row_factory = sqlite3.Row
        try:
            total = float(
                conn.execute(
                    "SELECT value FROM meta WHERE key = 'total_epsilon'"
                ).fetchone()[0]
            )
            events = conn.execute(
                "SELECT * FROM events ORDER BY id"
            ).fetchall()
            queries = {
                row["query_id"]: row
                for row in conn.execute("SELECT * FROM queries").fetchall()
            }
        finally:
            conn.close()

        report.events_replayed = len(events)
        report.queries_checked = len(queries)

        # -- log hygiene ------------------------------------------------
        for ev in events:
            details = json.loads(ev["details"])
            bad = FORBIDDEN_LOG_KEYS & set(details)
            if bad:
                report.violations.append(
                    f"event {ev['id']} leaks forbidden keys {sorted(bad)}"
                )

        # -- per-query state machine & at-most-once publication ---------
        per_query: dict[str, list[sqlite3.Row]] = {}
        for ev in events:
            per_query.setdefault(ev["query_id"], []).append(ev)

        for qid, evs in per_query.items():
            state = "START"
            for ev in evs:
                kind = ev["event"]
                if kind not in _ALLOWED_TRANSITIONS[state]:
                    report.violations.append(
                        f"query {qid!r}: illegal transition {state} -> {kind}"
                    )
                state = kind if kind in _ALLOWED_TRANSITIONS else state
            publishes = [e for e in evs if e["event"] == "PUBLISH"]
            if len(publishes) > 1:
                report.violations.append(
                    f"query {qid!r}: published {len(publishes)} times"
                    " (at-most-once violated)"
                )
            kinds = [e["event"] for e in evs]
            if "PUBLISH" in kinds and "CANCEL" in kinds:
                report.violations.append(
                    f"query {qid!r}: cancel after publish (refund attempt)"
                )

        # -- cross-check event log against the query table --------------
        for qid, evs in per_query.items():
            row = queries.get(qid)
            if row is None:
                report.violations.append(f"query {qid!r}: events without table row")
                continue
            kinds = [e["event"] for e in evs]
            if "PUBLISH" in kinds and row["status"] != "PUBLISHED":
                report.violations.append(
                    f"query {qid!r}: PUBLISH event but status={row['status']}"
                )
            if "CANCEL" in kinds and row["status"] != "CANCELLED":
                report.violations.append(
                    f"query {qid!r}: CANCEL event but status={row['status']}"
                )
            if "PUBLISH" not in kinds and row["status"] == "PUBLISHED":
                report.violations.append(
                    f"query {qid!r}: PUBLISHED without PUBLISH event"
                )

        # -- publication identity: unique sequence, single result -------
        published = [r for r in queries.values() if r["status"] == "PUBLISHED"]
        seqs = [r["publish_seq"] for r in published]
        if len(seqs) != len(set(seqs)):
            report.violations.append("duplicate publish_seq values")

        # -- budget conservation ----------------------------------------
        spent = sum(r["epsilon"] for r in published)
        reserved = sum(r["epsilon"] for r in queries.values()
                       if r["status"] == "RESERVED")
        cancelled_with_publish = [
            r["query_id"] for r in queries.values()
            if r["status"] == "CANCELLED"
            and any(e["event"] == "PUBLISH" for e in per_query.get(r["query_id"], []))
        ]
        for qid in cancelled_with_publish:
            report.violations.append(f"query {qid!r}: published then cancelled")
        available = total - spent - reserved
        report.spent, report.reserved, report.available = spent, reserved, available
        if available < -self.tolerance:
            report.violations.append(
                f"budget overdrawn: available={available}"
            )
        if not math.isclose(spent + reserved + available, total,
                            rel_tol=0, abs_tol=self.tolerance):
            report.violations.append("conservation identity broken")
        # Spent budget per the event log must match the query table.
        log_spent = sum(
            ev["epsilon"] for ev in events if ev["event"] == "PUBLISH"
        )
        if not math.isclose(log_spent, spent, rel_tol=0, abs_tol=self.tolerance):
            report.violations.append(
                f"event-log spend {log_spent} != table spend {spent}"
            )
        return report
