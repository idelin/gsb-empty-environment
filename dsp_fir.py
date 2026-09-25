"""Streaming FIR filtering with explicit coefficients and integer delay lines.

All filter coefficients are supplied by the caller; nothing is designed here.
NaN ("unknown / missing") samples propagate naturally through the filter
state: a NaN at input position k contaminates exactly len(coeffs) output
samples, which is how unknown data is marked and propagated downstream.
"""

import math


def detect_linear_phase_group_delay(coeffs, tol=1e-9):
    """Return the group delay (in samples) of a symmetric/antisymmetric FIR.

    Returns None when the filter is not linear phase, in which case the
    caller must supply the group delay explicitly.
    """
    n = len(coeffs)
    symmetric = all(abs(coeffs[i] - coeffs[n - 1 - i]) <= tol for i in range(n))
    antisymmetric = all(abs(coeffs[i] + coeffs[n - 1 - i]) <= tol for i in range(n))
    if symmetric or antisymmetric:
        return (n - 1) / 2.0
    return None


class FIRFilter:
    """Causal streaming FIR filter (direct form, explicit coefficients)."""

    def __init__(self, coeffs):
        if not coeffs:
            raise ValueError("coeffs must be non-empty")
        self.coeffs = [float(c) for c in coeffs]
        self._state = [0.0] * len(self.coeffs)

    def process_sample(self, x):
        self._state = [x] + self._state[:-1]
        acc = 0.0
        for c, s in zip(self.coeffs, self._state):
            acc += c * s
        return acc

    def process(self, block):
        return [self.process_sample(x) for x in block]


class IntegerDelay:
    """Stateful integer-sample delay line (used for group-delay alignment)."""

    def __init__(self, samples):
        if samples < 0 or int(samples) != samples:
            raise ValueError("delay must be a non-negative integer")
        self.samples = int(samples)
        self._state = [0.0] * self.samples

    def process(self, block):
        if self.samples == 0:
            return list(block)
        buf = self._state + list(block)
        self._state = buf[len(block):]
        return buf[:len(block)]


def is_unknown(x):
    return isinstance(x, float) and math.isnan(x)
