"""多通道音频块:样本 + 每样本有效标记。

缺测/未知样本以 valid=False 标记;NaN 一律判为无效并清零,
保证无效值不会污染滤波状态,只通过标记沿链路传播。
"""
from __future__ import annotations

import math


class Block:
    __slots__ = ("channels", "valid")

    def __init__(self, channels, valid=None):
        self.channels = [[float(x) for x in ch] for ch in channels]
        if not self.channels:
            raise ValueError("至少需要一个通道")
        n = len(self.channels[0])
        for ch in self.channels:
            if len(ch) != n:
                raise ValueError("各通道长度必须一致")
        if valid is None:
            self.valid = [[not math.isnan(x) for x in ch] for ch in self.channels]
        else:
            self.valid = [[bool(v) for v in vch] for vch in valid]
            if len(self.valid) != len(self.channels) or any(
                len(v) != n for v in self.valid
            ):
                raise ValueError("有效标记形状必须与音频一致")
        # 消毒:无效或 NaN 的样本清零并强制标记无效
        for c in range(len(self.channels)):
            xch = self.channels[c]
            vch = self.valid[c]
            for t in range(n):
                if not vch[t] or math.isnan(xch[t]):
                    xch[t] = 0.0
                    vch[t] = False

    @property
    def n_samples(self):
        return len(self.channels[0])

    @property
    def n_channels(self):
        return len(self.channels)

    def slice(self, start, end=None):
        return Block(
            [ch[start:end] for ch in self.channels],
            [v[start:end] for v in self.valid],
        )
