"""Local fixtures: deterministic sample arrays and sample rate.

No audio device is involved; sources are plain Python lists of floats.
"""

import math
import random

SAMPLE_RATE = 48000


def make_sources() -> dict[str, list[float]]:
    rng = random.Random(20260926)

    def tone(freq: float, n: int, amp: float) -> list[float]:
        return [
            amp * math.sin(2.0 * math.pi * freq * i / SAMPLE_RATE)
            for i in range(n)
        ]

    return {
        "lead": tone(440.0, 4096, 0.6),
        "pad": tone(220.0, 4096, 0.4),
        "noise": [rng.uniform(-0.3, 0.3) for _ in range(4096)],
    }
