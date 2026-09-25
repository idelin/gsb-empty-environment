"""Known-bad implementations injected to validate the verifier itself.

Each class violates exactly one family of checks, so the test-suite can
assert that the verifier detects every injected defect.
"""
from __future__ import annotations

from .reference import StreamingFIR, design_lowpass


class StateResetFIR(StreamingFIR):
    """Forgets its state at every chunk boundary (chunk-inconsistent)."""

    def process(self, chunk):
        self._hist = []
        return super().process(chunk)

    def finalize(self):
        self._finalized = True
        return []


class ChunkDelayFIR(StreamingFIR):
    """Loses one sample of information at every chunk boundary."""

    def reset(self):
        super().reset()
        self._chunks = 0

    def process(self, chunk):
        self._chunks += 1
        out = super().process(chunk)
        if self._chunks > 1 and out:
            out[0] = 0.0
        return out


class AbsoluteIndexFIR(StreamingFIR):
    """Time-varying: zeroes output at absolute indices divisible by period.
    Chunk-invariant but not shift-invariant (phase misalignment)."""

    def __init__(self, h, period: int = 257):
        super().__init__(h)
        self.period = int(period)

    def process(self, chunk):
        base = len(self._hist)
        out = super().process(chunk)
        for i in range(len(out)):
            if (base + i) % self.period == 0:
                out[i] = 0.0
        return out


class GainErrorFIR(StreamingFIR):
    """Scales every output sample by a constant gain error."""

    def __init__(self, h, gain: float):
        super().__init__(h)
        self.gain = float(gain)

    def process(self, chunk):
        return [self.gain * v for v in super().process(chunk)]


class LatencyLiarFIR(StreamingFIR):
    """Declares zero latency while actually delaying by (M-1)/2."""

    def __init__(self, h):
        super().__init__(h, latency_samples=0.0)


def make_underspec_stopband(spec) -> StreamingFIR:
    """Too few taps: stopband attenuation far below the requirement."""
    cutoff = 0.5 * (spec.passband[1] + spec.stopband[0])
    return StreamingFIR(design_lowpass(21, cutoff, spec.sample_rate))


def make_ripply_passband(spec) -> StreamingFIR:
    """Rectangular-window design with the cutoff placed just above the
    passband edge, so Gibbs ripple lands inside the passband."""
    cutoff = spec.passband[1] + 0.05 * (spec.stopband[0] - spec.passband[1])
    return StreamingFIR(design_lowpass(101, cutoff, spec.sample_rate,
                                       window="rectangular"))
