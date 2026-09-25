"""Adaptive target delay.

The target delay is  base_margin + headroom.  The headroom tracks the
jitter residual above the clock floor with fast attack / slow decay, so
a burst raises the target temporarily but it always relaxes back; clock
drift is handled by the rate ratio from the DriftEstimator and never
becomes a permanent delay increase.
"""


class DelayController:
    def __init__(self, fs, base_margin_s=0.025, init_headroom_s=0.015,
                 min_headroom_s=0.008, max_headroom_s=0.100,
                 decay_per_tick=0.98, gain=0.1, max_correction_frac=0.5):
        self.fs = fs
        self.base_margin_s = base_margin_s
        self.headroom_s = init_headroom_s
        self.min_headroom_s = min_headroom_s
        self.max_headroom_s = max_headroom_s
        self.decay_per_tick = decay_per_tick
        self.gain = gain
        self.max_correction_frac = max_correction_frac
        self._candidate_s = 0.0

    def observe(self, residual_s):
        if residual_s > self._candidate_s:
            self._candidate_s = residual_s

    def tick(self):
        headroom = max(self._candidate_s,
                       self.headroom_s * self.decay_per_tick,
                       self.min_headroom_s)
        self.headroom_s = min(headroom, self.max_headroom_s)
        self._candidate_s = 0.0

    @property
    def target_delay_s(self):
        return self.base_margin_s + self.headroom_s

    def correction_samples(self, level_samples, block):
        """Per-tick playout advance correction. Positive when the buffer
        level is above target (shrink delay by consuming faster)."""
        error = level_samples - self.target_delay_s * self.fs
        limit = block * self.max_correction_frac
        return max(-limit, min(limit, error * self.gain))

    def to_state(self):
        return {
            "base_margin_s": self.base_margin_s,
            "headroom_s": self.headroom_s,
            "candidate_s": self._candidate_s,
        }

    def load_state(self, state):
        self.base_margin_s = state["base_margin_s"]
        self.headroom_s = state["headroom_s"]
        self._candidate_s = state["candidate_s"]
