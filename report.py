"""Space-usage and batch-expiry-cost report for SlidingQuantileWindow."""

import gc
import time
import tracemalloc

from quantile_window import NaiveQuantileOracle, SlidingQuantileWindow


def build(n):
    w = SlidingQuantileWindow()
    for i in range(n):
        w.ingest("src", i, time=i, value=(i * 2654435761) % 1_000_000)
    return w


def space_report():
    print("== Space usage (tracemalloc, structure only) ==")
    for n in (10_000, 50_000, 100_000, 200_000):
        gc.collect()
        tracemalloc.start()
        w = build(n)
        current, _peak = tracemalloc.get_traced_memory()
        tracemalloc.stop()
        print(f"  n={n:>7,}: {current / 2**20:7.2f} MiB total, "
              f"{current / n:6.1f} bytes/event "
              f"(treap node + dict entry + heap entry)")
        del w


def expiry_report():
    print("\n== Batch expiry cost ==")
    n = 200_000
    w = build(n)
    # one big batch: evict half the window in a single advance()
    t0 = time.perf_counter()
    evicted = w.advance(n // 2)
    dt = time.perf_counter() - t0
    print(f"  single batch: evicted {evicted:,} of {n:,} in {dt * 1e3:7.2f} ms "
          f"({dt / evicted * 1e9:6.0f} ns/event, O(k log n))")

    # continuous watermark advancement: many small steps
    w = build(n)
    steps = 1_000
    t0 = time.perf_counter()
    total = 0
    for step in range(1, steps + 1):
        total += w.advance(step * (n // steps))
    dt = time.perf_counter() - t0
    print(f"  continuous:   {steps:,} advances evicted {total:,} in {dt * 1e3:7.2f} ms "
          f"({dt / steps * 1e6:6.1f} us/advance) -- no full re-sort per step")

    # contrast: small expiries + a query per step; the oracle re-sorts
    # ~n elements every step while the treap pays O(log n) per query.
    n2, steps2, per_step = 100_000, 200, 10
    o = NaiveQuantileOracle()
    for i in range(n2):
        o.ingest("src", i, time=i, value=(i * 2654435761) % 1_000_000)
    t0 = time.perf_counter()
    for step in range(steps2):
        o.advance(step * per_step)
        o.quantile(0.5)
    naive_dt = time.perf_counter() - t0
    w2 = build(n2)
    t0 = time.perf_counter()
    for step in range(steps2):
        w2.advance(step * per_step)
        w2.quantile(0.5)
    fast_dt = time.perf_counter() - t0
    print(f"  advance+query x{steps2} on n={n2:,} (evict {per_step}/step): "
          f"treap {fast_dt * 1e3:6.2f} ms vs re-sorting oracle "
          f"{naive_dt * 1e3:7.2f} ms ({naive_dt / max(fast_dt, 1e-9):5.1f}x)")


if __name__ == "__main__":
    space_report()
    expiry_report()
