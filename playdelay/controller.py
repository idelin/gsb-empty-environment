"""Adaptive target delay.

The controller is fed *drift-corrected* transit times (receive time minus the
clock-estimator mapping of the send time), so slow clock drift never enters
the jitter estimate. The headroom is an EWMA of the absolute transit deviation
and therefore decays after a jitter burst: bursts raise the delay temporarily,
they are not memorised as a permanent delay increase.
"""
from . import config


class DelayController:
    def __init__(self):
        self.mean = None      # EWMA of drift-corrected transit time
        self.jit = 0.0        # EWMA of |transit - mean|

    def update(self, transit):
        if self.mean is None:
            self.mean = transit
            return
        d = transit - self.mean
        self.mean += config.JITTER_ALPHA * d
        self.jit += config.JITTER_BETA * (abs(d) - self.jit)

    def headroom(self):
        h = config.K_JITTER * self.jit
        return min(max(h, config.MIN_HEADROOM), config.MAX_HEADROOM)

    def target_delay(self):
        return config.BASE_TARGET_DELAY + self.headroom()
