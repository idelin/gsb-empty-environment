"""Orchestrates impulse / sweep / multitone tests and counterexample search."""
from __future__ import annotations

import math
from dataclasses import dataclass, field

from .bands import BandSpec
from .counterexample import (
    CounterExample,
    search_block_inconsistency,
    search_phase_misalignment,
)
from .fir_bounds import fir_band_bounds, fir_response
from .metrics import (
    estimate_lag,
    max_abs_diff,
    to_db,
    tone_fit,
    windowed_tone_amplitude,
    wrap_phase,
)
from .report import MeasurementRecord, VerificationReport
from .signals import (
    impulse,
    log_chirp,
    log_spaced,
    multitone,
    stepped_sweep,
)
from .streaming import run_blocks


@dataclass
class Tolerances:
    passband_deviation: float = 0.05      # |H|-1 in passband, linear
    stopband_attenuation_db: float = 40.0 # required stopband attenuation
    time_offset_samples: float = 1.0      # |measured - declared| latency
    phase_residual_rad: float = 0.1       # passband phase residual after alignment
    block_consistency: float = 1e-6       # streaming vs one-shot max diff
    leakage_db: float = 40.0              # stopband leakage requirement
    numeric: float = 1e-9                 # absolute numeric floor


@dataclass
class VerifierConfig:
    tolerances: Tolerances = field(default_factory=Tolerances)
    signal_length: int = 4096          # multitone / impulse length
    probe_length: int = 1024           # counterexample probe length
    chirp_length: int = 8192           # time-offset chirp length
    max_lag: int = 128
    sweep_steps: int = 24              # stepped-sweep frequencies
    sweep_samples_per_step: int = 512
    block_trials: int = 16
    seed: int = 1234


def _band_tones(lo: float, hi: float, count: int) -> list[float]:
    return log_spaced(max(lo, 1e-6), hi, count)


