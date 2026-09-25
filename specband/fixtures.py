"""Local fixtures: deterministic sample arrays + sample rate.

No audio devices are touched; arrays are synthesised here.  Frequencies
are chosen on the bin grid (df = SAMPLE_RATE / N_SAMPLES = 2 Hz) so
hand-checking is easy.
"""

import math

SAMPLE_RATE = 64.0
N_SAMPLES = 32


def _mix(components, n=N_SAMPLES, fs=SAMPLE_RATE):
    return [
        sum(a * math.sin(2.0 * math.pi * f * t / fs + ph) for f, a, ph in components)
        for t in range(n)
    ]


# 8 Hz (bin 4) amplitude 1.0 + 16 Hz (bin 8) amplitude 0.5.
SAMPLES_CALM = _mix([(8.0, 1.0, 0.0), (16.0, 0.5, 0.3)])
# Corrected re-acquisition of the same frame (sensor gain fixed).
SAMPLES_CALM_V2 = _mix([(8.0, 1.1, 0.0), (16.0, 0.5, 0.3)])
# 24 Hz (bin 12) amplitude 1.2 + 4 Hz (bin 2) amplitude 0.4.
SAMPLES_BRIGHT = _mix([(24.0, 1.2, 0.1), (4.0, 0.4, 0.0)])
# Digital silence: zero total energy.
SAMPLES_SILENT = [0.0] * N_SAMPLES
