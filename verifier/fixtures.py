"""Local deterministic fixtures: sample arrays and the sample rate.

No audio devices are involved.  Every signal is generated from explicit
seeds and parameters; a saved fixture JSON stores those seeds/params so
the exact stimulus set can be regenerated (replayed) at any time.
"""
from __future__ import annotations

import json
from dataclasses import dataclass

from . import signals as sig


def _pick_tones(band, count: int, n_seg: int, fs: float, f_min: float = 0.0):
    lo, hi = band
    lo = max(lo, f_min)
    out = []
    for i in range(count):
        f = lo + (hi - lo) * (i + 0.5) / count
        f = sig.snap_to_bin(f, n_seg, fs)
        if lo <= f <= hi and f not in out:
            out.append(f)
    return out


@dataclass
class FixtureSet:
    sample_rate: float
    seed: int
    signals: dict
    tone_sets: dict
    params: dict

    def to_dict(self):
        return {
            "sample_rate": self.sample_rate,
            "seed": self.seed,
            "tone_sets": self.tone_sets,
            "params": self.params,
        }

    def save(self, path):
        with open(path, "w") as fh:
            json.dump(self.to_dict(), fh, indent=2)

    @classmethod
    def load(cls, path):
        with open(path) as fh:
            doc = json.load(fh)
        p = doc["params"]
        return generate_fixtures(
            doc["sample_rate"], tuple(p["passband"]), tuple(p["stopband"]),
            seed=doc["seed"],
            n_impulse=p["n_impulse"], n_chirp=p["n_chirp"],
            n_multi=p["n_multi"], n_seg=p["n_seg"], n_probe=p["n_probe"],
            n_pass_tones=p["n_pass_tones"], n_stop_tones=p["n_stop_tones"],
            guard_hz=p["guard_hz"],
        )


def generate_fixtures(sample_rate, passband, stopband, seed: int = 20260926, *,
                      n_impulse: int = 2048, n_chirp: int = 8192,
                      n_multi: int = 4096, n_seg: int = 2048,
                      n_probe: int = 1024, n_pass_tones: int = 8,
                      n_stop_tones: int = 8, guard_hz: float = 100.0) -> FixtureSet:
    """Build the deterministic stimulus set.

    Tone frequencies are snapped to bins of the n_seg-point analysis
    segment, so quadrature projection separates them exactly.  All tones
    stay at least guard_hz below fs/2 (near-Nyquist convention).
    """
    fs = float(sample_rate)
    f_nyq = fs / 2.0 - guard_hz
    bin_hz = fs / n_seg
    pass_tones = _pick_tones(passband, n_pass_tones, n_seg, fs, f_min=4.0 * bin_hz)
    stop_hi = min(stopband[1], f_nyq)
    stop_tones = _pick_tones((stopband[0], stop_hi), n_stop_tones, n_seg, fs)
    if not pass_tones or not stop_tones:
        raise ValueError("band too narrow for the requested tone count")
    f0 = max(20.0, bin_hz)
    signals = {
        "impulse": sig.impulse(n_impulse),
        "chirp": sig.log_chirp(n_chirp, fs, f0, f_nyq),
        "passband_multitone": sig.multitone(pass_tones, 0.8, n_multi, fs, seed + 1),
        "stopband_multitone": sig.multitone(stop_tones, 0.8, n_multi, fs, seed + 2),
        "stream_probe": sig.uniform_noise(n_probe, seed + 3),
    }
    tone_sets = {
        "passband": pass_tones,
        "stopband": stop_tones,
        "nyquist_edge": stop_tones[-3:],
    }
    params = {
        "passband": list(passband),
        "stopband": list(stopband),
        "n_impulse": n_impulse,
        "n_chirp": n_chirp,
        "n_multi": n_multi,
        "n_seg": n_seg,
        "n_probe": n_probe,
        "n_pass_tones": n_pass_tones,
        "n_stop_tones": n_stop_tones,
        "guard_hz": guard_hz,
        "analysis_segment": n_seg,
        "chirp_band": [f0, f_nyq],
    }
    return FixtureSet(fs, seed, signals, tone_sets, params)
