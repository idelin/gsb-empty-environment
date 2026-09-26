"""Local fixtures: deterministic sample arrays + sample rate. No audio devices."""
from __future__ import annotations

from .signals import impulse, log_chirp, multitone, probe_signal

SAMPLE_RATE = 8000.0  # Hz; all fixtures share this rate

_FIXTURE_SEED = 20260926


def fixture(name: str, n: int = 4096) -> tuple[list[float], float]:
    """Return (samples, sample_rate) for a named local fixture."""
    fs = SAMPLE_RATE
    if name == "impulse":
        return impulse(n), fs
    if name == "chirp":
        return log_chirp(n, fs, 100.0, 0.45 * fs), fs
    if name == "multitone_pass":
        return multitone([200.0, 400.0, 700.0, 950.0], fs, n, seed=_FIXTURE_SEED), fs
    if name == "probe":
        return probe_signal(n, fs, _FIXTURE_SEED), fs
    raise KeyError(f"unknown fixture {name!r}; available: impulse, chirp, "
                   "multitone_pass, probe")
