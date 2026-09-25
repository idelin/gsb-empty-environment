"""Replayable end-to-end scenarios for the polyphase resampler.

Run: python3 demo.py
Deterministic (seeded fixtures); exits non-zero if any scenario fails.
"""

from __future__ import annotations

import sys
from fractions import Fraction

import fixtures
from resampler import FLAG_CONTAMINATED, FLAG_TAIL, Resampler, SharedClock
from test_resampler import direct_reference, run_all


def scenario_impulse():
    """Impulse response vs direct convolution reference."""
    L, M, taps, n = 3, 2, 25, 64
    h = fixtures.lowpass_kernel(taps, L, M)
    r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
    outs = run_all(r, [(fixtures.impulse(n), ())])
    ref = direct_reference(fixtures.impulse(n), h, L, M, len(outs))
    err = max(abs(o.value - v) for o, v in zip(outs, ref))
    print(f"  outputs={len(outs)}  max|err| vs direct reference = {err:.3e}")
    print(f"  declared latency = {r.latency_samples} source samples "
          f"= {float(r.latency_seconds) * 1e3:.4f} ms @ {fixtures.FS_AUDIO} Hz")
    assert err < 1e-12


def scenario_ratio_switch():
    """Runtime ratio switch at a non-integer phase, continuous time base."""
    h2 = fixtures.lowpass_kernel(33, 2, 1)
    h3 = fixtures.lowpass_kernel(25, 3, 2)
    r = Resampler(h2, 2, 1, fs_in=fixtures.FS_VOICE)
    r.set_ratio(3, 2, at_source_index=Fraction(21, 2), kernel=h3)
    x = fixtures.tone_samples(120, fixtures.FS_VOICE, [440.0, 1200.0])
    outs = run_all(r, [(x, ())])
    times = [o.time for o in outs]
    pos = [o.source_position for o in outs]
    mono = all(a < b for a, b in zip(times, times[1:]))
    anchored = Fraction(21, 2) in pos
    print(f"  switch 2/1 -> 3/2 at source position 10.5: "
          f"{len(outs)} outputs, strictly increasing times: {mono}, "
          f"grid anchored on switch point: {anchored}")
    around = [o for o in outs
              if Fraction(9) < o.source_position < Fraction(13)]
    for o in around:
        print(f"    pos={float(o.source_position):7.3f}  "
              f"t={float(o.time) * 1e3:9.4f} ms  y={o.value:+.5f}")
    assert mono and anchored


def scenario_chunking_checkpoint():
    """Random/empty/short chunks + tail checkpoint restore == one-shot."""
    L, M, taps = 3, 2, 25
    h = fixtures.lowpass_kernel(taps, L, M)
    x = fixtures.tone_samples(500, fixtures.FS_AUDIO, [1000.0, 5000.0])
    ref = [(o.time, o.value, o.flags)
           for o in run_all(Resampler(h, L, M, fs_in=fixtures.FS_AUDIO),
                            [(x, ())])]
    sizes = fixtures.random_chunk_sizes(len(x), seed=1)
    r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
    outs, off = [], 0
    for s in sizes:
        outs.extend(r.process(x[off:off + s]))
        off += s
    outs.extend(r.flush(limit=3))            # checkpoint inside the tail
    r2 = Resampler.restore(r.checkpoint())
    while not r2.done:
        outs.extend(r2.flush(limit=2))
    got = [(o.time, o.value, o.flags) for o in outs]
    print(f"  chunk sizes (incl. empty/short): {sizes[:10]}... "
          f"-> {len(got)} outputs, bit-exact vs one-shot: {got == ref}")
    assert got == ref


def scenario_gaps():
    """Gap marking: zero-filled, contaminated output range reported."""
    L, M, taps = 2, 1, 17
    h = fixtures.lowpass_kernel(taps, L, M)
    x = fixtures.tone_samples(120, fixtures.FS_HIRES, [800.0])
    r = Resampler(h, L, M, fs_in=fixtures.FS_HIRES)
    outs = run_all(r, [(x[:40], ()), (x[40:80], [(50, 10)]), (x[80:], ())])
    flagged = [o for o in outs if o.flags & FLAG_CONTAMINATED]
    rng = r.contaminated_ranges[0]
    print(f"  gap at source samples [50, 60): {len(flagged)} outputs flagged, "
          f"contaminated time range "
          f"[{float(rng[0]) * 1e3:.4f}, {float(rng[1]) * 1e3:.4f}] ms")
    assert flagged and len(r.contaminated_ranges) == 1


def scenario_shared_clock():
    """Two channels, one clock, different rates: no drift."""
    fs = fixtures.FS_HIRES
    clock = SharedClock(fs)
    h = fixtures.lowpass_kernel(25, 3, 2)
    x = fixtures.tone_samples(600, fs, [1000.0, 7000.0])
    a = run_all(clock.channel(h, 3, 1), [(x, ())])
    chunks, off = [], 0
    for s in fixtures.random_chunk_sizes(len(x), 9):
        chunks.append((x[off:off + s], ()))
        off += s
    b = run_all(clock.channel(h, 3, 2), chunks)
    ta = {o.source_position: o.time for o in a}
    tb = {o.source_position: o.time for o in b}
    common = set(ta) & set(tb)
    drift = max((abs(float(ta[p] - tb[p])) for p in common), default=0.0)
    print(f"  channel A (3/1) one-shot vs channel B (3/2) random chunks: "
          f"{len(common)} shared positions, max time divergence = {drift:.3e} s")
    assert common and drift == 0.0


def scenario_tail():
    """Filter tail is emitted, not dropped."""
    L, M, taps = 3, 2, 25
    h = fixtures.lowpass_kernel(taps, L, M)
    r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
    outs = run_all(r, [(fixtures.impulse(100), ())])
    tail = [o for o in outs if o.flags & FLAG_TAIL]
    print(f"  {len(tail)} tail outputs; last output time "
          f"{float(outs[-1].time) * 1e3:.4f} ms reaches input end "
          f"({100 / fixtures.FS_AUDIO * 1e3:.4f} ms)")
    assert tail and outs[-1].time >= Fraction(100) / fixtures.FS_AUDIO - r.latency_seconds


def main():
    scenarios = [
        ("1. impulse response vs direct reference", scenario_impulse),
        ("2. runtime ratio switch at non-integer phase", scenario_ratio_switch),
        ("3. random chunking + tail checkpoint restore", scenario_chunking_checkpoint),
        ("4. gap marking and contaminated ranges", scenario_gaps),
        ("5. shared clock, different rates, no drift", scenario_shared_clock),
        ("6. filter tail emitted, not dropped", scenario_tail),
    ]
    for name, fn in scenarios:
        print(name)
        fn()
    print("ALL SCENARIOS PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
