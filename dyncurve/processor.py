"""Stateful multi-channel dynamics processor.

Responsibilities are split:
    CompiledCurve   static, stateless level->gain map (dyncurve.curve)
    detector        envelope follower (dyncurve.envelope)
    GainSmoother    attack/release smoothing of the gain trajectory
    DynamicsProcessor  wiring: sidechain routing, hot reload, block I/O

Declared latency: ``DynamicsProcessor.latency_samples`` == 0 (no
lookahead).  Hot reload never changes the latency and, with
``fade_samples`` > 0, never produces a gain step.

Envelope state policy on curve switch (explicit, verified by tests):
    EnvelopePolicy.RETAIN  detector and smoother states carry over
                           unchanged; the gain trajectory stays continuous.
    EnvelopePolicy.RESET   detector states are re-initialised to silence;
                           the smoother gain state is STILL retained so the
                           applied gain cannot jump.
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .curve import CompiledCurve, amp_to_db, db_to_amp
from .envelope import GainSmoother, PeakDetector, RMSDetector


class EnvelopePolicy(Enum):
    RETAIN = "retain"
    RESET = "reset"


@dataclass(frozen=True)
class DetectorConfig:
    kind: str = "peak"          # "peak" | "rms"
    attack_s: float = 0.005
    release_s: float = 0.050


@dataclass(frozen=True)
class SmootherConfig:
    attack_s: float = 0.005
    release_s: float = 0.100


@dataclass(frozen=True)
class SidechainConfig:
    # None: each channel is its own sidechain source.
    # Otherwise: indices into the sidechain block passed to process_block
    # (or into the main block when no separate sidechain block is given).
    channels: tuple | None = None
    link: str = "linked"        # "linked" (max envelope) | "independent"


class DynamicsProcessor:
    latency_samples = 0  # declared total latency; hot reload never changes it

    def __init__(self, num_channels, sample_rate, curve: CompiledCurve,
                 detector: DetectorConfig = DetectorConfig(),
                 smoother: SmootherConfig = SmootherConfig(),
                 sidechain: SidechainConfig = SidechainConfig()):
        if num_channels < 1:
            raise ValueError("num_channels must be >= 1")
        self.num_channels = num_channels
        self.sample_rate = float(sample_rate)
        self._det_cfg = detector
        self._sm_cfg = smoother
        self._sc_cfg = sidechain
        self._curve = curve
        self._old_curve = None
        self._fade_remaining = 0
        self._fade_total = 0
        self.samples_processed = 0
        self._build_state()

    # -- construction helpers ---------------------------------------------
    def _make_detector(self):
        cls = PeakDetector if self._det_cfg.kind == "peak" else RMSDetector
        return cls(self._det_cfg.attack_s, self._det_cfg.release_s,
                   self.sample_rate)

    def _make_smoother(self):
        return GainSmoother(self._sm_cfg.attack_s, self._sm_cfg.release_s,
                            self.sample_rate)

    def _build_state(self):
        n_sc = (self.num_channels if self._sc_cfg.channels is None
                else len(self._sc_cfg.channels))
        if self._sc_cfg.link == "linked":
            self._detectors = [self._make_detector() for _ in range(n_sc)]
            self._smoothers = [self._make_smoother()]
        elif self._sc_cfg.link == "independent":
            self._detectors = [self._make_detector()
                               for _ in range(self.num_channels)]
            self._smoothers = [self._make_smoother()
                               for _ in range(self.num_channels)]
        else:
            raise ValueError(f"unknown link mode {self._sc_cfg.link!r}")

    # -- hot reload ---------------------------------------------------------
    def update_curve(self, curve: CompiledCurve, fade_samples: int = 0,
                     envelope_policy: EnvelopePolicy = EnvelopePolicy.RETAIN):
        """Swap the static curve.  Takes effect at the first sample of the
        next process_block call (the declared switch point); the crossfade
        runs for ``fade_samples`` samples from that point.  Latency is
        unchanged (always 0)."""
        if fade_samples < 0:
            raise ValueError("fade_samples must be >= 0")
        self._old_curve = self._curve
        self._curve = curve
        self._fade_total = int(fade_samples)
        self._fade_remaining = int(fade_samples)
        if envelope_policy is EnvelopePolicy.RESET:
            for det in self._detectors:
                det.set_state(0.0)
            # smoother gain state deliberately retained: no gain jump
        elif envelope_policy is not EnvelopePolicy.RETAIN:
            raise ValueError(f"unknown envelope policy {envelope_policy!r}")

    # -- processing -----------------------------------------------------------
    def _target_gain_db(self, level_db: float) -> float:
        target = self._curve.gain_db(level_db)
        if self._fade_remaining > 0:
            frac = (self._fade_total - self._fade_remaining + 1) / self._fade_total
            old = self._old_curve.gain_db(level_db)
            target = (1.0 - frac) * old + frac * target
            self._fade_remaining -= 1
            if self._fade_remaining == 0:
                self._old_curve = None
        return target

    def process_block(self, block, sidechain_block=None):
        """Process one block.  ``block``: list of per-channel sample lists.
        Returns new per-channel lists.  Block size may vary freely; the
        result is identical to any other blocking of the same stream."""
        n_ch = self.num_channels
        if len(block) != n_ch:
            raise ValueError("block channel count mismatch")
        n = len(block[0])
        if any(len(ch) != n for ch in block):
            raise ValueError("ragged block")
        if self._sc_cfg.channels is not None:
            src = sidechain_block if sidechain_block is not None else block
            sc_signals = [src[i] for i in self._sc_cfg.channels]
        else:
            sc_signals = block

        out = [[0.0] * n for _ in range(n_ch)]
        if self._sc_cfg.link == "linked":
            dets, smoother = self._detectors, self._smoothers[0]
            for i in range(n):
                env = max(det.next(sig[i]) for det, sig in zip(dets, sc_signals))
                g_db = smoother.next(self._target_gain_db(amp_to_db(env)))
                g = db_to_amp(g_db)
                for ch in range(n_ch):
                    out[ch][i] = block[ch][i] * g
        else:
            for ch in range(n_ch):
                det, smoother = self._detectors[ch], self._smoothers[ch]
                sig = sc_signals[ch]
                src_ch, dst = block[ch], out[ch]
                for i in range(n):
                    g_db = smoother.next(
                        self._target_gain_db(amp_to_db(det.next(sig[i]))))
                    dst[i] = src_ch[i] * db_to_amp(g_db)
        self.samples_processed += n
        return out

    # -- state snapshot -------------------------------------------------------
    def get_state(self):
        return {
            "detectors": [d.get_state() for d in self._detectors],
            "smoothers": [s.get_state() for s in self._smoothers],
            "fade_remaining": self._fade_remaining,
            "fade_total": self._fade_total,
            "samples_processed": self.samples_processed,
        }

    def set_state(self, state):
        for det, s in zip(self._detectors, state["detectors"]):
            det.set_state(s)
        for sm, s in zip(self._smoothers, state["smoothers"]):
            sm.set_state(s)
        self._fade_remaining = state["fade_remaining"]
        self._fade_total = state["fade_total"]
        self.samples_processed = state["samples_processed"]
        if self._fade_remaining == 0:
            self._old_curve = None
