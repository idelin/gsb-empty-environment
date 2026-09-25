"""Local fixtures: sample rate and the source sample arrays.

No audio device is involved. The "signal" is a deterministic sum of
sines, generated per packet on demand so memory stays O(1).
"""

import math

SAMPLE_RATE = 48000          # declared fixture sample rate (Hz)
PACKET_SAMPLES = 480         # 10 ms per packet at 48 kHz
BLOCK_SAMPLES = 480          # 10 ms render block at 48 kHz

_TWO_PI = 2.0 * math.pi


def sample_at(index):
    t = index / SAMPLE_RATE
    return 0.5 * math.sin(_TWO_PI * 440.0 * t) + 0.25 * math.sin(_TWO_PI * 1237.0 * t)


def packet_samples(src_pos, count=PACKET_SAMPLES):
    return tuple(sample_at(src_pos + k) for k in range(count))