def verify(impl_factory, spec: BandSpec, config: VerifierConfig | None = None,
           name: str | None = None) -> VerificationReport:
    cfg = config or VerifierConfig()
    tol = cfg.tolerances
    fs = spec.sample_rate
    probe = impl_factory()
    declared = int(getattr(probe, "latency_samples", 0))
    coeffs = getattr(probe, "coefficients", None)
    report = VerificationReport(
        implementation=name or type(probe).__name__,
        declared_latency_samples=declared,
    )
    add = report.measurements.append

    # ---- 1. impulse test ------------------------------------------------
    n_imp = cfg.signal_length
    x_imp = impulse(n_imp)
    y_imp = run_blocks(impl_factory(), x_imp, [n_imp])
    imp_resp = y_imp
    imp_details = {"response_head": [round(v, 9) for v in imp_resp[:8]]}
    imp_passed, imp_value = True, None
    if coeffs is not None:
        err, idx = max_abs_diff(list(coeffs), imp_resp[: len(coeffs)])
        imp_value = err
        imp_passed = err <= max(tol.numeric, 1e-9)
        imp_details["coefficient_mismatch_index"] = idx
    add(MeasurementRecord(
        name="impulse_response",
        method="unit impulse; DFT grid resolution fs/N"
               + ("; compared to declared coefficients" if coeffs else ""),
        sample_rate=fs, num_samples=n_imp, value=imp_value,
        tolerance=(1e-9 if coeffs else None),
        resolution=fs / n_imp, passed=imp_passed, details=imp_details))

    # ---- 2. time offset (log chirp + cross-correlation) -----------------
    n_ch = cfg.chirp_length
    x_ch = log_chirp(n_ch, fs, spec.passband[0] + 20.0, min(spec.passband[1], 0.45 * fs))
    y_ch = run_blocks(impl_factory(), x_ch, [n_ch])
    lag, _ = estimate_lag(x_ch, y_ch, cfg.max_lag)
    offset = lag - declared
    add(MeasurementRecord(
        name="time_offset",
        method="log-chirp cross-correlation lag minus declared latency",
        sample_rate=fs, num_samples=n_ch, value=float(offset),
        tolerance=tol.time_offset_samples, resolution=1.0,
        passed=abs(offset) <= tol.time_offset_samples,
        details={"measured_lag": lag, "declared": declared}))

    # ---- 3. passband deviation (multitone, least-squares tone fits) -----
    n_mt = cfg.signal_length
    pb_tones = _band_tones(spec.passband[0], spec.passband[1], 8)
    x_pb = multitone(pb_tones, fs, n_mt, seed=cfg.seed)
    y_pb = run_blocks(impl_factory(), x_pb, [n_mt])
    y_al = y_pb[declared : declared + n_mt]
    gains, phases = [], []
    for f in pb_tones:
        a_in, p_in, _ = tone_fit(x_pb, f, fs, start=0)
        a_out, p_out, _ = tone_fit(y_al, f, fs)
        gains.append(a_out / a_in)
        phases.append(wrap_phase(p_out - p_in))
    deviation = max(abs(g - 1.0) for g in gains)
    # windowed cross-check with coherent-gain normalization (unity must read 1)
    win_gain = [
        windowed_tone_amplitude(y_al, f, fs) / max(tone_fit(x_pb, f, fs)[0], 1e-300)
        for f in pb_tones[:3]
    ]
    add(MeasurementRecord(
        name="passband_deviation",
        method="multitone least-squares tone fits; windowed coherent-gain cross-check",
        sample_rate=fs, num_samples=n_mt, value=deviation,
        tolerance=tol.passband_deviation,
        resolution=(pb_tones[-1] - pb_tones[0]) / (len(pb_tones) - 1),
        passed=deviation <= tol.passband_deviation,
        details={"gains": [round(g, 6) for g in gains],
                 "windowed_gain_crosscheck": [round(g, 6) for g in win_gain]}))

    # ---- 4. passband phase residual (phase misalignment indicator) ------
    phase_res = max(abs(p) for p in phases)
    add(MeasurementRecord(
        name="passband_phase_residual",
        method="multitone phase after declared-latency alignment, wrapped to +/-pi",
        sample_rate=fs, num_samples=n_mt, value=phase_res,
        tolerance=tol.phase_residual_rad,
        resolution=(pb_tones[-1] - pb_tones[0]) / (len(pb_tones) - 1),
        passed=phase_res <= tol.phase_residual_rad,
        details={"residuals_rad": [round(p, 6) for p in phases]}))

    # ---- 5. stopband leakage (stopband multitone in -> out) -------------
    sb_tones = _band_tones(spec.stopband[0], spec.stopband[1], 8)
    x_sb = multitone(sb_tones, fs, n_mt, seed=cfg.seed + 1)
    y_sb = run_blocks(impl_factory(), x_sb, [n_mt])
    y_sb_al = y_sb[declared : declared + n_mt]
    worst_ratio = 0.0
    for f in sb_tones:
        a_in, _, _ = tone_fit(x_sb, f, fs)
        a_out, _, _ = tone_fit(y_sb_al, f, fs)
        worst_ratio = max(worst_ratio, a_out / max(a_in, 1e-300))
    attenuation_db = -to_db(worst_ratio)
    add(MeasurementRecord(
        name="stopband_leakage",
        method="stopband multitone; max output/input tone amplitude ratio",
        sample_rate=fs, num_samples=n_mt, value=to_db(worst_ratio),
        tolerance=-tol.stopband_attenuation_db,
        resolution=(sb_tones[-1] - sb_tones[0]) / (len(sb_tones) - 1),
        passed=attenuation_db >= tol.stopband_attenuation_db,
        details={"attenuation_db": round(attenuation_db, 3)}))

    # ---- 6. stepped-sweep response grid (no full-band claim) ------------
    sweep_freqs = log_spaced(
        max(spec.passband[0], 20.0), min(0.49 * fs, spec.stopband[1]), cfg.sweep_steps)
    m_step = cfg.sweep_samples_per_step
    x_sw = stepped_sweep(sweep_freqs, fs, m_step)
    y_sw = run_blocks(impl_factory(), x_sw, [len(x_sw)])
    settle = min(m_step // 2, max(64, 2 * declared + 16))
    fit_len = m_step - settle
    sweep_gains = []
    for i, f in enumerate(sweep_freqs):
        seg_in = x_sw[i * m_step + settle : (i + 1) * m_step]
        seg_out = y_sw[i * m_step + settle + declared : (i + 1) * m_step + declared]
        a_in, _, _ = tone_fit(seg_in, f, fs)
        a_out, _, _ = tone_fit(seg_out, f, fs)
        sweep_gains.append(a_out / max(a_in, 1e-300))
    pb_sweep = [g for g, f in zip(sweep_gains, sweep_freqs)
                if spec.passband[0] <= f <= spec.passband[1]]
    sweep_dev = max((abs(g - 1.0) for g in pb_sweep), default=0.0)
    spacing = [math.log(sweep_freqs[i + 1] / sweep_freqs[i]) for i in range(len(sweep_freqs) - 1)]
    res_hz = (sweep_freqs[-1] - sweep_freqs[0]) / (len(sweep_freqs) - 1)
    add(MeasurementRecord(
        name="sweep_passband_grid",
        method="stepped-sine sweep, per-step least-squares fit after settling; "
               "grid points only, no inter-point guarantee",
        sample_rate=fs, num_samples=len(x_sw), value=sweep_dev,
        tolerance=tol.passband_deviation, resolution=res_hz,
        passed=sweep_dev <= tol.passband_deviation,
        details={"num_grid_points": len(sweep_freqs),
                 "passband_gains": [round(g, 6) for g in pb_sweep]}))

    # ---- 7. FIR conservative full-band bound (if coefficients exposed) --
    if coeffs is not None:
        pb_bounds = fir_band_bounds(coeffs, fs, spec.passband[0], spec.passband[1])
        sb_bounds = fir_band_bounds(coeffs, fs, spec.stopband[0], spec.stopband[1])
        pb_dev_bound = max(abs(pb_bounds.upper_bound - 1.0), abs(1.0 - pb_bounds.lower_bound))
        sb_atten_bound_db = -to_db(sb_bounds.upper_bound)
        ok = (pb_dev_bound <= tol.passband_deviation
              and sb_atten_bound_db >= tol.stopband_attenuation_db)
        add(MeasurementRecord(
            name="fir_conservative_bound",
            method="coefficient grid + demodulated derivative bound "
                   "(linear phase removed); valid between grid points",
            sample_rate=fs, num_samples=len(coeffs), value=pb_dev_bound,
            tolerance=tol.passband_deviation,
            resolution=pb_bounds.grid_spacing_hz, passed=ok,
            details={
                "passband_deviation_bound": round(pb_dev_bound, 6),
                "stopband_attenuation_bound_db": round(sb_atten_bound_db, 3),
                "between_grid_margin": round(pb_bounds.between_grid_margin, 6),
                "grid_points": pb_bounds.grid_points}))
        report.full_band_claim = (
            "certified from FIR coefficients: passband deviation <= "
            f"{pb_dev_bound:.4g}, stopband attenuation >= {sb_atten_bound_db:.3g} dB "
            f"everywhere (grid {pb_bounds.grid_points} points, "
            f"between-grid margin {pb_bounds.between_grid_margin:.3g})")
    else:
        report.full_band_claim = (
            "not certified: no coefficients exposed; measurements are valid only "
            "at the listed frequencies with the stated resolution")

    # ---- 8. near-Nyquist measurement (interpretation check) -------------
    nyq_tones = [0.45 * fs, 0.4875 * fs]
    x_ny = multitone(nyq_tones, fs, n_mt, seed=cfg.seed + 2)
    y_ny = run_blocks(impl_factory(), x_ny, [n_mt])
    y_ny_al = y_ny[declared : declared + n_mt]
    nyq_gains, nyq_ok = [], True
    for f in nyq_tones:
        a_in, _, _ = tone_fit(x_ny, f, fs)
        a_out, _, _ = tone_fit(y_ny_al, f, fs)
        g = a_out / max(a_in, 1e-300)
        nyq_gains.append(g)
        if coeffs is not None:
            expected = abs(fir_response(coeffs, f, fs))
            if abs(g - expected) > 0.02:
                nyq_ok = False
    add(MeasurementRecord(
        name="near_nyquist_response",
        method="tones at 0.45fs and 0.4875fs, conditioned least-squares fit"
               + ("; checked against coefficient response" if coeffs else ""),
        sample_rate=fs, num_samples=n_mt, value=max(nyq_gains),
        tolerance=None, resolution=None, passed=nyq_ok,
        details={"frequencies_hz": nyq_tones,
                 "gains": [round(g, 6) for g in nyq_gains]}))

    # ---- 9/10. counterexample search: block splits & phase shifts -------
    ce_block = search_block_inconsistency(
        impl_factory, cfg.probe_length, fs, tol.block_consistency,
        trials=cfg.block_trials, seed=cfg.seed * 7 + 1)
    ce_shift = search_phase_misalignment(
        impl_factory, cfg.probe_length, fs, tol.block_consistency,
        seed=cfg.seed * 7 + 2)
    for ce in (ce_block, ce_shift):
        if ce is not None:
            report.counterexamples.append(ce)
    add(MeasurementRecord(
        name="block_consistency",
        method=f"random block-partition search ({cfg.block_trials} trials) + ddmin shrink",
        sample_rate=fs, num_samples=cfg.probe_length,
        value=None if ce_block is None else ce_block.max_error,
        tolerance=tol.block_consistency, resolution=None,
        passed=ce_block is None,
        details={} if ce_block is None else {"shrunk_partition": ce_block.partition}))
    add(MeasurementRecord(
        name="shift_invariance",
        method="shifted-probe search over k=1..32 samples + minimal-shift shrink",
        sample_rate=fs, num_samples=cfg.probe_length,
        value=None if ce_shift is None else ce_shift.max_error,
        tolerance=tol.block_consistency, resolution=None,
        passed=ce_shift is None,
        details={} if ce_shift is None else {"minimal_shift": ce_shift.shift}))
    return report
