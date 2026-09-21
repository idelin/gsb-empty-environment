"""Sample downstream consumer of revision reports.

``DecisionMirror`` keeps a downstream replica of the replayed decisions.
It loads the initial replay in bulk and afterwards applies *only* the
``changed_range`` of each ``RevisionReport`` -- everything outside that
range kept its identity (same values, same version), so re-reading it
would be wasted work.  ``verify()`` re-reads the engine's full state and
proves the incremental application stayed consistent.

Run the demo:  python3 revision_consumer.py
"""

from __future__ import annotations

from token_bucket import ReplayEngine


class DecisionMirror:
    """A downstream store updated exclusively through RevisionReports."""

    def __init__(self) -> None:
        # request_id -> (allowed, reason, bucket_before, bucket_after, version)
        self.rows = {}
        self.applied_revisions = []

    def load(self, decisions) -> None:
        for d in decisions:
            self.rows[d.request_id] = (
                d.allowed, d.reason, d.bucket_before, d.bucket_after, d.version,
            )

    def apply(self, engine: ReplayEngine, report) -> list:
        """Apply one revision; returns the request ids actually rewritten."""
        rewritten = []
        if report.changed_range is not None:
            lo, hi = report.changed_range
            decisions = engine.decisions
            for idx in range(lo, hi + 1):
                d = decisions[idx]
                row = (d.allowed, d.reason, d.bucket_before, d.bucket_after, d.version)
                if self.rows.get(d.request_id) != row:
                    self.rows[d.request_id] = row
                    rewritten.append(d.request_id)
        self.applied_revisions.append(report)
        return rewritten

    def verify(self, engine: ReplayEngine) -> None:
        """Full-state cross-check against the engine (not just the bucket)."""
        expected = {
            d.request_id: (d.allowed, d.reason, d.bucket_before, d.bucket_after, d.version)
            for d in engine.decisions
        }
        assert self.rows == expected, "mirror diverged from engine state"


def _show(title, engine, report=None, rewritten=()):
    print(f"\n== {title} ==")
    if report is not None:
        print(
            f"revision={report.revision} kind={report.kind} "
            f"recomputed={report.recomputed_range} changed={report.changed_range} "
            f"ids={list(report.changed_request_ids)}"
        )
        print(f"mirror rewrote: {list(rewritten)}")
    for d in engine.decisions:
        mark = "ALLOW " if d.allowed else "REJECT"
        print(
            f"  {d.request_id:>2} t={str(d.time):>4} {mark} {d.reason:<19}"
            f" bucket {d.bucket_before} -> {d.bucket_after}  v{d.version}"
        )


def main() -> None:
    engine = ReplayEngine(
        segments=[(0, 2, 1)],  # capacity 2, refill 1 token/ms from t=0
        requests=[(0, 1, "a"), (0, 2, "b"), (1, 1, "c"), (2, 1, "d"), (3, 1, "e")],
    )
    mirror = DecisionMirror()
    mirror.load(engine.decisions)
    _show("initial replay (v1)", engine)

    # 1) A historical request arrives late, at t=0 ahead of everything.
    report = engine.insert_request(0, 0, "x")
    rewritten = mirror.apply(engine, report)
    mirror.verify(engine)
    _show("after backfilling x at t=0", engine, report, rewritten)
    # Recomputation stops at c: its record reconverged (bucket refilled to
    # exactly 1 token during the 1ms gap), so c/d/e keep version 1.

    # 2) Capacity shrinks to 1 from t=2 on.  The clamp changes nothing here
    # (the bucket never holds more than 1 token after t=2 anyway), but the
    # edit touches the open-ended final segment, so no early stop is legal
    # and recomputation runs to the end of the log.
    report = engine.update_config([(0, 2, 1), (2, 1, 1)])
    rewritten = mirror.apply(engine, report)
    mirror.verify(engine)
    _show("after shrinking capacity to 1 from t=2", engine, report, rewritten)

    # 3) From t=2 on, capacity is restored to 2 and the rate doubles.
    #    d still recomputes identically (its refill happens before the new
    #    segment can add anything), e is the only decision that changes.
    report = engine.update_config([(0, 2, 1), (2, 2, 2)])
    rewritten = mirror.apply(engine, report)
    mirror.verify(engine)
    _show("after doubling refill rate from t=2", engine, report, rewritten)

    print("\nmirror consistent with engine after every revision.")


if __name__ == "__main__":
    main()
