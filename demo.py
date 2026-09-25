"""Runnable example: multi-track rendering through a piecewise-affine plan.

Run with:  python3.11 demo.py
"""
from fixtures import load_tracks
from plan import EditPlan
from render import (
    LATENCY_SAMPLES,
    LOOKAHEAD_SOURCE_SAMPLES,
    TOLERANCE,
    render_chunked,
    render_direct,
)
from timemap import Kind


def main() -> None:
    tracks, sr = load_tracks()
    print(f"fixture: {len(tracks)} tracks x {len(tracks[0])} samples @ {sr} Hz")
    print(f"declared latency={LATENCY_SAMPLES} samples, "
          f"lookahead<={LOOKAHEAD_SOURCE_SAMPLES} source sample, "
          f"tolerance={TOLERANCE:g}")

    # Compound edit: forward, freeze, reverse, 2x forward, repeated region.
    # No single speed factor could describe this plan.
    plan = EditPlan.from_control_points([
        (0.000, 0.0),      # \
        (0.020, 160.0),    # /  forward, speed 1
        (0.030, 160.0),    #    freeze: hold source position 160
        (0.045, 40.0),     #    reverse back to position 40
        (0.050, 120.0),    #    forward, speed 2 (duration halved)
        (0.060, 40.0),     #    reverse: replays the 40..120 region
        (0.070, 120.0),    #    forward: repeats the same region again
    ])
    print("\nsegments (output s -> source pos, kind):")
    for seg in plan.time_map.segments:
        print(f"  [{seg.out_start:.3f}, {seg.out_end:.3f}) s "
              f"-> [{seg.src_start:6.1f}, {seg.src_end:6.1f}]  {seg.kind.value}")

    num_output = int(plan.out_end * sr)
    direct = render_direct(plan, tracks, sr, num_output)
    chunked = render_chunked(plan, tracks, sr, num_output, chunk_size=137)
    max_diff = max(
        abs(a - b)
        for ta, tb in zip(direct.values, chunked.values)
        for a, b in zip(ta, tb)
    )
    print(f"\nrendered {num_output} output samples "
          f"({num_output / sr:.3f} s) per track")
    print(f"chunked(137) vs direct: max |diff| = {max_diff:.3e} "
          f"(tolerance {TOLERANCE:g}), masks equal: {direct.mask == chunked.mask}")

    freeze_idx = [i for i, k in enumerate(direct.kinds) if k is Kind.FREEZE]
    print(f"\nfreeze segment: output samples {freeze_idx[0]}..{freeze_idx[-1]} "
          f"all read source position {direct.positions[freeze_idx[0]]:.1f} "
          f"and are marked Kind.FREEZE")

    # Speed 2 check: the 0.045..0.050 s leg covers 80 source positions in
    # 40 output samples.
    i0, i1 = int(0.045 * sr), int(0.050 * sr)
    span = direct.positions[i1 - 1] - direct.positions[i0]
    print(f"speed-2 leg: {i1 - i0} output samples cover "
          f"{span:.1f} source positions (duration halved)")

    # Revision: move one control point, recompute only the affected range.
    revised = plan.revise([
        (0.000, 0.0), (0.020, 160.0), (0.030, 170.0),  # moved freeze level
        (0.045, 40.0), (0.050, 120.0), (0.060, 40.0), (0.070, 120.0),
    ])
    rng = plan.affected_output_range(revised)
    print(f"\nafter moving the freeze control point, only output "
          f"[{rng[0]:.3f}, {rng[1]:.3f}) s is affected; the old plan still "
          f"reads source {plan.time_map.source_at(0.025):.1f} at 0.025 s "
          f"(old readers unaffected)")

    print("\nsample excerpt (track 0, output samples 158..164, freeze):")
    for i in range(158, 165):
        print(f"  out {i:3d}: pos={direct.positions[i]:6.1f} "
              f"kind={direct.kinds[i].value:7s} "
              f"value={direct.values[0][i]:+.4f} valid={direct.mask[0][i]}")


if __name__ == "__main__":
    main()
