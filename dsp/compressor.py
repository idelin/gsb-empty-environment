"""单频带多通道联动压缩器。

- 每通道独立包络,控制增益取各通道最小值(联动),音频各自独立;
- 阈值热更新:目标阈值按声明斜率(dB/s)逐样本平滑逼近,无跳变;
- 增益变化速率受限(攻击/释放两个方向,dB/s),轨迹可核验;
- 压缩器不延迟音频(延迟 0 样本),包络在无效样本期间保持。
"""
from __future__ import annotations

import math

from .block import Block
from .envelope import EnvelopeFollower
from .telemetry import StageTelemetry


def static_gain_db(level_db, threshold_db, ratio, knee_db):
    """软膝静态压缩曲线,返回增益(dB,<=0)。"""
    if ratio < 1.0:
        raise ValueError("压缩比必须 >= 1")
    if knee_db > 0.0:
        lo = threshold_db - 0.5 * knee_db
        hi = threshold_db + 0.5 * knee_db
        if level_db <= lo:
            return 0.0
        if level_db >= hi:
            return (threshold_db - level_db) * (1.0 - 1.0 / ratio)
        d = level_db - lo
        return (1.0 / ratio - 1.0) * d * d / (2.0 * knee_db)
    if level_db <= threshold_db:
        return 0.0
    return (threshold_db - level_db) * (1.0 - 1.0 / ratio)


class BandCompressor:
    def __init__(self, fs, n_channels, threshold_db=-20.0, ratio=4.0, knee_db=6.0,
                 attack_s=0.005, release_s=0.05, gain_attack_db_per_s=2000.0,
                 gain_release_db_per_s=250.0, threshold_slew_db_per_s=480.0,
                 bypass=False, name="compressor"):
        if ratio < 1.0:
            raise ValueError("压缩比必须 >= 1")
        self.name = name
        self.fs = float(fs)
        self.n_channels = n_channels
        self.ratio = float(ratio)
        self.knee_db = float(knee_db)
        self.latency_samples = 0
        self._envs = [EnvelopeFollower(fs, attack_s, release_s)
                      for _ in range(n_channels)]
        self._thr = float(threshold_db)
        self._thr_target = float(threshold_db)
        self._thr_step = float(threshold_slew_db_per_s) / self.fs
        self._atk_step = float(gain_attack_db_per_s) / self.fs
        self._rel_step = float(gain_release_db_per_s) / self.fs
        self._g = 0.0
        self._bypass = bool(bypass)

    def set_threshold_db(self, db):
        """热更新阈值目标值,实际阈值按声明斜率平滑过渡。"""
        self._thr_target = float(db)

    def set_bypass(self, flag):
        self._bypass = bool(flag)

    def process(self, block):
        if block.n_channels != self.n_channels:
            raise ValueError("通道数不匹配")
        n = block.n_samples
        nch = self.n_channels
        out = [[0.0] * n for _ in range(nch)]
        gain_traj = []
        thr_traj = []
        for t in range(n):
            if self._thr < self._thr_target:
                self._thr = min(self._thr_target, self._thr + self._thr_step)
            elif self._thr > self._thr_target:
                self._thr = max(self._thr_target, self._thr - self._thr_step)
            linked = 0.0
            for c in range(nch):
                if block.valid[c][t]:
                    self._envs[c].update(abs(block.channels[c][t]))
                level = 20.0 * math.log10(max(self._envs[c].env, 1e-12))
                g = static_gain_db(level, self._thr, self.ratio, self.knee_db)
                if g < linked:
                    linked = g
            if linked < self._g:
                self._g = max(linked, self._g - self._atk_step)
            else:
                self._g = min(linked, self._g + self._rel_step)
            g_db = 0.0 if self._bypass else self._g
            lin = 10.0 ** (g_db / 20.0)
            for c in range(nch):
                out[c][t] = block.channels[c][t] * lin
            gain_traj.append(g_db)
            thr_traj.append(self._thr)
        telemetry = StageTelemetry(
            name=self.name, latency_samples=0,
            gain_db=gain_traj, threshold_db=thr_traj,
        )
        return Block(out, [list(v) for v in block.valid]), telemetry
