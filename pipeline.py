"""Multiband pipeline: crossover -> per-band compression -> limiter.

* All filter coefficients are supplied explicitly by the caller.
* Recombination compensates differing band group delays (see Crossover).
* Channels share control gain per band and in the limiter (linked), while
  audio paths stay independent.
* Unknown (NaN) samples are marked and propagated through every stage.
* The pipeline reports per-stage latency, gain trajectories and over-limit
  evidence; nothing is ever hard-clipped.
"""

import math

from dsp_crossover import Crossover
from dsp_limiter import LookaheadLimiter


def _linked(values):
    """Shared control gain: most conservative wins; unknown taints all."""
    if any(math.isnan(v) for v in values):
        return float("nan")
    return min(values)


class MultibandProcessor:
    def __init__(self, sample_rate, bands, compressors, limiter_params,
                 channels=2):
        if len(bands) != len(compressors):
            raise ValueError("need one compressor per band")
        self.sample_rate = sample_rate
        self.channels = channels
        self.bands = list(bands)
        self.compressors = list(compressors)
        self.crossovers = [Crossover(self.bands) for _ in range(channels)]
        self.limiter = LookaheadLimiter(
            sample_rate, channels, **limiter_params
        )
        self.trajectories = {
            "band:" + c.name: [] for c in self.compressors
        }
        self.trajectories["limiter"] = []
        self.trajectories["limiter_threshold_db"] = []
        self.trajectories["limiter_required"] = []

    def stage_latencies(self):
        """Per-stage latency in samples (compressors are zero-latency gain)."""
        return {
            "crossover": self.crossovers[0].latency_samples,
            "compressors": 0,
            "limiter": self.limiter.latency_samples,
        }

    @property
    def latency_samples(self):
        return sum(self.stage_latencies().values())

    @property
    def evidence(self):
        return self.limiter.evidence

    def process_block(self, blocks):
        """Process one block per channel; returns aligned output blocks."""
        n = len(blocks[0])
        if len(blocks) != self.channels or any(len(b) != n for b in blocks):
            raise ValueError("expected one equal-length block per channel")
        band_blocks = [xo.process(block) for xo, block in zip(self.crossovers, blocks)]
        mixed = [[0.0] * n for _ in range(self.channels)]
        for b, comp in enumerate(self.compressors):
            desired = [
                comp.desired_gains(ch, band_blocks[ch][b])
                for ch in range(self.channels)
            ]
            traj = self.trajectories["band:" + comp.name]
            for i in range(n):
                g = _linked([desired[ch][i] for ch in range(self.channels)])
                traj.append(g)
                for ch in range(self.channels):
                    mixed[ch][i] += band_blocks[ch][b][i] * g
        outs, gains, thrs, g_reqs = self.limiter.process_block(mixed)
        self.trajectories["limiter"].extend(gains)
        self.trajectories["limiter_threshold_db"].extend(thrs)
        self.trajectories["limiter_required"].extend(g_reqs)
        return outs

    def flush(self):
        outs, gains, thrs, g_reqs = self.limiter.flush()
        self.trajectories["limiter"].extend(gains)
        self.trajectories["limiter_threshold_db"].extend(thrs)
        self.trajectories["limiter_required"].extend(g_reqs)
        return outs
