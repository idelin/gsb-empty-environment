"""Playback clock estimation.

The source clock reads s(t) = (1 + drift) * t + c in receiver time t. We run a
discounted (fixed-memory) least-squares regression of send timestamps on
receive timestamps; the slope estimates (1 + drift). The playback rate is
nominal_rate * slope, so slow clock drift is absorbed by the *rate*, not by
permanently growing the target delay.

Sub-tolerance noise is snapped away: a slope within RATE_SNAP_TOL of 1.0 is
treated as exactly 1.0 (declared tolerance, see config).
"""
from . import config


class ClockEstimator:
    def __init__(self, forgetting=config.CLOCK_FORGETTING):
        self.forgetting = forgetting
        self.w = 0.0
        self.sx = 0.0
        self.sy = 0.0
        self.sxx = 0.0
        self.sxy = 0.0
        self.n = 0

    def update(self, recv_time, send_time):
        lam = self.forgetting
        self.w = lam * self.w + 1.0
        self.sx = lam * self.sx + recv_time
        self.sy = lam * self.sy + send_time
        self.sxx = lam * self.sxx + recv_time * recv_time
        self.sxy = lam * self.sxy + recv_time * send_time
        self.n += 1

    @property
    def ready(self):
        return self.n >= config.MIN_CLOCK_SAMPLES and self.w > 0.0

    def slope(self):
        """Estimated source-seconds per receiver-second (1.0 until ready)."""
        if not self.ready:
            return 1.0
        var = self.sxx - self.sx * self.sx / self.w
        if var <= 0.0:
            return 1.0
        cov = self.sxy - self.sx * self.sy / self.w
        s = cov / var
        if not 0.5 < s < 1.5:      # sanity guard
            return 1.0
        return s

    def intercept(self):
        if not self.ready:
            return 0.0
        return (self.sy - self.slope() * self.sx) / self.w

    def map_send_to_recv(self, send_time):
        """Receiver time at which the source clock read `send_time`."""
        if not self.ready:
            return send_time
        return (send_time - self.intercept()) / self.slope()

    def playback_rate(self, nominal):
        s = self.slope()
        if abs(s - 1.0) <= config.RATE_SNAP_TOL:
            s = 1.0
        return nominal * s
