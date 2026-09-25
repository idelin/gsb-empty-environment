"""峰值包络跟随器(攻击/释放独立时间常数)。无效样本期间保持原值。"""
from __future__ import annotations

import math


class EnvelopeFollower:
    def __init__(self, fs, attack_s, release_s):
        self._ca = math.exp(-1.0 / (fs * attack_s)) if attack_s > 0 else 0.0
        self._cr = math.exp(-1.0 / (fs * release_s)) if release_s > 0 else 0.0
        self.env = 0.0

    def update(self, x):
        c = self._ca if x > self.env else self._cr
        self.env = c * self.env + (1.0 - c) * x
        return self.env
