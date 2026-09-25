"""显式系数的流式 FIR 滤波器与整数延迟线。

FIR 仅接受奇数长、对称(线性相位)系数,群延迟恒为 (N-1)/2,
以便重组时按声明值精确补偿。无效样本按 0 参与卷积,
其无效标记按抽头覆盖范围传播到输出。
"""
from __future__ import annotations

from collections import deque
from operator import mul


class FIRFilter:
    def __init__(self, coefficients, name="fir"):
        h = [float(c) for c in coefficients]
        if not h:
            raise ValueError("系数不能为空")
        if len(h) % 2 == 0:
            raise ValueError("仅接受奇数长系数(整数群延迟)")
        scale = max(max(abs(c) for c in h), 1.0)
        asym = max(abs(h[k] - h[-1 - k]) for k in range(len(h) // 2 + 1))
        if asym > 1e-9 * scale:
            raise ValueError("仅接受对称(线性相位)系数")
        self.name = name
        self.h = h
        self.group_delay = (len(h) - 1) // 2
        self._x = deque([0.0] * len(h), maxlen=len(h))
        self._v = deque([True] * len(h), maxlen=len(h))

    def process(self, samples, valid):
        h = self.h
        xq = self._x
        vq = self._v
        out = []
        outv = []
        for x, v in zip(samples, valid):
            xq.appendleft(x)
            vq.appendleft(v)
            out.append(sum(map(mul, h, xq)))
            outv.append(all(vq))
        return out, outv


class DelayLine:
    def __init__(self, delay_samples):
        if delay_samples < 0:
            raise ValueError("延迟不能为负")
        self.delay_samples = int(delay_samples)
        self._x = deque([0.0] * self.delay_samples)
        self._v = deque([True] * self.delay_samples)

    def process(self, samples, valid):
        xq = self._x
        vq = self._v
        out = []
        outv = []
        for x, v in zip(samples, valid):
            xq.append(x)
            vq.append(v)
            out.append(xq.popleft())
            outv.append(vq.popleft())
        return out, outv
