"""Top-level verification orchestration.

verify() runs the full battery against an unknown streamable
implementation and returns a VerificationReport.  Measured response
error and finite test resolution are reported as separate fields; a
full-band claim is only ever made via the FIR coefficient certificate.
"""
from __future__ import annotations

import os
from dataclasses import asdict

from .dsp import fft, next_pow2
from .fir_bounds import certify_fir
from .fixtures import generate_fixtures
from .metrics import to_db, quadrature_tone, estimate_delay
from .report import CheckResult, VerificationReport
from .shrink import shrink_chunk_counterexample
from .signals import bin_grid
from .streaming import (run_streaming, find_chunk_counterexample,
                        find_shift_counterexample)


def verify(impl_factory, spec, *, fir_coefficients=None, fixtures=None,
           seed: int = 1234, chunk_trials: int = 24, max_shift: int = 8,
           artifact_dir: str | None = None) -> VerificationReport:
    spec.validate()
    fx = fixtures or generate_fixtures(
        spec.sample_rate, spec.passband, spec.stopband,
        guard_hz=spec.nyquist_guard_hz)
    fs = spec.sample_rate
    checks = []
    artifacts = []

    def run(x):
        return run_streaming(impl_factory(), x, [])

    def save_artifact(name, obj):
        if artifact_dir is None:
            return None
        os.makedirs(artifact_dir, exist_ok=True)
        path = os.path.join(artifact_dir, name)
        obj.save(path)
        artifacts.append(path)
        return path

    n_seg = fx.params["analysis_segment"]

    # 1. impulse response: delay and support
    x_imp = fx.signals["impulse"]
    y_imp = run(x_imp)
    delay = estimate_delay(x_imp, y_imp)
    peak = max((abs(v) for v in y_imp), default=0.0)
    thresh = peak * 1e-6
    support = 0
    for i, v in enumerate(y_imp):
        if abs(v) > thresh:
            support = i + 1
    checks.append(CheckResult(
        name="impulse_response", passed=True,
        method=("impulse excitation; delay = parabolic-interpolated "
                "cross-correlation peak; support at -120 dB of peak"),
        n_samples=len(x_imp),
        measured={"delay_samples": delay, "response_support": support,
                  "peak_amplitude": peak},
        tolerances={}))

    # 2. declared latency vs measured
    declared = float(impl_factory().latency_samples)
    lat_err = abs(delay - declared)
    lat_ok = (lat_err <= spec.latency_tolerance_samples
              and delay <= spec.max_latency_samples)
    checks.append(CheckResult(
        name="declared_latency", passed=lat_ok,
        method="impulse-measured delay vs impl.latency_samples",
        n_samples=len(x_imp),
        measured={"measured_delay": delay, "declared_latency": declared,
                  "abs_error": lat_err},
        tolerances={"latency_tolerance_samples": spec.latency_tolerance_samples,
                    "max_latency_samples": spec.max_latency_samples}))

    # 3. swept-sine response on a grid (measured error AND resolution)
    x_ch = fx.signals["chirp"]
    y_ch = run(x_ch)
    n_fft = next_pow2(len(y_ch))
    spec_x = fft(list(x_ch) + [0.0] * (n_fft - len(x_ch)))
    spec_y = fft(list(y_ch) + [0.0] * (n_fft - len(y_ch)))
    f0, f1 = fx.params["chirp_band"]

    def hmag(f):
        k = int(round(f * n_fft / fs))
        denom = abs(spec_x[k])
        return abs(spec_y[k]) / denom if denom > 1e-12 else float("nan")

    pass_fs = bin_grid(max(f0, spec.passband[0]), spec.passband[1], n_fft, fs)
    stop_fs = bin_grid(spec.stopband[0], min(spec.stopband[1], f1), n_fft, fs)
    pass_mags = [hmag(f) for f in pass_fs]
    stop_mags = [hmag(f) for f in stop_fs]
    ripple_db = max((abs(to_db(m)) for m in pass_mags), default=0.0)
    atten_db = -to_db(max(stop_mags)) if stop_mags else float("inf")
    swept_ok = (ripple_db <= spec.passband_ripple_db
                and atten_db >= spec.stopband_atten_db)
    checks.append(CheckResult(
        name="swept_response", passed=swept_ok,
        method=("log-chirp deconvolution H=Y/X, magnitude only "
                "(delay-invariant); one-sided grid"),
        n_samples=len(x_ch),
        measured={"passband_ripple_db": ripple_db,
                  "stopband_atten_db": atten_db,
                  "grid_spacing_hz": fs / n_fft,
                  "grid_points_passband": len(pass_fs),
                  "grid_points_stopband": len(stop_fs)},
        tolerances={"passband_ripple_db": spec.passband_ripple_db,
                    "stopband_atten_db": spec.stopband_atten_db},
        detail=(f"finite resolution: |H| sampled every {fs / n_fft:.3f} Hz; "
                "behaviour between grid points is NOT certified by this "
                "measurement (see fir_certificate)")))

    # 4. multitone passband deviation (quadrature projection, window-free)
    checks.append(_multitone_check(
        name="multitone_passband", run=run, fx=fx, fs=fs, n_seg=n_seg,
        tone_key="passband", signal_key="passband_multitone",
        limit_db=spec.passband_ripple_db, is_passband=True))

    # 5. stopband leakage (multitone in the stopband)
    checks.append(_multitone_check(
        name="stopband_leakage", run=run, fx=fx, fs=fs, n_seg=n_seg,
        tone_key="stopband", signal_key="stopband_multitone",
        limit_db=spec.stopband_atten_db, is_passband=False))

    # 6. near-Nyquist edge tones
    edge_tones = fx.tone_sets["nyquist_edge"]
    x_stop = fx.signals["stopband_multitone"]
    y_stop = run(x_stop)
    start = len(x_stop) - n_seg
    worst_edge = -float("inf")
    for f in edge_tones:
        a_y, _ = quadrature_tone(y_stop, f, fs, start, n_seg)
        a_x, _ = quadrature_tone(x_stop, f, fs, start, n_seg)
        worst_edge = max(worst_edge, to_db(a_y / a_x))
    checks.append(CheckResult(
        name="nyquist_edge", passed=worst_edge <= -spec.stopband_atten_db,
        method=("tones placed below fs/2 with "
                f"{spec.nyquist_guard_hz} Hz guard; quadrature projection "
                "at the true frequency (no negative-frequency aliasing)"),
        n_samples=len(x_stop),
        measured={"worst_leakage_db": worst_edge,
                  "edge_tones_hz": list(edge_tones)},
        tolerances={"stopband_atten_db": spec.stopband_atten_db}))

    # 7. chunk invariance: random partition search + shrinking
    probe = fx.signals["stream_probe"]
    cex, worst = find_chunk_counterexample(
        impl_factory, probe, chunk_trials, seed, spec.chunk_tolerance)
    if cex is None:
        checks.append(CheckResult(
            name="chunk_invariance", passed=True,
            method=(f"{chunk_trials} random partitions (2-8 chunks, "
                    f"seed={seed}) vs single-chunk reference"),
            n_samples=len(probe),
            measured={"max_abs_diff": worst},
            tolerances={"chunk_tolerance": spec.chunk_tolerance}))
    else:
        shrunk = shrink_chunk_counterexample(impl_factory, cex)
        path = save_artifact("chunk_counterexample.json", shrunk)
        checks.append(CheckResult(
            name="chunk_invariance", passed=False,
            method="random partition search + greedy/ddmin shrinking",
            n_samples=len(shrunk.input),
            measured={"max_abs_diff": shrunk.max_abs_diff,
                      "chunks": len(shrunk.boundaries) + 1,
                      "first_diff_index": shrunk.first_diff_index},
            tolerances={"chunk_tolerance": spec.chunk_tolerance},
            detail=(f"shrunk replayable counterexample saved to {path}"
                    if path else "shrunk counterexample retained in memory")))

    # 8. shift invariance (phase / time alignment)
    sce, worst_s = find_shift_counterexample(
        impl_factory, probe, max_shift, spec.shift_tolerance)
    if sce is None:
        checks.append(CheckResult(
            name="shift_invariance", passed=True,
            method=f"shifts 1..{max_shift} samples vs shifted reference",
            n_samples=len(probe),
            measured={"max_abs_diff": worst_s},
            tolerances={"shift_tolerance": spec.shift_tolerance}))
    else:
        path = save_artifact("shift_counterexample.json", sce)
        checks.append(CheckResult(
            name="shift_invariance", passed=False,
            method="ascending shift search (first failure is minimal)",
            n_samples=len(sce.input),
            measured={"shift": sce.shift, "max_abs_diff": sce.max_abs_diff,
                      "first_diff_index": sce.first_diff_index},
            tolerances={"shift_tolerance": spec.shift_tolerance},
            detail=(f"replayable counterexample saved to {path}"
                    if path else "counterexample retained in memory")))

    # 9. FIR certificate from coefficients (conservative inter-grid bound)
    if fir_coefficients is not None:
        certs = certify_fir(list(fir_coefficients), spec)
        pb, sb = certs["passband"], certs["stopband"]
        cert_ok = pb.status == "certified" and sb.status == "certified"
        checks.append(CheckResult(
            name="fir_certificate", passed=cert_ok,
            method=("coefficient DTFT on adaptively refined grid + "
                    "Lipschitz bound L=(2pi/fs)*sum(n*|h[n]|) between "
                    "grid points"),
            n_samples=len(fir_coefficients),
            measured={
                "passband_status": pb.status,
                "passband_grid_points": pb.grid_points,
                "passband_interval_hz": pb.interval_hz,
                "stopband_status": sb.status,
                "stopband_grid_points": sb.grid_points,
                "stopband_interval_hz": sb.interval_hz,
                "lipschitz_per_hz": pb.lipschitz,
            },
            tolerances={"passband_ripple_db": spec.passband_ripple_db,
                        "stopband_atten_db": spec.stopband_atten_db},
            detail="full-band claim derived from coefficients, not from "
                   "a finite set of measurement points"))

    return VerificationReport(asdict(spec), checks, artifacts)


