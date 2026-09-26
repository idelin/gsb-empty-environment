"""Runnable demo: python3 examples/demo.py

Plans gains for local fixtures, prints conflicts/residuals, applies the
serialized plan in chunks, and demonstrates cancel-safe file output.
"""
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from normplan import fixtures
from normplan.apply import apply_plan, apply_plan_to_file
from normplan.planner import Plan, Segment, lin_to_db, plan_gains


def show(title, result):
    print(f"== {title} ==")
    print(f"normalized={result.normalized}  output_peak={result.output_peak:.6f}")
    for i, s in enumerate(result.plan.segments):
        tag = "silent" if s.silent else (
            f"target={s.target_rms_db:+.2f} dBFS "
            f"achieved={lin_to_db(s.achieved_rms):+.2f} dBFS "
            f"residual={s.residual_db:+.3f} dB"
        )
        print(f"  seg{i} [{s.start},{s.end}) gain={s.gain:.4f}  {tag}")
    for c in result.conflicts:
        print(f"  CONFLICT {c.kind} segs={c.segments}: {c.detail}")
    print()


def main():
    samples, sr = fixtures.loud_quiet_tone()

    feasible = plan_gains(
        samples, sr,
        [Segment(0, 4000, -30.0), Segment(4000, 8000, -12.0)],
        peak_ceiling=0.98, max_adjacent_ratio=4.0,
    )
    show("feasible: quiet/loud tone, generous rate limit", feasible)

    infeasible = plan_gains(
        samples, sr,
        [Segment(0, 4000, -40.0), Segment(4000, 8000, -6.0)],
        peak_ceiling=1.0, max_adjacent_ratio=1.05,
    )
    show("infeasible: 34 dB target jump, strict rate limit", infeasible)

    xfade_samples, xfade_sr = fixtures.overlap_peak_tone()
    xfade = plan_gains(
        xfade_samples, xfade_sr,
        [Segment(0, 2500, -3.0), Segment(2000, 4500, -3.0)],
        peak_ceiling=1.0, max_adjacent_ratio=4.0, fade="equal_power",
    )
    show("equal-power crossfade vs peak ceiling", xfade)

    plan = Plan.from_json(feasible.plan.to_json())
    whole = apply_plan(plan, samples)
    chunked = apply_plan(plan, samples,
                         chunks=[(0, 1000), (1000, 5000), (5000, 8000)])
    assert whole.tobytes() == chunked.tobytes()
    print("chunked apply == whole-buffer apply (bit-identical)")

    with tempfile.TemporaryDirectory() as d:
        out = os.path.join(d, "demo_render")
        ok = apply_plan_to_file(plan, samples, out, chunk_size=1024)
        print(f"file render complete={ok}, marker={os.path.exists(out + '.complete')}")
        ok = apply_plan_to_file(plan, samples, out, chunk_size=1024,
                                cancel_check=lambda w, t: w >= 2048)
        print(f"cancelled render complete={ok}, marker={os.path.exists(out + '.complete')}")

    print("\nserialized plan (first 8 lines):")
    print("\n".join(plan.to_json().splitlines()[:8]))


if __name__ == "__main__":
    main()
