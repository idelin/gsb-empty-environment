"""Replayable end-to-end demo of the multiband compressor/limiter pipeline.

Deterministic: local fixtures only, no audio devices, no randomness, no I/O.
Prints declared latencies, tolerances, gain-trajectory summaries and
over-limit evidence for each scenario. Exit code 0 iff every check passes.

Run: python3.11 run_demo.py
"""

import math

from fixtures import (
    SAMPLE_RATE, TOL_RECON, impulse, make_processor, multitone, run_blocks,
    with_unknown,
)


def lin(db):
    return 10.0 ** (db / 20.0)


def fmt_ms(samples):
    return "%.3f ms" % (1000.0 * samples / SAMPLE_RATE)


def report_header(proc):
    print("== declared stage latencies ==")
    total = 0
    for name, samples in proc.stage_latencies().items():
        print("  %-10s %4d samples (%s)" % (name, samples, fmt_ms(samples)))
        total += samples
    print("  %-10s %4d samples (%s)" % ("TOTAL", total, fmt_ms(total)))
    print("declared tolerances: recon=%.0e peak=%.0e" % (TOL_RECON, 1e-6))


def scenario_bypass():
    print("\n== scenario 1: bypass recombination vs reference ==")
    proc = make_processor(ratio=1.0, limiter_threshold_db=0.0)
    report_header(proc)
    n = 4096
    ref = multitone(n, [100.0, 1500.0, 9000.0], [0.2, 0.2, 0.2])
    outs = run_blocks(proc, [ref, [0.0] * n], 512)
    lat = proc.latency_samples
    err = max(abs(outs[0][i] - ref[i - lat]) for i in range(lat, n))
    ok = err < TOL_RECON
    print("  max |out - delayed reference| = %.3e (tol %.0e) -> %s"
          % (err, TOL_RECON, "PASS" if ok else "FAIL"))
    return ok


def scenario_linked_dynamics():
    print("\n== scenario 2: stereo linked dynamics, right-only spike ==")
    proc = make_processor()
    n = 4096
    left = multitone(n, [100.0], [0.05])
    right = impulse(n, 1000, 0.9)
    right[2000:2480] = multitone(480, [100.0], [0.8])
    outs = run_blocks(proc, [left, right], 512)
    for name in ("band:low", "band:mid", "band:high", "limiter"):
        traj = proc.trajectories[name]
        print("  gain[%s]: min=%.4f final=%.4f"
              % (name, min(traj), traj[-1]))
    print("  right peak out = %.4f (threshold %.4f)"
          % (max(abs(v) for v in outs[1]), lin(-6.0)))
    print("  left  peak out = %.4f (audio independent, gain-shared)"
          % max(abs(v) for v in outs[0]))
    print("  over-limit evidence: %d segment(s)" % len(proc.evidence))
    for ev in proc.evidence[:5]:
        print("    samples %d..%d peak=%.4f thr=%.1f dB min_gain=%.4f"
              % (ev["start"], ev["end"], ev["peak"],
                 ev["threshold_db"], ev["min_gain"]))
    ok = (min(proc.trajectories["limiter"]) < 1.0
          and max(abs(v) for v in outs[1]) <= lin(-6.0) * 1.001
          and len(proc.evidence) >= 1)
    print("  -> %s" % ("PASS" if ok else "FAIL"))
    return ok


def scenario_hot_update_and_toggle():
    print("\n== scenario 3: threshold hot-update + cancel/restore ==")
    proc = make_processor(channels=1, ratio=1.0, threshold_smooth_ms=2.0)
    block, n = 512, 52 * 512
    pa, pb, pc = 400, 10000, 18000
    x = [0.0] * n
    for p in (pa, pb, pc):
        x[p] = 0.9
    out, pos = [], 0

    def feed(count):
        nonlocal pos
        for _ in range(count):
            out.extend(proc.process_block([x[pos:pos + block]])[0])
            pos += block

    feed(1)
    proc.limiter.set_threshold_db(-20.0)   # spike A still inside lookahead
    feed(8)
    proc.limiter.set_enabled(False)        # cancel: spike B passes through
    feed(15)
    proc.limiter.set_enabled(True)         # restore: spike C limited again
    feed(52 - 24)
    out.extend(proc.flush()[0])
    lat = proc.latency_samples
    rows = [("A hot-updated to -20 dB", pa),
            ("B cancelled (passes)   ", pb),
            ("C restored, limited    ", pc)]
    ok = True
    for label, p in rows:
        got = out[p + lat]
        print("  spike %s: out=%.4f at sample %d (latency %d)"
              % (label, got, p + lat, lat))
    ok = (abs(out[pa + lat] - lin(-20.0)) < 1e-6
          and out[pb + lat] > 0.85
          and abs(out[pc + lat] - lin(-20.0)) < 1e-6
          and len(proc.evidence) == 3)
    print("  evidence segments: %d (cancelled segment kept as proof)"
          % len(proc.evidence))
    print("  -> %s" % ("PASS" if ok else "FAIL"))
    return ok


def scenario_unknown_samples():
    print("\n== scenario 4: unknown (NaN) sample marking/propagation ==")
    proc = make_processor(channels=1, attack_ms=1.0, release_ms=5.0)
    n, k = 8192, 1000
    x = with_unknown(multitone(n, [300.0], [0.3]), [k])
    out = run_blocks(proc, [x], 512)[0]
    nan_idx = [i for i, v in enumerate(out) if math.isnan(v)]
    print("  unknown input at sample %d -> marked output region %d..%d"
          % (k, nan_idx[0], nan_idx[-1]))
    print("  region covers FIR window + envelope taint + limiter lookahead;")
    print("  output before %d and after %d is finite (pipeline recovers)"
          % (nan_idx[0], nan_idx[-1]))
    ok = (nan_idx[0] == k and math.isnan(proc.trajectories["limiter"][k])
          and not any(math.isnan(v) for v in out[nan_idx[-1] + 1:]))
    print("  -> %s" % ("PASS" if ok else "FAIL"))
    return ok


def main():
    results = [
        scenario_bypass(),
        scenario_linked_dynamics(),
        scenario_hot_update_and_toggle(),
        scenario_unknown_samples(),
    ]
    print("\n== summary ==")
    print("  %d/%d scenarios passed" % (sum(results), len(results)))
    return 0 if all(results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
