"""Runnable demo: runtime kernel switching with crossfade and snapshots.

Scenario (all signals from local fixtures, no audio device):
  1. constant + sine input, kernel switched lp2 -> echo mid-fade -> long32
  2. a switch is issued *during* an ongoing fade (composed, not dropped)
  3. a snapshot is taken mid-fade and restored; output continues
     bit-identically
  4. every printed block carries source-kernel weights and exact times
"""

import json

from kernel_switcher import KernelSwitcher
from fixtures import SAMPLE_RATE, KERNELS, constant, sine

TOL = 1e-9


def fmt_weights(weights):
    return ", ".join(f"{ver}={w:.4f}" for ver, w in sorted(weights.items()))


def main():
    n = 960
    src = [c + s for c, s in zip(constant(0.5, n), sine(440.0, n, amplitude=0.2))]

    sw = KernelSwitcher(KERNELS["lp2"], "lp2", SAMPLE_RATE, max_kernel_length=64)
    sw.schedule_switch("echo", KERNELS["echo"], fade_samples=120, at_sample=240)
    # Second switch lands in the middle of the first fade (240..360).
    sw.schedule_switch("long32", KERNELS["long32"], fade_samples=120,
                       at_sample=300)

    print(f"sample rate: {SAMPLE_RATE:.0f} Hz, declared latency: 0 samples")
    print(f"bounds: voices <= {sw.max_voices}, "
          f"history = {sw.history_length} samples, "
          f"worst-case MACs/sample <= {sw.max_voices * 64}")
    print()

    snap = None
    produced = []
    block = 96
    for start in range(0, n, block):
        blk = sw.process_block(src[start:start + block])
        produced.extend(blk.samples)
        w0 = blk.weights[0]
        w1 = blk.weights[-1]
        print(f"block @{blk.start_sample:4d}  t=[{blk.times[0]:.5f} .. "
              f"{blk.times[-1]:.5f}] s")
        print(f"   weights start: {fmt_weights(w0)}")
        print(f"   weights end  : {fmt_weights(w1)}")
        if snap is None and sw.n > 300:  # mid-fade safe point
            snap = sw.snapshot()
            print(f"   >> snapshot saved at sample {sw.n} "
                  f"(voices={sw.active_voice_count})")
    print()

    # --- invariant checks -------------------------------------------------
    edges = sw.graph["edges"]
    switches = [e for e in edges if e["type"] == "switch"]
    assert len(switches) == 2, switches
    assert switches[0]["sample"] == 240 and switches[1]["sample"] == 300
    assert sw.active_voice_count <= sw.max_voices
    assert sw.history_length == 63  # declared bound: max_kernel_length - 1

    # --- restore from the mid-fade snapshot and verify bit-exact resume ---
    sw2 = KernelSwitcher.restore(json.loads(json.dumps(snap)))
    pos = snap["n"]
    tail_ref = produced[pos:]
    tail_new = []
    for start in range(pos, n, block):
        tail_new.extend(sw2.process_block(src[start:start + block]).samples)
    assert tail_new == tail_ref, "restore must resume bit-exactly"
    print(f"snapshot @{snap['n']}: restored stream is bit-identical "
          f"({len(tail_new)} samples)")

    # --- equal-gain constant check: no step anywhere ----------------------
    sw3 = KernelSwitcher(KERNELS["identity"], "id", SAMPLE_RATE)
    sw3.schedule_switch("lp4", KERNELS["lp4"], 64, at_sample=100)
    sw3.schedule_switch("long32", KERNELS["long32"], 64, at_sample=130)
    out = sw3.process_block(constant(1.0, 400)).samples
    worst = max(abs(y - 1.0) for y in out)
    assert worst < TOL, worst
    print(f"equal-gain constant check: max deviation {worst:.3e} (< {TOL:g})")

    # --- rapid switches inside fades: voices composed, never dropped ------
    sw4 = KernelSwitcher(KERNELS["identity"], "id", SAMPLE_RATE)
    pool = ["lp2", "lp4", "long32", "echo"]
    for k in range(8):
        ver = pool[k % len(pool)]
        sw4.schedule_switch(ver, KERNELS[ver], 200, at_sample=100 + 20 * k)
    out4 = sw4.process_block(constant(0.5, 600)).samples
    worst4 = max(abs(y - 0.5) for y in out4[64:])  # after history is full
    merges4 = [e for e in sw4.graph["edges"] if e["type"] == "merge"]
    assert worst4 < TOL, worst4
    assert sw4.active_voice_count <= sw4.max_voices
    print(f"rapid-switch check: {len(merges4)} composed voices, "
          f"max deviation {worst4:.3e}, voices={sw4.active_voice_count}")
    for e in merges4:
        w = e["weights"]
        print(f"   merge @{e['sample']:4d}  {e['from'][0]}(w={w[0]:.3f}) + "
              f"{e['from'][1]}(w={w[1]:.3f}) -> {e['to']}")

    print()
    print("transition graph:")
    for ver, info in sw.graph["nodes"].items():
        print(f"  node {ver:18s} len={info['kernel_length']:3d} "
              f"first_seen={info['first_seen_sample']}")
    for e in edges:
        if e["type"] == "switch":
            print(f"  switch @{e['sample']:4d} fade={e['fade_samples']:4d}  "
                  f"{'+'.join(e['from'])} -> {e['to']}")
        else:
            print(f"  merge  @{e['sample']:4d}  "
                  f"{'+'.join(e['from'])} -> {e['to']}")
    print()
    print("DEMO OK")


if __name__ == "__main__":
    main()
