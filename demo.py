"""Run example for the multi-resolution index (stdlib only)."""

import random
import time
from fractions import Fraction

from fixtures import RATES, impulse, noise, sine_wave, with_dropouts
from mrindex import MultiResIndex, TOLERANCE


def main():
    print("=== fixtures (local arrays, no audio device) ===")
    rate = RATES["cd"]
    n = 100_000
    samples = sine_wave(n, freq=440.0, rate=rate)
    samples = with_dropouts(samples, [10, 20_000, 77_777])
    print(f"fixture: 440 Hz sine, n={n}, rate={rate} Hz, 3 dropouts")

    t0 = time.perf_counter()
    idx = MultiResIndex(samples, sample_rate=rate)
    build_ms = (time.perf_counter() - t0) * 1e3
    print(f"build: {build_ms:.1f} ms, nodes={idx.node_count()}, "
          f"copies={idx.last_op.copies}")

    print("\n=== full-range summary (count/missing/boundaries) ===")
    s = idx.summary()
    print(f"count={s.count} missing={s.missing} min={s.minimum:.6f} "
          f"max={s.maximum:.6f}")
    print(f"energy={s.energy:.6f} mean={s.mean:.6e} rms={s.rms:.6f}")
    print(f"bounds=[{s.first_index}, {s.last_index}] "
          f"covers_full_series={s.covers_full_series} "
          f"visits={idx.last_op.visits}")

    print("\n=== peaks: all tied source ranges ===")
    tied = [0.0, 5.0, 1.0, 5.0, 5.0, -2.0, 5.0, 0.0]
    tidx = MultiResIndex(tied)
    value, ranges = tidx.peaks()
    print(f"samples={tied}")
    print(f"max={value} tied source ranges={ranges}")
    value, ranges = tidx.peaks(kind="min")
    print(f"min={value} tied source ranges={ranges}")

    print("\n=== non-aligned query excludes out-of-range peaks ===")
    sp = [0.0] * 33
    sp[9] = 100.0   # just before the window
    sp[20] = 100.0  # just after the window
    sp[15] = 3.0
    sidx = MultiResIndex(sp)
    q = sidx.summary(10, 20)
    pv, pr = sidx.peaks(10, 20)
    print(f"window [10, 20): max={q.maximum} (spikes at 9 and 20 excluded), "
          f"peak ranges={pr}, visits={sidx.last_op.visits}")
    e = sidx.summary(7, 7)
    pv, pr = sidx.peaks(7, 7)
    print(f"empty [7, 7): count={e.count} min={e.minimum} max={e.maximum} "
          f"energy={e.energy} peak={pv} ranges={pr}")

    print("\n=== single-sample spike visible at every level ===")
    imp = impulse(257, at=123, height=50.0)
    iidx = MultiResIndex(imp)
    width = 1
    while width <= 512:
        bmax = max(b.maximum for b in iidx.buckets(0, 257, width))
        print(f"  bucket width {width:>4}: max over buckets = {bmax}")
        width *= 2

    print("\n=== grid-aligned buckets: partial edges not treated as full ===")
    for b in idx.buckets(3, 19, 8):
        print(f"  [{b.start:>2},{b.end:>2}) coverage={b.coverage} "
              f"partial={b.partial} max={b.maximum:.6f} energy={b.energy:.6f}")

    print("\n=== versioned replace / insert / cut / undo ===")
    v0 = idx.to_list()[:5]
    idx.replace(100, 110, [9.0] * 10)
    print(f"replace [100,110) -> version={idx.version} "
          f"copies={idx.last_op.copies}")
    idx.insert(50, [1.0, 2.0, 3.0])
    print(f"insert 3 @50      -> version={idx.version} "
          f"copies={idx.last_op.copies}")
    idx.cut(0, 7)
    print(f"cut [0,7)         -> version={idx.version} "
          f"copies={idx.last_op.copies}")
    while idx.undo():
        pass
    print(f"undo x3           -> version={idx.version} "
          f"restored head={idx.to_list()[:5] == v0}")

    print("\n=== rational sample-rate change 44100 -> 48000 (160/147) ===")
    rs = idx.change_rate(160, 147)
    print(f"in: n={idx.size} rate={idx.sample_rate}; "
          f"out: n={rs.size} rate={rs.sample_rate}")

    print("\n=== measured latency (this machine) ===")
    rng = random.Random(7)
    t0 = time.perf_counter()
    for _ in range(2000):
        a = rng.randrange(0, n - 100)
        idx.summary(a, a + 100)
    q_us = (time.perf_counter() - t0) / 2000 * 1e6
    visits = idx.last_op.visits
    t0 = time.perf_counter()
    copies = 0
    for _ in range(500):
        a = rng.randrange(0, n - 10)
        idx.replace(a, a + 10, noise(10, seed=a))
        copies += idx.last_op.copies
    e_us = (time.perf_counter() - t0) / 500 * 1e6
    print(f"range query (100 samples of {n}): {q_us:.1f} us, "
          f"last visits={visits}")
    print(f"replace 10 samples: {e_us:.1f} us, avg copies={copies / 500:.1f} "
          f"(tree has ~{idx.node_count()} nodes)")

    print("\n=== declarations ===")
    print("latency : query O(log n), edit O(log n + k) copies, undo O(1)")
    print(f"tolerance: min/max/count exact; energy/mean/rms rel {TOLERANCE}; "
          f"resample abs {TOLERANCE}")


if __name__ == "__main__":
    main()
