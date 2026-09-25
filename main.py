"""Demo run: fixture-based gap repair, calibration, and version rollback.

Run with:  python3.11 main.py
"""

from dataclasses import replace

from gaprepair import (
    MODE_OFFLINE,
    MODE_ONLINE,
    NUMERICAL_TOLERANCE,
    OFFLINE_LATENCY,
    ONLINE_LATENCY,
    ModelRegistry,
    ModelVersion,
    RepairConfig,
    calibrate_and_register,
    repair_all,
    restore_segments,
    synthetic_occlusion_experiment,
)
from gaprepair import fixtures


def show_reports(title, result):
    print(f"\n== {title} ==")
    repaired = sum(result.repair_mask)
    print(f"samples={len(result.samples)} repaired={repaired} "
          f"unknown={sum(1 for g in result.gaps if g.status == 'left_unknown')}")
    for gap in result.gaps:
        errs = gap.error_estimates
        err_txt = "none" if errs is None else ", ".join(f"{e:.4f}" for e in errs)
        print(f"  gap start={gap.start:>3} len={gap.length:>2} "
              f"status={gap.status:<12} method={str(gap.method):<6} "
              f"err_est=[{err_txt}]")
        interesting = {k: v for k, v in gap.evidence.items()
                       if k in ("reason", "degrade_reason", "degraded_to",
                                "anchors", "train_samples_used",
                                "condition_estimate")}
        if gap.evidence.get("ar_fit"):
            interesting["ar_fit"] = {
                k: v for k, v in gap.evidence["ar_fit"].items()
                if k in ("reason", "condition_estimate")
            }
        if interesting:
            print(f"      evidence={interesting}")


def main():
    samples, rate, gap_specs = fixtures.load_fixture_with_gaps()
    print("declarations")
    print(f"  sample rate (local fixture): {rate} Hz (no audio device is used)")
    print(f"  numerical tolerance: {NUMERICAL_TOLERANCE}")
    print(f"  online latency:  {ONLINE_LATENCY}")
    print(f"  offline latency: {OFFLINE_LATENCY}")
    print(f"  gaps in fixture: {gap_specs} (max_gap_len=8)")

    online = repair_all(samples, RepairConfig(mode=MODE_ONLINE))
    show_reports("online repair (causal AR, degrades to linear)", online)

    offline = repair_all(samples, RepairConfig(mode=MODE_OFFLINE))
    show_reports("offline repair (two-sided linear)", offline)

    edge, _, edge_specs = fixtures.load_fixture_with_edge_gaps()
    print(f"\n== edge gaps {edge_specs}: leading gap has no left anchor ==")
    edge_online = repair_all(edge, RepairConfig(mode=MODE_ONLINE))
    edge_offline = repair_all(edge, RepairConfig(mode=MODE_OFFLINE))
    for label, res in (("online", edge_online), ("offline", edge_offline)):
        for gap in res.gaps:
            print(f"  {label:<7} gap start={gap.start:>3} len={gap.length} "
                  f"status={gap.status} evidence={gap.evidence.get('reason') or gap.evidence.get('degrade_reason') or 'ok'}")

    final, steps = restore_segments(samples, RepairConfig(mode=MODE_ONLINE))
    max_diff = max(abs(a - b) for a, b in zip(final, online.samples)
                   if a == a and b == b) if final else 0.0
    print(f"\nsegment-by-segment restore matches repair_all: "
          f"max diff {max_diff:.3e} (tolerance {NUMERICAL_TOLERANCE})")

    print("\n== synthetic occlusion calibration + version rollback ==")
    clean, _ = fixtures.load_clean_fixture()
    base = RepairConfig(mode=MODE_ONLINE)
    registry = ModelRegistry()
    v0 = ModelVersion("uncalibrated-v0", error_scale=1.0, metrics={})
    registry.register(v0)
    train_occ = fixtures.calibration_occlusions(count=24, length=4, seed=7)
    val_occ = fixtures.calibration_occlusions(count=24, length=4, seed=99)
    before = synthetic_occlusion_experiment(clean, base, val_occ)
    outcome = calibrate_and_register(clean, base, train_occ, val_occ,
                                     registry, "calibrated-v1",
                                     target_coverage=0.95)
    print(f"  v0 val coverage (z={base.confidence_z}): {before['coverage']:.3f}")
    print(f"  calibrated scale: {outcome['candidate'].error_scale:.4f}")
    print(f"  v1 train coverage: {outcome['train_coverage']:.3f} "
          f"val coverage: {outcome['val_coverage']:.3f}")
    print(f"  active version: {registry.active.name} "
          f"(rolled back: {outcome['rolled_back']})")
    registry.rollback("uncalibrated-v0")
    print(f"  after explicit rollback: {registry.active.name}")
    registry.rollback("calibrated-v1")
    print(f"  re-activated: {registry.active.name}")


if __name__ == "__main__":
    main()
