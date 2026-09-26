"""Run sample: synthetic-data-only private count/sum with cached releases.

Usage:  python3.11 demo.py
"""

from dpquery import (
    BudgetExhaustedError,
    Predicate,
    PrivateQueryEngine,
    Query,
    make_synthetic_dataset,
    make_version_variant,
)


def show(title, release, note=""):
    print(f"  {title:<42} value={release.value:10.2f} "
          f"reused={str(release.reused):<5} spent={release.spent_total:.2f} {note}")


def main() -> None:
    ds = make_synthetic_dataset(n=400, seed=20260927)
    print(f"dataset={ds.name} version={ds.version} records={len(ds.records)}")
    engine = PrivateQueryEngine(ds, total_epsilon=3.0, seed=20260927)

    adult_beijing = Query(kind="count", predicates=(
        Predicate("age", ">=", 18), Predicate("city", "==", "Beijing")))
    adult_beijing_swap = Query(kind="count", predicates=(
        Predicate("city", "==", "Beijing"), Predicate("age", ">=", 18)))
    adult_shanghai_threshold = Query(kind="count", predicates=(
        Predicate("age", ">=", 30), Predicate("city", "==", "Beijing")))
    income_sum = Query(kind="sum", column="income",
                       predicates=(Predicate("active", "==", True),),
                       bounds=(0.0, 50_000.0))

    print("--- fresh release (charged 0.5) ---")
    show("count(age>=18 & city=Beijing)", engine.release(adult_beijing, 0.5))
    print("--- identical canonical spec, fields reordered: cache hit, 0 charge ---")
    show("count(city=Beijing & age>=18)", engine.release(adult_beijing_swap, 0.5),
         "(same value)")
    print("--- threshold 18 -> 30: spec changed, charged again ---")
    show("count(age>=30 & city=Beijing)", engine.release(adult_shanghai_threshold, 0.5))
    print("--- bounded sum query, fresh charge 0.5 ---")
    show("sum(income | active, clip[0,50k])", engine.release(income_sum, 0.5))

    print("--- new data version (n=401, new seed): same query is a cache miss ---")
    ds2 = make_version_variant()
    engine2 = PrivateQueryEngine(ds2, total_epsilon=3.0, seed=20260927)
    print(f"dataset={ds2.name} version={ds2.version} records={len(ds2.records)}")
    show("count(age>=18 & city=Beijing) @ v2", engine2.release(adult_beijing, 0.5))

    print("--- budget exhaustion: 1.5 left, new query wants 2.0 ---")
    big = Query(kind="count", predicates=(Predicate("plan", "==", "A"),))
    try:
        engine.release(big, 2.0)
    except BudgetExhaustedError as exc:
        print(f"  rejected: {exc}")
    print("--- cached result still served for free after exhaustion ---")
    show("count(age>=18 & city=Beijing) cached", engine.release(adult_beijing, 0.5))


if __name__ == "__main__":
    main()
