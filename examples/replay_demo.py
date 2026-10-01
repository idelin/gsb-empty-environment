"""Replayable demo covering the required operation sequence.

Run:  python examples/replay_demo.py
Exit code 0 means every check passed.
"""
from __future__ import annotations

import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from stftpipe import NUMERICAL_TOLERANCE, STFTPipeline, SpectralEditError, fixtures
from stftpipe.fft import fft
from stftpipe.reference import dft

_OK = True


def check(name, cond, detail=""):
    global _OK
    _OK = _OK and bool(cond)
    print(f"  [{'PASS' if cond else 'FAIL'}] {name} {detail}")


def max_err(a, b):
    return max((abs(x - y) for x, y in zip(a, b)), default=0.0)


def main():
    print(f"declared: latency = 1 window (samples); tolerance = {NUMERICAL_TOLERANCE:g}; "
          "stdlib only, fixtures instead of audio devices")

    print("1) fixed legal window/hop round trip (hann N=256 hop=64)")
    sig = fixtures.mixed(3000)
    pipe = STFTPipeline(256, 64, sample_rate=fixtures.SAMPLE_RATE_A)
    pipe.add_channel(sig, fixtures.SAMPLE_RATE_A)
    pipe.analyze()
    err = max_err(pipe.output(), sig)
    check("round-trip lossless", pipe.is_lossless() and err < NUMERICAL_TOLERANCE,
          f"err={err:.3e} latency={pipe.algorithmic_latency_samples} samples")

    print("2) variable-hop round trip + weight check (hops=[48,96,32,80])")
    vh = STFTPipeline(256, [48, 96, 32, 80])
    vh_sig = fixtures.mixed(4000)
    vh.add_channel(vh_sig)
    vh.analyze()
    w = vh.weights()
    check("variable-hop lossless",
          vh.is_lossless() and max_err(vh.output(), vh_sig) < NUMERICAL_TOLERANCE)
    check("weights not constant (no fixed COLA coefficient)",
          max(w) - min(w) > 0.1, f"w in [{min(w):.3f}, {max(w):.3f}]")

    print("3) drop one frame -> exact unrecoverable region (hop = N/2)")
    dr = STFTPipeline(256, 128)
    dr.add_channel(sig)
    dr.analyze()
    dr.drop_frame(3)
    regions = dr.unrecoverable_regions()
    expect = [(dr.starts[3] + 128, dr.starts[3] + 129)]
    check("unrecoverable region exact", regions == expect, f"{regions}")
    check("not claimed lossless", not dr.is_lossless())
    dr.restore_frame(3)
    check("restore frame recovers",
          dr.is_lossless() and max_err(dr.output(), sig) < NUMERICAL_TOLERANCE)

    print("4) local spectral edit (real-signal conjugate constraint)")
    ed = STFTPipeline(256, 64)
    ed.add_channel(sig)
    ed.analyze()
    before = ed.output()
    ed.edit_spectrum(0, 2, {5: 0.5 + 0.25j})
    after = ed.output()
    lo = max(0, ed.starts[2])
    hi = min(ed.starts[2] + 256, ed.out_len)
    check("only affected interval rebuilt",
          before[:lo] == after[:lo] and before[hi:] == after[hi:]
          and before[lo:hi] != after[lo:hi])
    try:
        ed.edit_spectrum(0, 2, {5: 1 + 1j, 251: 2 + 0j})
        check("conjugate violation rejected", False)
    except SpectralEditError:
        check("conjugate violation rejected", True)

    print("5) failed batch correction keeps published version")
    v0 = ed.published_version
    out0 = ed.output()
    try:
        ed.apply_batch([
            ("edit", 0, 2, {6: 0.2 + 0.1j}),
            ("edit", 0, 3, {4: 1 + 1j, 252: 9 + 9j}),
        ])
        check("batch rolled back", False)
    except SpectralEditError:
        check("batch rolled back",
              ed.published_version == v0 and ed.output() == out0,
              f"version={ed.published_version}")

    print("6) checkpoint keeps unclosed tail; resumed stream matches one-shot")
    full_sig = fixtures.mixed(4000)
    full = STFTPipeline(256, 64)
    full.add_channel(full_sig)
    full.analyze()
    part = STFTPipeline(256, 64)
    part.add_channel(full_sig[:2500])
    part.analyze()
    ckpt = part.checkpoint()
    check("checkpoint has open tail",
          part.starts[0] <= ckpt["tail_start"] < part.out_len
          and len(ckpt["open_tail"]["output"][0]) == part.out_len - ckpt["tail_start"] > 0)
    resumed = STFTPipeline.from_checkpoint(ckpt)
    resumed.extend({0: full_sig[2500:]}, 64)
    check("resumed == one-shot",
          max_err(resumed.output(), full.output()) < NUMERICAL_TOLERANCE
          and resumed.mask() == full.mask())

    print("7) multi-channel time alignment")
    tone = fixtures.tone(440.0, 3000)
    delay = 100
    mc = STFTPipeline(256, 64)
    mc.add_channel(tone)
    mc.add_channel([0.0] * delay + tone[:3000 - delay])
    mc.analyze()
    o0, o1 = mc.output(0), mc.output(1)
    aerr = max(abs(o1[n] - o0[n - delay]) for n in range(delay + 512, 2500))
    check("aligned within tolerance", aerr < NUMERICAL_TOLERANCE, f"err={aerr:.3e}")

    print("8) direct DFT reference on small input")
    seg = sig[:16]
    derr = max(abs(a - b) for a, b in zip(fft(seg), dft(seg)))
    check("FFT == direct DFT", derr < 1e-12, f"err={derr:.3e}")

    print("ALL CHECKS PASSED" if _OK else "SOME CHECKS FAILED")
    return 0 if _OK else 1


if __name__ == "__main__":
    sys.exit(main())
