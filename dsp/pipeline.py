"""完整流水线:三频带分频(含分带压缩与群延迟补偿) + 前瞻限幅。"""
from __future__ import annotations

from .crossover import Crossover
from .limiter import LookaheadLimiter


class Pipeline:
    def __init__(self, fs, n_channels, band_coeffs, band_params, limiter_params):
        self.fs = fs
        self.n_channels = n_channels
        self.crossover = Crossover(fs, n_channels, band_coeffs, band_params)
        self.limiter = LookaheadLimiter(fs, n_channels, **limiter_params)

    @property
    def latency_report(self):
        stages = {
            "crossover": self.crossover.latency_samples,
            "limiter": self.limiter.latency_samples,
        }
        return {
            "stages": stages,
            "total": sum(stages.values()),
            "sample_rate": self.fs,
        }

    def process(self, block, collect_bands=False):
        mid, xo_telemetry = self.crossover.process(
            block, collect_bands=collect_bands)
        out, lim_telemetry = self.limiter.process(mid)
        telemetry = {
            "latency": self.latency_report,
            "crossover": xo_telemetry,
            "limiter": lim_telemetry,
        }
        return out, telemetry
