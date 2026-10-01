"""Direct O(n^2) DFT/IDFT reference used to validate small inputs."""
from __future__ import annotations

import cmath
import math


def dft(x):
    n = len(x)
    return [
        sum(x[m] * cmath.exp(-2j * math.pi * k / n * m) for m in range(n))
        for k in range(n)
    ]


def idft(x):
    n = len(x)
    return [
        sum(x[m] * cmath.exp(2j * math.pi * k / n * m) for m in range(n)) / n
        for k in range(n)
    ]
