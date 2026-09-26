#!/usr/bin/env python3
"""Runnable demo of the offline loudness normalization planner.

Run from anywhere:  python3 examples/run_example.py
"""

import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from loudnorm import (  # noqa: E402
    apply_plan,
    plan_normalization,
    write_output,
)
from loudnorm import fixtures  # noqa: E402


def show(title, plan):
    print("== %s ==" % title)
    print("status: %s | latency: %d samples | tolerances: %s"
          % (plan.status, plan.latency_samples, plan.tolerances))
    for i, seg in enumerate(plan.segments):
        tag = " (silent)" if seg["silent"] else ""
        print("  seg %d [%6d, %6d) target=%.4f rms=%.4f gain=%.4f%s"
              % (i, seg["start"], seg["end"], seg["target_rms"],
                 seg["rms"], seg["gain"], tag))
    for note in plan.notes:
        print("  note: %s" % note["detail"])
    for conflict in plan.conflicts:
        print("  CONFLICT [%s] seg=%s residual=%.2f dB: %s"
              % (conflict["kind"], conflict["segment"],
                 conflict["residual_db"], conflict["detail"]))
    print()


def main():
    # 1. Feasible case: fully normalized, chunked == whole-block.
    samples, rate, specs, kwargs = fixtures.feasible_case()
    plan = plan_normalization(samples, rate, specs, **kwargs)
    show("feasible case", plan)
    whole = apply_plan(samples, rate, plan)
    chunked = apply_plan(samples, rate, plan, chunk_size=997)
    assert whole == chunked, "chunked application must equal whole-block"
    print("chunked (997) == whole-block: exact match\n")

    # 2. Serialization round-trip: JSON -> plan -> identical rendering.
    restored = plan.__class__.from_json(plan.to_json())
    assert apply_plan(samples, rate, restored) == whole
    print("JSON round-trip: identical rendering\n")

    # 3. Infeasible case: adjacent targets too far apart for the rate
    #    limit -> conflicts with residuals, never claimed normalized.
    s2, r2, spec2, kw2 = fixtures.infeasible_case()
    plan2 = plan_normalization(s2, r2, spec2, **kw2)
    show("infeasible adjacent targets", plan2)

    # 4. Crossfade peak: repaired so the rendered peak respects the ceiling.
    s3, r3, spec3, kw3 = fixtures.crossfade_peak_case()
    plan3 = plan_normalization(s3, r3, spec3, **kw3)
    show("crossfade peak repair", plan3)
    out3 = apply_plan(s3, r3, plan3)
    print("rendered peak after repair: %.6f (ceiling %.6f)\n"
          % (max(abs(x) for x in out3), plan3.peak_ceiling))

    # 5. Atomic write with complete-file marker.
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, "out.f32")
        write_output(path, samples, rate, plan, chunk_size=4096)
        with open(path + ".complete", encoding="utf-8") as fh:
            marker = json.load(fh)
        print("wrote %s (%d bytes), marker complete=%s digest=%.16s..."
              % (path, os.path.getsize(path), marker["complete"],
                 marker["input_digest"]))


if __name__ == "__main__":
    main()
