"""Whole-segment reference implementation.

Computes the aligned output by direct convolution over the complete input
in one pass, using the exact same per-sample arithmetic as the streaming
aligner.  Any chunked execution, tail flush, or overlapping-change schedule
must reproduce this output bit-for-bit.
"""

import math

import fixture
from aligner import build_changes, convolve_at, evaluate_delay


def reference_run(inputs, base_delays, missing=None,
                  changes_per_channel=None,
                  kernel=fixture.fractional_kernel,
                  kernel_length=fixture.KERNEL_LENGTH):
    """Return (samples, zero_filled, contaminated) for the whole segment.

    inputs:              [channel][sample] full input arrays (equal length)
    base_delays:         declared per-channel base delays
    missing:             optional [channel][sample] bool mask of missing input
    changes_per_channel: optional [channel] list of (position, target,
                         transition) delay-change specs
    """
    num_channels = len(inputs)
    length = len(inputs[0])
    if any(len(x) != length for x in inputs):
        raise ValueError("all channels must have equal length")
    if missing is None:
        missing = [[False] * length for _ in range(num_channels)]
    if changes_per_channel is None:
        changes_per_channel = [[] for _ in range(num_channels)]
    group = (kernel_length - 1) / 2
    schedules = [build_changes(base_delays[c], changes_per_channel[c])
                 for c in range(num_channels)]

    def delay_at(c, n):
        return evaluate_delay(base_delays[c], schedules[c], n)

    # total output count on the common time base
    total = 0
    while True:
        n = total
        ok = True
        for c in range(num_channels):
            i_int = math.floor(delay_at(c, n) - group)
            if n - i_int > length - 1:
                ok = False
                break
        if not ok:
            break
        total += 1

    samples = [[0.0] * total for _ in range(num_channels)]
    zero_filled = [[False] * total for _ in range(num_channels)]
    contaminated = [[False] * total for _ in range(num_channels)]

    for c in range(num_channels):
        x, miss = inputs[c], missing[c]

        def get(idx, x=x, miss=miss):
            if 0 <= idx < len(x):
                m = miss[idx]
                return (0.0 if m else x[idx]), True, m
            return 0.0, False, False

        for n in range(total):
            value, z, ct = convolve_at(get, n, delay_at(c, n),
                                       kernel, group, kernel_length)
            samples[c][n] = value
            zero_filled[c][n] = z
            contaminated[c][n] = ct

    return samples, zero_filled, contaminated
