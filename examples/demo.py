"""End-to-end demo: compile a plan, render it in random chunks, map
samples between source and output, and apply an incremental edit.

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import random
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from xsplice import EQUAL_POWER, Segment, Session, fixtures, render, render_chunked


def main():
    sources = fixtures.make_sources()
    rate = fixtures.SAMPLE_RATE
    print(f"sources: {[(k, len(v)) for k, v in sources.items()]} @ {rate} Hz")

    session = Session(sources, rate)
    session.upsert(Segment("verse", "verse", 0, 1200, 0,
                           fade_out=240, curve=EQUAL_POWER))
    session.upsert(Segment("chorus", "chorus", 0, 1200, 960,
                           fade_in=240, fade_out=120, curve=EQUAL_POWER))
    # Reversed tail: plays tail[599:299:-1] after a 120-sample crossfade.
    session.upsert(Segment("tail", "tail", 600, 300, 2040,
                           fade_in=120, curve=EQUAL_POWER))
    plan = session.plan

    print(f"compiled: total={plan.total} samples "
          f"({plan.total / rate:.4f} s), latency={plan.latency} samples, "
          f"tolerance={plan.tolerance}")
    print("intervals (source -> output mapping):")
    for iv in plan.intervals:
        print(f"  out[{iv.out_start}:{iv.out_end}] <- {iv.src_id}"
              f"[{iv.src_first} step {iv.step}] ({iv.seg_id})")

    # Pre-generated plan + random chunked execution == whole render.
    rng = random.Random(42)
    sizes = [rng.randint(1, 96) for _ in range(40)]
    whole = render(plan)
    chunked = render_chunked(plan, sizes)
    print(f"random-chunk render identical to whole render: {chunked == whole}")

    # Sample mapping both ways.
    t = 1000  # inside the verse->chorus crossfade
    print(f"output_to_source({t}) = {plan.output_to_source(t)}")
    print(f"source_to_output('tail', 599) = {plan.source_to_output('tail', 599)}")

    # Incremental edit: only the dirty interval needs re-rendering.
    rev = session.upsert(Segment("chorus", "chorus", 100, 1300, 960,
                                 fade_in=240, fade_out=120, curve=EQUAL_POWER))
    print(f"edit chorus: changed_output={rev.changed_output}, "
          f"changed_source={rev.changed_source}")

    peak = max(abs(v) for v in whole)
    print(f"peak amplitude of rendered mix: {peak:.6f}")


if __name__ == "__main__":
    main()
