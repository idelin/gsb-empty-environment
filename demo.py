"""Runnable sample: reservation, commit, cancel, crash-retry, checking.

Synthetic data only; the statistical summary at the end is a sanity check,
not a formal privacy proof.
"""

import math
import tempfile
from pathlib import Path

from budget_ledger import (
    BudgetLedger,
    ConflictError,
    FaultInjectedError,
    InvalidStateError,
)
from ledger_checker import LedgerChecker


def main():
    tmp = tempfile.mkdtemp(prefix="budget-demo-")
    db = Path(tmp) / "ledger.db"
    ledger = BudgetLedger(db, total_epsilon=1.0, sensitivity=1.0)
    print(f"ledger file: {db}")

    # Synthetic dataset: 100 rows, 47 of them flagged.
    true_count = 47.0

    print("\n-- reserve + commit (atomic: noise, publish, budget deduction)")
    ledger.reserve("q-count", {"sql": "SELECT COUNT(*) FROM t WHERE flag"}, 0.4)
    pub = ledger.commit("q-count", true_value=true_count)
    print(f"published result={pub.result:.4f} (true={true_count}, seq={pub.publish_seq})")

    print("\n-- idempotent retry reuses the original noise and budget record")
    again = ledger.commit("q-count", true_value=true_count)
    print(f"retry result={again.result:.4f} noise_reused={again.noise_reused}")

    print("\n-- same ID with different content conflicts")
    try:
        ledger.reserve("q-count", {"sql": "SELECT SUM(x) FROM t"}, 0.1)
    except ConflictError as exc:
        print(f"ConflictError: {exc}")

    print("\n-- cancel before publish refunds; after publish it must not")
    ledger.reserve("q-sum", {"sql": "SELECT SUM(x) FROM t"}, 0.3)
    ledger.cancel("q-sum")
    print("q-sum cancelled, reservation refunded")
    try:
        ledger.cancel("q-count")
    except InvalidStateError as exc:
        print(f"InvalidStateError: {exc}")

    print("\n-- crash after durable publish, then retry on a reopened ledger")
    ledger.reserve("q-mean", {"sql": "SELECT AVG(x) FROM t"}, 0.2)
    try:
        ledger.commit("q-mean", true_value=0.5, fault_at="after_tx_commit")
    except FaultInjectedError as exc:
        print(f"simulated crash: {exc}")
    recovered = BudgetLedger(db, total_epsilon=1.0)
    pub2 = recovered.commit("q-mean", true_value=0.5)
    print(f"recovered result={pub2.result:.4f} noise_reused={pub2.noise_reused}")

    snap = recovered.snapshot()
    print("\n-- budget snapshot (explicit epsilon-sum composition)")
    print(f"total={snap.total} reserved={snap.reserved} "
          f"spent={snap.spent} available={snap.available:.4f}")
    print(f"composition bound (sum of published epsilons) = "
          f"{recovered.composition_bound()}")

    print("\n-- independent ledger checker")
    report = LedgerChecker().check(db)
    print(f"events_replayed={report.events_replayed} "
          f"queries_checked={report.queries_checked} ok={report.ok}")
    assert report.ok, report.violations

    print("\n-- statistical sanity check (NOT a formal privacy proof)")
    stats_db = Path(tmp) / "stats.db"
    stats = BudgetLedger(stats_db, total_epsilon=1e9, sensitivity=1.0)
    eps = 0.5
    samples = []
    for i in range(2000):
        stats.reserve(f"s{i}", {"i": i}, eps)
        samples.append(stats.commit(f"s{i}", true_value=0.0).result)
    mean = sum(samples) / len(samples)
    var = sum((s - mean) ** 2 for s in samples) / len(samples)
    theory = 2.0 / eps**2  # Var of Laplace(0, sensitivity/epsilon)
    print(f"n={len(samples)} sample_mean={mean:+.4f} "
          f"sample_var={var:.3f} theoretical_var={theory:.3f}")


if __name__ == "__main__":
    main()
