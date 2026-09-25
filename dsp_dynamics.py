"""Per-band envelope follower and compressor gain computer.

Unknown (NaN) policy: a NaN input taints the envelope for TAINT_TIME_CONSTANTS
times the slowest smoothing time constant, during which the envelope (and
therefore the desired gain) is NaN. After the taint window the follower
restarts from zero, so unknown regions are bounded and explicitly marked.
"""

import math

TAINT_TIME_CONSTANTS = 5.0


def _coef(sample_rate, time_ms):
    if time_ms <= 0.0:
        return 0.0
    return math.exp(-1.0 / (sample_rate * time_ms / 1000.0))


class EnvelopeFollower:
    """Peak envelope follower with separate attack/release smoothing."""

    def __init__(self, sample_rate, attack_ms, release_ms):
        self.sample_rate = sample_rate
        self.attack_ms = attack_ms
        self.release_ms = release_ms
        self._att = _coef(sample_rate, attack_ms)
        self._rel = _coef(sample_rate, release_ms)
        slowest = max(attack_ms, release_ms, 1e-6) / 1000.0 * sample_rate
        self.taint_len = int(math.ceil(TAINT_TIME_CONSTANTS * slowest))
        self.env = 0.0
        self._taint = 0

    def process(self, x):
        if math.isnan(x):
            self._taint = self.taint_len
            self.env = 0.0
            return float("nan")
        if self._taint > 0:
            self._taint -= 1
            return float("nan")
        a = abs(x)
        coef = self._att if a > self.env else self._rel
        self.env = coef * self.env + (1.0 - coef) * a
        return self.env


class BandCompressor:
    """Computes per-channel desired gains for one band.

    Linking across channels is done by the pipeline (shared control gain);
    this class keeps one envelope follower per channel so each channel
    keeps independent audio.
    """

    def __init__(self, name, sample_rate, channels, threshold_db, ratio,
                 attack_ms, release_ms):
        if ratio < 1.0:
            raise ValueError("ratio must be >= 1")
        self.name = name
        self.threshold_db = threshold_db
        self.ratio = ratio
        self.followers = [
            EnvelopeFollower(sample_rate, attack_ms, release_ms)
            for _ in range(channels)
        ]

    def _gain_from_envelope(self, env):
        if math.isnan(env):
            return float("nan")
        if env <= 0.0:
            return 1.0
        level_db = 20.0 * math.log10(env)
        over_db = level_db - self.threshold_db
        if over_db <= 0.0:
            return 1.0
        gain_db = -over_db * (1.0 - 1.0 / self.ratio)
        return 10.0 ** (gain_db / 20.0)

    def desired_gains(self, channel, block):
        follower = self.followers[channel]
        return [self._gain_from_envelope(follower.process(x)) for x in block]