def _multitone_check(name, run, fx, fs, n_seg, tone_key, signal_key,
                     limit_db, is_passband) -> CheckResult:
    x = fx.signals[signal_key]
    y = run(x)
    start = len(x) - n_seg
    freqs = fx.tone_sets[tone_key]
    ratios = []
    for f in freqs:
        a_y, _ = quadrature_tone(y, f, fs, start, n_seg)
        a_x, _ = quadrature_tone(x, f, fs, start, n_seg)
        ratios.append(a_y / a_x)
    if is_passband:
        value_db = max(abs(to_db(r)) for r in ratios)
        ok = value_db <= limit_db
        measured = {"deviation_db": value_db, "tones": len(freqs)}
        tol = {"passband_ripple_db": limit_db}
    else:
        value_db = max(to_db(r) for r in ratios)
        ok = value_db <= -limit_db
        measured = {"leakage_db": value_db, "tones": len(freqs)}
        tol = {"stopband_atten_db": limit_db}
    return CheckResult(
        name=name, passed=ok,
        method=("bin-aligned multitone; steady-state segment analysed by "
                "quadrature projection (window-free, coherent-gain exact); "
                f"segment={n_seg} samples, resolution={fs / n_seg:.3f} Hz"),
        n_samples=len(x),
        measured=measured,
        tolerances=tol,
        detail=(f"first {start} samples discarded as transient; "
                "amplitude ratios are delay-invariant"))
