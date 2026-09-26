"""Run sample: concatenate three segments with equal-power crossfades."""

import xfade
import fixtures


def main() -> None:
    sources = fixtures.make_sources()
    fade = 256
    segments = xfade.concatenate(
        [
            xfade.Segment("lead", 0, 2000, 0, name="intro"),
            xfade.Segment("pad", 500, 2000, 0, name="verse"),
            xfade.Segment("lead", 1000, 1500, 0, reversed=True, name="echo"),
        ],
        fade_len=fade,
    )
    out, comp = xfade.render(
        segments, sources, fade, xfade.Curve.EQUAL_POWER
    )
    print(f"sample rate      : {fixtures.SAMPLE_RATE} Hz")
    print(f"declared latency : {xfade.LATENCY_SAMPLES} samples")
    print(f"declared tolerance: {xfade.TOLERANCE:g}")
    print(f"output length    : {comp.output_length} samples "
          f"(= 5500 - 2 * {fade}, crossfade counted once)")
    print(f"peak total gain  : {comp.peak_total_gain:.6f} "
          f"(limit sqrt(2) = {2 ** 0.5:.6f})")
    print(f"peak |sample|    : {max(abs(x) for x in out):.6f}")

    m = comp.maps[2]
    o = m.source_to_output(1200)
    print(f"mapping example  : 'echo' source 1200 -> output {o}; "
          f"output {o} -> source {m.output_to_source(o)}")

    plan = xfade.build_plan(segments, sources, fade,
                            xfade.Curve.EQUAL_POWER, block=128)
    worst = 0.0
    for seed in range(8):
        chunked = xfade.execute_plan(plan, sources, seed=seed)
        worst = max(worst, max(abs(a - b) for a, b in zip(out, chunked)))
    print(f"plan ops         : {len(plan.ops)} blocks of <=128 samples")
    print(f"random-chunk worst diff over 8 seeds: {worst:.3e} "
          f"(< tolerance: {worst < xfade.TOLERANCE})")


if __name__ == "__main__":
    main()
