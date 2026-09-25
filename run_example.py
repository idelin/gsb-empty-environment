"""Run example: repair gaps in local fixtures, online and offline.

Usage: python3.11 run_example.py
"""

from pathlib import Path

from gaprepair import (Mode, ModelRegistry, ModelVersion, NUMERIC_TOLERANCE,
                       RepairConfig, find_gaps, load_fixture,
                       online_latency_samples, repair,
                       synthetic_occlusion_experiment)

MASK_CHARS = {0: "K", 1: "R", 2: "U"}


def show(result, label):
    print(f"\n== {label} (mode={result.mode.value}, "
          f"model={result.model_version}) ==")
    print("mask:", "".join(MASK_CHARS[m] for m in result.mask))
    print("counts:", result.counts())
    for segment in result.repairs:
        gap = segment.gap
        if segment.method == "none":
            print(f"  gap[{gap.start}:{gap.end}] len={gap.length} -> UNKNOWN "
                  f"({segment.evidence.get('reason')})")
        else:
            head = ", ".join(
                f"{p:+.4f}+/-{s:.4f}"
                for p, s in list(zip(segment.predictions, segment.stderr))[:3]
            )
            print(f"  gap[{gap.start}:{gap.end}] len={gap.length} "
                  f"-> {segment.method}: {head}")


def main():
    config = RepairConfig(max_gap=8, ar_order=4, window=32)
    samples, fs = load_fixture(Path("fixtures/signal_short_gaps.json"))
    gaps = [(g.start, g.length) for g in find_gaps(samples)]
    print(f"fixture: {len(samples)} samples @ {fs} Hz; gaps: {gaps}")

    show(repair(samples, fs, config, Mode.ONLINE, model_version="v1"),
         "online causal AR repair")

    calib = synthetic_occlusion_experiment(samples, config, Mode.OFFLINE)
    print("\n== synthetic occlusion calibration (offline) ==")
    for key, detail in sorted(calib.details.items()):
        print(f"  {key}: n={detail['n']} rms_z={detail['rms_z']:.3f} "
              f"coverage_95={detail['coverage_95']:.3f} "
              f"scale={detail['scale']:.3f}")

    calib_on = synthetic_occlusion_experiment(samples, config, Mode.ONLINE)
    print("\n== synthetic occlusion calibration (online) ==")
    for key, detail in sorted(calib_on.details.items()):
        print(f"  {key}: n={detail['n']} rms_z={detail['rms_z']:.3f} "
              f"coverage_95={detail['coverage_95']:.3f} "
              f"scale={detail['scale']:.3f}")

    registry = ModelRegistry()
    registry.register(ModelVersion("v1-uncalibrated", config, None))
    registry.register(ModelVersion("v2-calibrated", config, calib))
    print("\nregistry active:", registry.active.name)
    show(repair(samples, fs, registry.active.config, Mode.OFFLINE,
                model_version=registry.active.name,
                calibration=registry.active.calibration),
         "offline two-sided repair (calibrated)")
    rolled = registry.rollback()
    print("rolled back to:", rolled.name, "| history:", registry.history)

    long_samples, fs2 = load_fixture(Path("fixtures/signal_long_gap.json"))
    show(repair(long_samples, fs2, config, Mode.OFFLINE,
                model_version=rolled.name),
         "long gap stays unknown")

    print(f"\ndeclared numeric tolerance: {NUMERIC_TOLERANCE:.0e}")
    print("declared online latency: gap_length + 1 samples "
          f"(e.g. len=4 -> {online_latency_samples(4)} samples)")


if __name__ == "__main__":
    main()
