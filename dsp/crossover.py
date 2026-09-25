"""三频带 FIR 分频器:显式系数、分带压缩、群延迟补偿重组。

各带滤波器群延迟可不同;重组前每带按 (最大群延迟 - 本带群延迟)
插入补偿延迟,使各带在同一时间原点对齐后求和。
"""
from __future__ import annotations

from .block import Block
from .compressor import BandCompressor
from .fir import DelayLine, FIRFilter


class Crossover:
    def __init__(self, fs, n_channels, band_coeffs, band_params, name="crossover"):
        if len(band_coeffs) != len(band_params):
            raise ValueError("系数与参数的数量必须一致")
        if not band_coeffs:
            raise ValueError("至少一个频带")
        self.name = name
        self.n_channels = n_channels
        self.firs = [
            [FIRFilter(coeffs, name=f"{name}.band{b}.ch{c}")
             for c in range(n_channels)]
            for b, coeffs in enumerate(band_coeffs)
        ]
        self.band_group_delays = [firs[0].group_delay for firs in self.firs]
        self.latency_samples = max(self.band_group_delays)
        self._comp_delays = [
            [DelayLine(self.latency_samples - gd) for _ in range(n_channels)]
            for gd in self.band_group_delays
        ]
        self.compressors = [
            BandCompressor(fs, n_channels, name=f"{name}.comp{b}", **params)
            for b, params in enumerate(band_params)
        ]

    def process(self, block, collect_bands=False):
        if block.n_channels != self.n_channels:
            raise ValueError("通道数不匹配")
        n = block.n_samples
        nch = self.n_channels
        summed = [[0.0] * n for _ in range(nch)]
        summed_valid = [[True] * n for _ in range(nch)]
        band_telemetries = []
        band_outputs = []
        for b in range(len(self.firs)):
            filtered = [
                self.firs[b][c].process(block.channels[c], block.valid[c])
                for c in range(nch)
            ]
            band_block = Block([f[0] for f in filtered], [f[1] for f in filtered])
            compressed, telemetry = self.compressors[b].process(band_block)
            aligned = [
                self._comp_delays[b][c].process(compressed.channels[c],
                                                compressed.valid[c])
                for c in range(nch)
            ]
            for c in range(nch):
                ac, av = aligned[c]
                sc, sv = summed[c], summed_valid[c]
                for t in range(n):
                    sc[t] += ac[t]
                    sv[t] = sv[t] and av[t]
            band_telemetries.append(telemetry)
            if collect_bands:
                band_outputs.append([a[0] for a in aligned])
        telemetry = {
            "name": self.name,
            "latency_samples": self.latency_samples,
            "band_group_delays": list(self.band_group_delays),
            "bands": band_telemetries,
        }
        if collect_bands:
            telemetry["band_outputs"] = band_outputs
        return Block(summed, summed_valid), telemetry
