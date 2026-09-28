"""End-to-end runnable sample:

    python3.11 demo.py

Everything here operates on deterministic, artificial data only.
"""

from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor

from dp_synth import (
    BudgetExceededError,
    Condition,
    CountQuery,
    DPQueryEngine,
    HistogramQuery,
    Predicate,
    SumQuery,
    diagnose_laplace,
    make_synthetic_employee_data,
    true_answer,
)


def main() -> None:
    data = make_synthetic_employee_data(n=2000, seed=20260929)
    data2 = make_synthetic_employee_data(n=2001, seed=20260929)
    print("=" * 72)
    print("SYNTHETIC DATA ONLY")
    print(f"  v1: {data!r}")
    print(f"  v2: {data2!r}")
    assert data.version != data2.version

    engine = DPQueryEngine(total_epsilon_per_version=5.0, rng_seed=7)

    pro = Predicate((Condition("segment", "==", "pro"),))
    q1 = CountQuery(predicate=pro, epsilon=1.0)
    # Same conjunction with conditions stated in reverse construction style;
    # field/condition order is semantically irrelevant.
    q1_same = CountQuery(
        predicate=Predicate((Condition("segment", "==", "pro"),)), epsilon=1.0
    )

    print("=" * 72)
    print("1) EXACT REPEAT -> stored noisy output, zero extra epsilon")
    p1 = engine.run(q1, data)
    p1_again = engine.run(q1_same, data)
    truth1 = true_answer(q1, data)
    print(f"  true count(pro) = {truth1:.0f}")
    print(f"  publication #1 = {p1.value:.4f} (charged {p1.epsilon:g})")
    print(f"  repeat call    = {p1_again.value:.4f}  identical={p1.value == p1_again.value}")
    print(f"  remaining epsilon (v1) = {engine.remaining_budget(data.version):g}")

    print("=" * 72)
    print("2) THRESHOLD CHANGE -> different semantics, no reuse, new charge")
    q1_t = CountQuery(predicate=pro, epsilon=1.0, threshold=10.0)
    pt = engine.run(q1_t, data)
    print(f"  threshold=10 -> {pt.value:.4f} (fresh draw, new charge)")
    print(f"  remaining epsilon (v1) = {engine.remaining_budget(data.version):g}")

    print("=" * 72)
    print("3) EPSILON CHANGE -> different semantics, no reuse")
    q1_lo = CountQuery(predicate=pro, epsilon=0.5)
    plo = engine.run(q1_lo, data)
    print(f"  epsilon=0.5  -> {plo.value:.4f} (fresh charge of 0.5)")
    print(f"  remaining epsilon (v1) = {engine.remaining_budget(data.version):g}")

    print("=" * 72)
    print("4) DATA VERSION CHANGE -> separate ledger, separate cache")
    p_v2 = engine.run(q1, data2)
    print(f"  same query on v2 -> {p_v2.value:.4f}")
    print(f"  remaining epsilon (v1) = {engine.remaining_budget(data.version):g}")
    print(f"  remaining epsilon (v2) = {engine.remaining_budget(data2.version):g}")

    print("=" * 72)
    print("5) BOUNDED SUM + HISTOGRAM, then BUDGET EXHAUSTION")
    active_beijing = Predicate(
        (Condition("active", "==", True), Condition("city", "==", "Beijing"))
    )
    q_sum = SumQuery("salary", 0.0, 60_000.0, predicate=active_beijing, epsilon=1.0)
    ps = engine.run(q_sum, data)
    print(f"  noisy sum salary (active & Beijing) = {ps.value:,.2f}")
    print(f"  true  sum salary (active & Beijing) = {true_answer(q_sum, data):,.2f}")
    print(f"  remaining epsilon (v1) = {engine.remaining_budget(data.version):g}")

    q_hist = HistogramQuery("city", epsilon=1.0)
    ph = engine.run(q_hist, data)
    true_hist = true_answer(q_hist, data)
    print("  noisy histogram by city:")
    for city, value in sorted(ph.value.items()):
        print(f"    {city:<10} true={true_hist[city]:4d}  noisy={value:10.2f}")
    print(f"  remaining epsilon (v1) = {engine.remaining_budget(data.version):g}")

    one_more = CountQuery(predicate=Predicate((Condition("city", "==", "Hangzhou"),)), epsilon=1.0)
    try:
        engine.run(one_more, data)
    except BudgetExceededError as exc:
        print(f"  BudgetExceededError -> {exc}")
    print("  repeating an already-published query still works for free:")
    free = engine.run(q1_t, data)
    print(f"  threshold query reused -> {free.value:.4f}, remaining unchanged = "
          f"{engine.remaining_budget(data.version):g}")

    print("=" * 72)
    print("6) CONCURRENT IDENTICAL QUERIES -> one mechanism output, one charge")
    engine2 = DPQueryEngine(total_epsilon_per_version=4.0, rng_seed=7)
    qc = CountQuery(
        predicate=Predicate((Condition("city", "in", ["Beijing", "Shanghai"]),)),
        epsilon=1.0,
    )
    with ThreadPoolExecutor(max_workers=8) as pool:
        results = list(pool.map(lambda _: engine2.run(qc, data), range(8)))
    values = {r.value for r in results}
    print(f"  8 concurrent calls -> {len(values)} distinct noisy value(s): {values.pop():.4f}")
    print(f"  remaining epsilon = {engine2.remaining_budget(data.version):g}")

    print("=" * 72)
    print("7) STATISTICAL DIAGNOSTICS (not a formal privacy proof)")
    diag = diagnose_laplace(true_value=1000.0, epsilon=1.0, sensitivity=1.0, trials=20000)
    print("  " + diag.as_text().replace("\n", "\n  "))

    print("=" * 72)
    print("LEDGER v1")
    print(engine.ledger_report(data.version))
    print("LEDGER v2")
    print(engine.ledger_report(data2.version))
    print("LEDGER (concurrency engine)")
    print(engine2.ledger_report(data.version))

    print("=" * 72)
    print("8) NON-SYNTHETIC INPUT IS REJECTED")
    try:
        engine.run(q1, [{"segment": "pro"}])  # type: ignore[arg-type]
    except TypeError as exc:
        print(f"  TypeError -> {exc}")


if __name__ == "__main__":
    main()
