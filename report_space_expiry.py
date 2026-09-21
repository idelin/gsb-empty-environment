"""Space-usage and batch-expiry cost report for ExactQuantileWindow."""

import random
import time

from exact_quantile_window import ExactQuantileWindow


def main() -> None:
    rng = random.Random(42)
    n_events = 200_000
    window = 10_000.0          # ts step is 1.0 -> ~10k events live at steady state

    w = ExactQuantileWindow(window=window)

    t0 = time.perf_counter()
    for i in range(n_events):
        w.ingest("src", i, ts=float(i), value=rng.random())
    t_ingest = time.perf_counter() - t0

    # ---- steady-state measurements (window full) ----
    rep = w.space_report()

    t0 = time.perf_counter()
    hits = 0
    n_queries = 20_000
    for _ in range(n_queries):
        hits += w.query(rng.random()) is not None
    t_query = (time.perf_counter() - t0) / n_queries * 1e9

    t0 = time.perf_counter()
    retracted = 0
    for i in range(n_events - 5_000, n_events):   # all still live
        retracted += w.retract("src", i)
    t_retract = (time.perf_counter() - t0) / retracted * 1e9

    # ---- batch expiry: refill the window, then advance in varied batches ----
    mark = w.right
    seq = n_events
    for _ in range(30_000):                        # ts inside current window
        w.ingest("src", seq, ts=mark - rng.random() * window * 0.9,
                 value=rng.random())
        seq += 1
    live_before = len(w)

    expiry_rows = []
    for step in [1, 100, 1_000, 5_000, 20_000]:
        mark += step
        t0 = time.perf_counter()
        evicted = w.advance(mark)
        dt = time.perf_counter() - t0
        per = dt * 1e9 / evicted if evicted else 0.0
        expiry_rows.append((step, evicted, dt * 1e6, per))

    print(f"ingest                 : {n_events:,} events in {t_ingest:.2f}s "
          f"({t_ingest / n_events * 1e9:,.0f} ns/op, incremental expiry included)")
    print(f"query (exact, any q)   : {t_query:,.0f} ns/op  (n={rep['live_events']:,} live)")
    print(f"retract by identity    : {t_retract:,.0f} ns/op  ({retracted:,} live retractions)")
    print()
    print(f"batch expiry (advance watermark, {live_before:,} live before):")
    print(f"  {'watermark step':>14} {'evicted':>8} {'total us':>10} {'ns/event':>10}")
    for step, evicted, total_us, per in expiry_rows:
        print(f"  {step:>14,} {evicted:>8,} {total_us:>10.1f} {per:>10.0f}")
    print("  -> total cost tracks evicted count: O(k log n), no full-window rescan")
    print()
    print("space report at steady state (maintenance structures):")
    for k, v in rep.items():
        if k.endswith("_bytes") or k == "total_bytes_estimate":
            print(f"  {k:<24}: {v:>14,} B")
        elif isinstance(v, float):
            print(f"  {k:<24}: {v:>14.1f}")
        else:
            print(f"  {k:<24}: {v:>14,}")
    print()
    print(f"totals: ingested={w.total_ingested:,} expired={w.total_expired:,} "
          f"retracted={w.total_retracted:,}")


if __name__ == "__main__":
    main()
