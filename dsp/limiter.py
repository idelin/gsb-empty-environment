"""前瞻限幅器(多通道联动)。

工作原理(每样本):
1. 各通道取绝对值并联动求峰值,进入长度 L+1 的前瞻窗;
2. 需求增益 = min(0, 当前平滑阈值 - 窗内峰值dB),对窗内所有样本生效,
   因此阈值在前瞻区中途收紧时,仍在窗内的样本同样受限(不漏限幅);
3. 施加增益 = min(需求增益, 上一增益 + 释放步进),攻击瞬时、释放按
   声明速率(dB/s)恢复,增益变化速率可核验;
4. 音频走固定 L 样本延迟线;旁路仅把施加增益置 0 dB,延迟线不变,
   取消/恢复都不会引入双延迟;旁路期间内部增益持续跟踪,恢复即生效。

输出永不超过当前平滑阈值(数值容差内),不裁平波形;
每次增益压限都记录超限证据事件。无效样本不参与峰值检测,
其标记沿音频路径与检测窗传播到输出。
"""
from __future__ import annotations

import math
from collections import deque

from .block import Block
from .telemetry import StageTelemetry

_EVENT_EPS_DB = 1e-9


class LookaheadLimiter:
    def __init__(self, fs, n_channels, lookahead_samples=64, threshold_db=-1.0,
                 release_db_per_s=120.0, threshold_slew_db_per_s=480.0,
                 bypass=False, name="limiter"):
        if lookahead_samples < 0:
            raise ValueError("前瞻长度不能为负")
        self.name = name
        self.fs = float(fs)
        self.n_channels = n_channels
        self.latency_samples = int(lookahead_samples)
        self._rel_step = float(release_db_per_s) / self.fs
        self._thr_step = float(threshold_slew_db_per_s) / self.fs
        self._thr = float(threshold_db)
        self._thr_target = float(threshold_db)
        self._g = 0.0
        self._bypass = bool(bypass)
        self._win_peak = deque()
        self._win_valid = deque()
        self._delay = [deque([0.0] * self.latency_samples)
                       for _ in range(n_channels)]
        self._delay_valid = [deque([True] * self.latency_samples)
                             for _ in range(n_channels)]
        self._t = 0
        self.events = []
        self._open = None

    def set_threshold_db(self, db):
        """热更新阈值目标值,实际阈值按声明斜率平滑过渡。"""
        self._thr_target = float(db)

    def set_bypass(self, flag):
        self._bypass = bool(flag)

    def _update_events(self, t, wmax, greq_db, applied_db, out_peak):
        active = (not self._bypass) and greq_db < -_EVENT_EPS_DB
        if active:
            w_db = 20.0 * math.log10(wmax) if wmax > 0.0 else -math.inf
            o_db = 20.0 * math.log10(out_peak) if out_peak > 0.0 else -math.inf
            if self._open is None:
                self._open = {
                    "start": t, "end": t,
                    "input_peak_db": w_db, "threshold_db": self._thr,
                    "min_gain_db": applied_db, "output_peak_db": o_db,
                }
            else:
                e = self._open
                e["end"] = t
                if w_db > e["input_peak_db"]:
                    e["input_peak_db"] = w_db
                    e["threshold_db"] = self._thr
                if applied_db < e["min_gain_db"]:
                    e["min_gain_db"] = applied_db
                if o_db > e["output_peak_db"]:
                    e["output_peak_db"] = o_db
        elif self._open is not None:
            self._open["would_exceed_db"] = (
                self._open["input_peak_db"] - self._open["threshold_db"])
            self.events.append(self._open)
            self._open = None

    def process(self, block):
        if block.n_channels != self.n_channels:
            raise ValueError("通道数不匹配")
        n = block.n_samples
        nch = self.n_channels
        L = self.latency_samples
        block_start = self._t
        out = [[0.0] * n for _ in range(nch)]
        outv = [[True] * n for _ in range(nch)]
        gain_traj = []
        thr_traj = []
        for t in range(n):
            peak = 0.0
            all_valid = True
            for c in range(nch):
                v = block.valid[c][t]
                all_valid = all_valid and v
                if v:
                    a = abs(block.channels[c][t])
                    if a > peak:
                        peak = a
            self._win_peak.append(peak)
            self._win_valid.append(all_valid)
            if len(self._win_peak) > L + 1:
                self._win_peak.popleft()
                self._win_valid.popleft()
            if self._thr < self._thr_target:
                self._thr = min(self._thr_target, self._thr + self._thr_step)
            elif self._thr > self._thr_target:
                self._thr = max(self._thr_target, self._thr - self._thr_step)
            wmax = max(self._win_peak)
            greq = 0.0
            if wmax > 0.0:
                greq = min(0.0, self._thr - 20.0 * math.log10(wmax))
            self._g = min(greq, self._g + self._rel_step)
            g_db = 0.0 if self._bypass else self._g
            lin = 10.0 ** (g_db / 20.0)
            window_valid = all(self._win_valid)
            out_peak = 0.0
            for c in range(nch):
                dq = self._delay[c]
                dv = self._delay_valid[c]
                dq.append(block.channels[c][t])
                dv.append(block.valid[c][t])
                d = dq.popleft()
                dvv = dv.popleft()
                y = d * lin
                out[c][t] = y
                outv[c][t] = dvv and window_valid
                ay = abs(y)
                if ay > out_peak:
                    out_peak = ay
            gain_traj.append(g_db)
            thr_traj.append(self._thr)
            self._update_events(self._t, wmax, greq, g_db, out_peak)
            self._t += 1
        events = [e for e in self.events if e["end"] >= block_start]
        if self._open is not None:
            events.append(dict(
                self._open, ongoing=True,
                would_exceed_db=self._open["input_peak_db"]
                - self._open["threshold_db"]))
        telemetry = StageTelemetry(
            name=self.name, latency_samples=L,
            gain_db=gain_traj, threshold_db=thr_traj, events=events,
        )
        return Block(out, outv), telemetry
