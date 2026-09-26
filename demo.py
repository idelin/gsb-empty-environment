"""Runnable demo of the recurrence library (python3 demo.py)."""

from datetime import datetime

from fixtures import make_test_table
from recurrence import (
    DailyRule,
    FoldPolicy,
    GapPolicy,
    MonthlyRule,
    RuleSet,
    WallClock,
    WeeklyRule,
)


def W(y, m, d, hh=0, mm=0):
    return WallClock(datetime(y, m, d, hh, mm))


def show(title, rows):
    print(f"\n== {title} ==")
    for row in rows:
        print(row)


def main():
    table = make_test_table()
    print(f"offset table version: {table.version}")

    # 1. Monthly 31st: short months skipped, instants not uniformly spaced.
    rs = RuleSet(table).add_rule(MonthlyRule(W(2026, 1, 31, 9, 0), monthdays={31}))
    pairs = list(rs.iter_instants(W(2026, 1, 1), W(2027, 1, 1)))
    show("monthly 31st (wall -> UTC instant, delta to previous)", [
        f"{w}  ->  {i}   (+{i.epoch_seconds - p}s)"
        if (p := (pairs[n - 1][1].epoch_seconds if n else i.epoch_seconds)) is not None
        else ""
        for n, (w, i) in enumerate(pairs)
    ])

    # 2. Union + intersection + difference.
    rs2 = RuleSet(table)
    rs2.add_rule(DailyRule(W(2026, 1, 1, 9, 0)))                 # every day
    rs2.add_intersect(WeeklyRule(W(2026, 1, 5, 9, 0), {0, 2, 4}))  # Mon/Wed/Fri only
    rs2.add_exclude_rule(MonthlyRule(W(2026, 1, 12, 9, 0), {12}))  # except the 12th
    rs2.add_rdate(W(2026, 1, 12, 9, 0))                          # ...but re-added
    rs2.add_exdate(W(2026, 1, 12, 9, 0))                         # ...exdate wins
    rs2.add_rdate(W(2026, 1, 3, 9, 0))                           # extra one-off
    show("daily ∩ Mon/Wed/Fri − 12th (+extra 1/3, exdate beats rdate on 1/12)",
         [str(w) for w in rs2.iter_wall(W(2026, 1, 1), W(2026, 1, 20))])

    # 3. Gap / fold policies on a daily 02:30 rule across the transitions.
    rs3 = RuleSet(table).add_rule(DailyRule(W(2026, 3, 27, 2, 30)))
    show("daily 02:30 across spring-forward gap (shift-forward policy)", [
        f"{w}  ->  {i}"
        for w, i in rs3.iter_instants(W(2026, 3, 27), W(2026, 3, 31),
                                      GapPolicy.SHIFT_FORWARD)
    ])
    show("daily 02:30 across fall-back fold (EARLIER vs LATER)", [
        f"earlier: {w} -> {i}"
        for w, i in rs3.iter_instants(W(2026, 10, 24), W(2026, 10, 27),
                                      fold_policy=FoldPolicy.EARLIER)
    ] + [
        f"later:   {w} -> {i}"
        for w, i in rs3.iter_instants(W(2026, 10, 24), W(2026, 10, 27),
                                      fold_policy=FoldPolicy.LATER)
    ])

    # 4. Large-span count via arithmetic jumps + kth lookup.
    big = DailyRule(W(2000, 1, 1, 8, 0))
    n = big.count_between(W(2000, 1, 1), W(4000, 1, 1))
    show("daily count 2000..4000 (no per-day scan)", [
        f"count = {n}", f"meta  = {big.last_meta}",
        f"kth(500000) = {big.kth(500_000)}",
    ])

    # 5. Exclusions affect count and rank.
    rs5 = RuleSet(table).add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
    rs5.add_exdate(W(2026, 1, 3, 9, 0))
    show("exdate shifts rank", [
        f"count Jan-2026 = {rs5.count_between(W(2026, 1, 1), W(2026, 2, 1))}",
        f"2nd occurrence (k=2) = {rs5.kth(2, W(2026, 1, 1))}",
    ])

    # 6. Cursor pagination bound to ruleset + table version.
    rs6 = RuleSet(table).add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
    cursor, page_no = None, 1
    rows = []
    while True:
        items, cursor = rs6.page(W(2026, 1, 1), W(2026, 2, 1), 10, cursor)
        rows.append(f"page {page_no}: {items[0][0].dt.date()} .. "
                    f"{items[-1][0].dt.date()}  ({len(items)} items)"
                    + (f"  next={cursor.encode()[:32]}..." if cursor else "  (last)"))
        if cursor is None:
            break
        page_no += 1
    show("paged iteration, limit=10", rows)


if __name__ == "__main__":
    main()
