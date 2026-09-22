"""Demo: null-rejection analysis drives safe rewrites on synthetic bags."""

from relalg import Col, Cmp, IsNull, Lit, format_table, left_join, select
from rewrites import (
    Filter,
    Join,
    PreconditionError,
    Scan,
    execute,
    is_null_rejecting,
    outer_to_inner,
    pushdown_after_conversion,
    pushdown_filter,
    render,
)
from test_rewrites import JOIN_PRED, L, R, X_GT_3, X_IS_NULL


def show(title, rel):
    print(f"\n== {title} ==")
    print(format_table(rel))


def main():
    show("L (left, preserved side)", L)
    show("R (right, null-supplying side)", R)

    print("\n-- null-rejection analysis on R's columns --")
    for name, pred in [("x > 3", X_GT_3), ("x IS NULL", X_IS_NULL)]:
        print(f"  {name:12} null-rejecting on {{rk, x}}: "
              f"{is_null_rejecting(pred, R.columns)}")

    print("\n-- R3: sigma[x > 3](L LEFT JOIN R) --")
    plan = Filter(X_GT_3, Join("left", Scan(L), Scan(R), JOIN_PRED))
    print(f"  original : {render(plan)}")
    converted = outer_to_inner(plan)
    print(f"  R3       : {render(converted)}")
    pushed = pushdown_after_conversion(plan)
    print(f"  R3+R4    : {render(pushed)}")
    show("result of original plan", execute(plan))
    show("result of rewritten plan (identical bag)", execute(pushed))

    print("\n-- R2 refusal: sigma[x IS NULL](L LEFT JOIN R) --")
    bad = Filter(X_IS_NULL, Join("left", Scan(L), Scan(R), JOIN_PRED))
    for rule, fn in [("pushdown to right side", pushdown_filter),
                     ("outer-to-inner", outer_to_inner)]:
        try:
            fn(bad)
            print(f"  {rule}: UNEXPECTEDLY ALLOWED")
        except PreconditionError as exc:
            print(f"  {rule}: refused -> {exc}")

    correct = select(left_join(L, R, JOIN_PRED), X_IS_NULL)
    naive = left_join(L, select(R, X_IS_NULL), JOIN_PRED)
    show("counterexample: correct sigma[x IS NULL](L LEFT JOIN R)", correct)
    show("counterexample: naive L LEFT JOIN sigma[x IS NULL](R)", naive)


if __name__ == "__main__":
    main()
