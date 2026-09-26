"""Reference implementations and known-bad implementations for injection tests."""
from __future__ import annotations

import math
from typing import Sequence


def design_lowpass(num_taps: int, cutoff_hz: float, fs: float) -> list[float]:
    """Windowed-sinc lowpass (Hamming), unity DC gain, linear phase."""
    if num_taps % 2 == 0:
        raise ValueError("use an odd tap count for an integer group delay")
    m = num_taps - 1
    fc = cutoff_hz / fs
    h = []
    for n in range(num_taps):
        k = n - m / 2.0
        sinc = 2.0 * fc if k == 0 else math.sin(2.0 * math.pi * fc * k) / (math.pi * k)
        win = 0.54 - 0.46 * math.cos(2.0 * math.pi * n / m)
        h.append(sinc * win)
    s = sum(h)
    return [v / s for v in h]


class FIRFilter:
    """Correct streaming FIR; state carried across blocks; declares latency."""

    def __init__(self, coefficients: Sequence[float], latency_samples: int | None = None,
                 name: str = "FIRFilter"):
        self.coefficients = list(coefficients)
        self.latency_samples = (
            (len(self.coefficients) - 1) // 2 if latency_samples is None else latency_samples
        )
        self.name = name
        self.reset()

    def reset(self) -> None:
        self._line = [0.0] * len(self.coefficients)

    def process(self, block: Sequence[float]) -> list[float]:
        h = self.coefficients
        line = self._line
        out = []
        for v in block:
            line = [v] + line[:-1]
            out.append(sum(hi * di for hi, di in zip(h, line)))
        self._line = line
        return out


class IdentityFilter:
    """Unity-gain pass-through; used to validate window normalization."""

    latency_samples = 0

    def reset(self) -> None:
        pass

    def process(self, block: Sequence[float]) -> list[float]:
        return list(block)


# ---- known-bad implementations (injected to test detection capability) ----


class BlockResetFIR(FIRFilter):
    """Drops filter state at every block boundary (classic streaming bug)."""

    def process(self, block: Sequence[float]) -> list[float]:
        self.reset()
        return super().process(block)


class UndeclaredDelayFIR(FIRFilter):
    """Under-declares its latency by 3 samples (phase misalignment)."""

    def __init__(self, coefficients: Sequence[float], name: str = "UndeclaredDelayFIR"):
        super().__init__(coefficients, name=name)
        self.latency_samples -= 3


class AttenuatingFilter:
    """Claims unity gain but applies 0.5."""

    latency_samples = 0

    def reset(self) -> None:
        pass

    def process(self, block: Sequence[float]) -> list[float]:
        return [0.5 * v for v in block]


class BlockParityFilter:
    """Gain depends on block length parity (block-size-dependent behavior)."""

    latency_samples = 0

    def reset(self) -> None:
        pass

    def process(self, block: Sequence[float]) -> list[float]:
        gain = 1.0 if len(block) % 2 == 0 else 0.85
        return [gain * v for v in block]
