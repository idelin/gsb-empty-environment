"""Multichannel dynamics processor.

Wires envelope detectors -> sidechain combining -> static curve -> gain
smoother -> sample gain. The static curve is hot-swappable: set_curve()
schedules a crossfade that starts exactly at a given absolute sample index
and blends old/new curve gains in the dB domain, so neither the total
latency (always LATENCY_SAMPLES) nor the applied gain ever jumps.

Envelope state policy on curve switch: PRESERVED, not migrated. Detectors
and gain smoothers keep running with their current state; only the static
curve (and its crossfade) changes. This is verified in
tests/test_hotreload.py.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import List, Optional, Sequence, Tuple

from .curve import CompiledCurve
from .envelope import (GainSmoother, amplitude_to_db, detector_states,
                       make_detector, restore_detector_states)

#: Declared end-to-end latency of the processor, in samples. All stages are
#: causal zero-lookahead filters, so latency is 0 and hot curve swaps never
#: change it.
LATENCY_SAMPLES = 0


@dataclass(frozen=True)
class DetectorConfig:
    kind: str = "peak"          # 'peak' | 'rms'
    attack_ms: float = 5.0
    release_ms: float = 100.0
    window_ms: float = 20.0     # rms only


@dataclass(frozen=True)
class SmootherConfig:
    attack_ms: float = 1.0
    release_ms: float = 50.0


@dataclass(frozen=True)
class SidechainConfig:
    """How per-channel detector envelopes combine into gain decisions.

    mode='max':        one linked gain from the loudest channel envelope.
    mode='mean':       one linked gain from the mean envelope.
    mode='weighted':   one linked gain from the weighted sum of envelopes.
    mode='independent': each channel gets its own gain (no linking).
    """
    mode: str = "max"
    weights: Optional[Tuple[float, ...]] = None

    def __post_init__(self):
        if self.mode not in ("max", "mean", "weighted", "independent"):
            raise ValueError(f"unknown sidechain mode: {self.mode!r}")


@dataclass
class _Fade:
    old_curve: CompiledCurve
    new_curve: CompiledCurve
    start: int                  # absolute sample index of first blended sample
    length: int                 # crossfade length in samples (>= 1)


class DynamicsProcessor:
    """Stateful multichannel compressor/expander."""

    LATENCY_SAMPLES = LATENCY_SAMPLES

    def __init__(self, sample_rate: float, num_channels: int,
                 curve: CompiledCurve,
                 detector: DetectorConfig = DetectorConfig(),
                 smoother: SmootherConfig = SmootherConfig(),
                 sidechain: SidechainConfig = SidechainConfig()):
        if num_channels < 1:
            raise ValueError("num_channels must be >= 1")
        if sidechain.mode == "weighted":
            if sidechain.weights is None or len(sidechain.weights) != num_channels:
                raise ValueError("weighted sidechain needs one weight per channel")
        self.sample_rate = float(sample_rate)
        self.num_channels = num_channels
        self.sidechain = sidechain
        self._curve = curve
        self._detectors = [make_detector(detector.kind, self.sample_rate,
                                         detector.attack_ms, detector.release_ms,
                                         detector.window_ms)
                           for _ in range(num_channels)]
        # linked modes share one gain smoother; independent mode uses one per channel
        n_smooth = num_channels if sidechain.mode == "independent" else 1
        self._smoothers = [GainSmoother(self.sample_rate, smoother.attack_ms,
                                        smoother.release_ms)
                           for _ in range(n_smooth)]
        self._sample = 0                 # absolute sample index of next sample
        self._fade: Optional[_Fade] = None
        self._pending: List[Tuple[CompiledCurve, int, int]] = []

    # ------------------------------------------------------------------
    @property
    def latency_samples(self) -> int:
        """Total processor latency in samples (constant across curve swaps)."""
        return LATENCY_SAMPLES

    @property
    def curve(self) -> CompiledCurve:
        return self._curve

    @property
    def samples_processed(self) -> int:
        return self._sample

    # ------------------------------------------------------------------
    def set_curve(self, new_curve: CompiledCurve, at_sample: int,
                  fade_samples: int) -> None:
        """Schedule a hot curve swap with a dB-domain crossfade.

        The crossfade starts exactly at absolute sample index `at_sample`
        (clamped to the current position if already past) and ramps linearly
        in dB over `fade_samples` samples. Multiple swaps are queued and run
        sequentially so gains never jump. Envelope/smoother state is
        preserved (documented policy), latency is unaffected.
        """
        if fade_samples < 1:
            raise ValueError("fade_samples must be >= 1")
        self._pending.append((new_curve, at_sample, fade_samples))

    def _gain_target_db(self, level_db: float) -> float:
        f = self._fade
        if f is None:
            return self._curve.gain_db(level_db)
        pos = self._sample - f.start
        if pos < 0:
            return f.old_curve.gain_db(level_db)
        if pos >= f.length:
            return f.new_curve.gain_db(level_db)
        a = (pos + 1) / f.length          # 0 < a <= 1, continuous from old gain
        return ((1.0 - a) * f.old_curve.gain_db(level_db)
                + a * f.new_curve.gain_db(level_db))

    def _advance_fade(self) -> None:
        if self._fade is not None and self._sample - self._fade.start >= self._fade.length:
            self._curve = self._fade.new_curve
            self._fade = None
        if self._fade is None and self._pending:
            new_curve, at_sample, length = self._pending.pop(0)
            start = max(at_sample, self._sample)
            self._fade = _Fade(self._curve, new_curve, start, length)

    # ------------------------------------------------------------------
    def _combined_level_db(self, envs: Sequence[float]) -> float:
        mode = self.sidechain.mode
        if mode == "max":
            return amplitude_to_db(max(envs))
        if mode == "mean":
            return amplitude_to_db(sum(envs) / len(envs))
        if mode == "weighted":
            return amplitude_to_db(sum(w * e for w, e in zip(self.sidechain.weights, envs)))
        raise AssertionError("independent mode has no combined level")

    def process_block(self, block: Sequence[Sequence[float]]) -> List[List[float]]:
        """Process a block of interleaved-per-channel samples.

        `block[ch][n]`; returns a new list of per-channel output lists.
        Per-sample work is independent of block boundaries, so any blocking
        of the same stream yields bit-identical output.
        """
        if len(block) != self.num_channels:
            raise ValueError("block channel count mismatch")
        n = len(block[0])
        if any(len(ch) != n for ch in block):
            raise ValueError("ragged block")
        out = [[0.0] * n for _ in range(self.num_channels)]
        linked = self.sidechain.mode != "independent"
        for i in range(n):
            self._advance_fade()
            envs = [self._detectors[ch].update(abs(block[ch][i]))
                    for ch in range(self.num_channels)]
            if linked:
                gain_db = self._smoothers[0].update(
                    self._gain_target_db(self._combined_level_db(envs)))
                lin = math.pow(10.0, gain_db / 20.0)
                for ch in range(self.num_channels):
                    out[ch][i] = block[ch][i] * lin
            else:
                for ch in range(self.num_channels):
                    gain_db = self._smoothers[ch].update(
                        self._gain_target_db(amplitude_to_db(envs[ch])))
                    out[ch][i] = block[ch][i] * math.pow(10.0, gain_db / 20.0)
            self._sample += 1
        return out

    def process(self, channel_samples: Sequence[Sequence[float]]) -> List[List[float]]:
        return self.process_block(channel_samples)

    # ------------------------------------------------------------------
    def get_state(self) -> dict:
        """Snapshot of the full runtime state (curves are immutable, shared)."""
        return {
            "sample": self._sample,
            "curve": self._curve,
            "detectors": detector_states(self._detectors),
            "smoothers": [s.get_state() for s in self._smoothers],
            "fade": None if self._fade is None else (
                self._fade.old_curve, self._fade.new_curve,
                self._fade.start, self._fade.length),
            "pending": list(self._pending),
        }

    def set_state(self, state: dict) -> None:
        """Restore a snapshot taken by get_state(); processing continues
        bit-identically to the original run."""
        self._sample = int(state["sample"])
        self._curve = state["curve"]
        restore_detector_states(self._detectors, list(state["detectors"]))
        for s, v in zip(self._smoothers, state["smoothers"]):
            s.set_state(v)
        fade = state["fade"]
        self._fade = None if fade is None else _Fade(fade[0], fade[1], fade[2], fade[3])
        self._pending = list(state["pending"])
