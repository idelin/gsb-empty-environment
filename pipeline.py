"""STFT 管线：变 hop、帧丢失、局部频谱编辑、增量重建、checkpoint、版本化。

核心原则：
- 逆变换按每个样本实际重叠的窗权重（sum of w^2）归一，绝不使用固定
  COLA 系数，因此变 hop / 短尾帧 / 删帧后权重都按真实覆盖计算。
- 编辑或补帧只重建受影响的输出区间 [start, start+win)。
- 权重为零的样本输出 None 并置不可恢复掩码，绝不补 0 冒充无损。
- 多通道共享同一帧网格与掩码，保证时间对齐。
- 批量更正先验证后提交，失败整体回滚，已发布版本不变。
"""

from __future__ import annotations

import bisect

from stft import fft, hann_window, ifft

# 权重判零阈值：低于此值视为无覆盖（不可恢复）。
WEIGHT_EPS = 1e-12
# 实信号共轭对称校验容差。
HERM_TOL = 1e-9


class SpectralEditError(ValueError):
    """频谱编辑违反实信号共轭对称约束时抛出。"""


def check_hermitian(spectrum, tol=HERM_TOL):
    """校验频谱满足实信号共轭对称：X[k] == conj(X[n-k])，DC/Nyquist 为实。"""
    n = len(spectrum)
    if abs(complex(spectrum[0]).imag) > tol:
        return False
    if n % 2 == 0 and abs(complex(spectrum[n // 2]).imag) > tol:
        return False
    for k in range(1, (n + 1) // 2):
        lhs = complex(spectrum[k])
        rhs = complex(spectrum[n - k]).conjugate()
        if abs(lhs - rhs) > tol * (1.0 + abs(lhs)):
            return False
    return True


class StftPipeline:
    def __init__(self, channels, sample_rate, starts, win):
        if not channels:
            raise ValueError("at least one channel required")
        num = len(channels[0])
        if any(len(c) != num for c in channels):
            raise ValueError("channels must be time-aligned: equal sample counts")
        if any(b <= a for a, b in zip(starts, starts[1:])):
            raise ValueError("frame starts must be strictly increasing")
        self.sample_rate = sample_rate
        self.num_samples = num
        self.num_channels = len(channels)
        self.win = win
        self.window = hann_window(win)
        self.starts = list(starts)
        self.active = [True] * len(self.starts)
        self.spectra = []   # spectra[frame][channel] -> list[complex]
        self.contrib = []   # contrib[frame][channel] -> 窗加权时域贡献
        self.accum = [[0.0] * num for _ in channels]
        self.weight = [0.0] * num  # 帧网格共享（多通道对齐）
        for s in self.starts:
            frame_spectra = []
            frame_contrib = []
            for c in channels:
                windowed = [
                    (c[s + i] if 0 <= s + i < num else 0.0) * self.window[i]
                    for i in range(win)
                ]
                sp = fft(windowed)
                frame_spectra.append(sp)
                frame_contrib.append(self._synth_contrib(sp))
            self.spectra.append(frame_spectra)
            self.contrib.append(frame_contrib)
            self._add_weight(s, +1.0)
            for ci in range(self.num_channels):
                self._add_contrib(ci, s, frame_contrib[ci], +1.0)
        # 输出：float 或 None（None = 不可恢复掩码样本）
        self.output = [[0.0] * num for _ in channels]
        self.mask = [False] * num
        self._recompute_interval(0, num)
        self.version = 0
        self.published = None

    # ---- 延迟声明 ----
    @property
    def algorithmic_latency_samples(self):
        """算法延迟：一个完整窗长（需收齐一窗样本才能给出首个输出）。"""
        return self.win

    @property
    def latency_seconds(self):
        return self.win / self.sample_rate

    # ---- 内部累加/重建 ----
    def _synth_contrib(self, spectrum):
        td = ifft(spectrum)
        return [td[i].real * self.window[i] for i in range(self.win)]

    def _add_weight(self, start, sign):
        for i, w in enumerate(self.window):
            n = start + i
            if 0 <= n < self.num_samples:
                self.weight[n] += sign * w * w

    def _add_contrib(self, channel, start, contrib, sign):
        acc = self.accum[channel]
        for i, v in enumerate(contrib):
            n = start + i
            if 0 <= n < self.num_samples:
                acc[n] += sign * v

    def _recompute_interval(self, lo, hi):
        lo = max(0, lo)
        hi = min(hi, self.num_samples)
        for n in range(lo, hi):
            if self.weight[n] > WEIGHT_EPS:
                for c in range(self.num_channels):
                    self.output[c][n] = self.accum[c][n] / self.weight[n]
                self.mask[n] = False
            else:
                for c in range(self.num_channels):
                    self.output[c][n] = None
                self.mask[n] = True

    def frame_interval(self, frame_index):
        s = self.starts[frame_index]
        return (max(0, s), min(s + self.win, self.num_samples))

    def is_lossless(self):
        return not any(self.mask)

    # ---- 频谱编辑 ----
    def _validated_spectrum(self, spectrum, edits, mode):
        n = self.win
        new = list(spectrum)
        for k, v in edits.items():
            if not isinstance(k, int) or not 0 <= k < n:
                raise SpectralEditError(f"bin index out of range: {k!r}")
            v = complex(v)
            if k == 0 or (n % 2 == 0 and k == n // 2):
                if abs(v.imag) > HERM_TOL:
                    raise SpectralEditError("DC/Nyquist bins must stay real")
                new[k] = v
                continue
            mirror = n - k
            if mode == "mirror":
                if mirror in edits and abs(complex(edits[mirror]) - v.conjugate()) > HERM_TOL:
                    raise SpectralEditError("conflicting conjugate pair in edits")
                new[k] = v
                new[mirror] = v.conjugate()
            elif mode == "strict":
                if mirror not in edits:
                    raise SpectralEditError(
                        "strict mode requires explicit conjugate pair")
                if abs(complex(edits[mirror]) - v.conjugate()) > HERM_TOL:
                    raise SpectralEditError("conjugate pair inconsistent")
                new[k] = v
            else:
                raise ValueError(f"unknown edit mode: {mode!r}")
        if not check_hermitian(new):
            raise SpectralEditError("edit breaks real-signal conjugate symmetry")
        return new

    def edit_spectrum(self, channel, frame_index, edits, mode="mirror"):
        """局部频谱编辑。返回受影响输出区间 (lo, hi)，仅重建该区间。"""
        if not self.active[frame_index]:
            raise ValueError("frame is deleted")
        new_spectrum = self._validated_spectrum(
            self.spectra[frame_index][channel], edits, mode)
        new_contrib = self._synth_contrib(new_spectrum)
        s = self.starts[frame_index]
        self._add_contrib(channel, s, self.contrib[frame_index][channel], -1.0)
        self._add_contrib(channel, s, new_contrib, +1.0)
        self.spectra[frame_index][channel] = new_spectrum
        self.contrib[frame_index][channel] = new_contrib
        lo, hi = self.frame_interval(frame_index)
        self._recompute_interval(lo, hi)
        return (lo, hi)

    # ---- 帧丢失 / 补帧 ----
    def delete_frame(self, frame_index):
        """删除一帧。零权样本置不可恢复掩码。返回受影响区间。"""
        if not self.active[frame_index]:
            raise ValueError("frame already deleted")
        s = self.starts[frame_index]
        self._add_weight(s, -1.0)
        for ci in range(self.num_channels):
            self._add_contrib(ci, s, self.contrib[frame_index][ci], -1.0)
        self.active[frame_index] = False
        lo, hi = self.frame_interval(frame_index)
        self._recompute_interval(lo, hi)
        return (lo, hi)

    def insert_frame(self, start, spectra):
        """在指定起点补一帧（每通道一个满足共轭对称的频谱）。返回受影响区间。"""
        if len(spectra) != self.num_channels:
            raise ValueError("spectra must cover every channel")
        for sp in spectra:
            if len(sp) != self.win:
                raise ValueError("spectrum length must equal window length")
            if not check_hermitian(sp):
                raise SpectralEditError(
                    "inserted spectrum violates conjugate symmetry")
        index = bisect.bisect_left(self.starts, start)
        if (index < len(self.starts) and self.starts[index] == start
                and self.active[index]):
            raise ValueError("an active frame already exists at this start")
        contribs = [self._synth_contrib(sp) for sp in spectra]
        self.starts.insert(index, start)
        self.active.insert(index, True)
        self.spectra.insert(index, [list(sp) for sp in spectra])
        self.contrib.insert(index, contribs)
        self._add_weight(start, +1.0)
        for ci in range(self.num_channels):
            self._add_contrib(ci, start, contribs[ci], +1.0)
        lo, hi = max(0, start), min(start + self.win, self.num_samples)
        self._recompute_interval(lo, hi)
        return (lo, hi)

    def append_frame(self, start, spectra):
        """流式追加：起点必须晚于所有活动帧（保证 checkpoint 封闭前缀有效）。"""
        active_starts = [s for s, a in zip(self.starts, self.active) if a]
        if active_starts and start <= max(active_starts):
            raise ValueError("appended frame must start after the last active frame")
        return self.insert_frame(start, spectra)

    # ---- 批量更正（原子） ----
    def _snapshot(self):
        return (
            list(self.starts),
            list(self.active),
            [[list(sp) for sp in f] for f in self.spectra],
            [[list(td) for td in f] for f in self.contrib],
            [list(a) for a in self.accum],
            list(self.weight),
            [list(o) for o in self.output],
            list(self.mask),
        )

    def _restore(self, snap):
        (self.starts, self.active, self.spectra, self.contrib,
         self.accum, self.weight, self.output, self.mask) = snap

    def _apply_op(self, op):
        kind = op[0]
        if kind == "edit":
            return self.edit_spectrum(*op[1:])
        if kind == "delete":
            return self.delete_frame(*op[1:])
        if kind == "insert":
            return self.insert_frame(*op[1:])
        raise ValueError(f"unknown op: {kind!r}")

    def apply_batch(self, ops):
        """原子地应用一批更正：任一失败则整体回滚，已发布版本不受影响。"""
        snapshot = self._snapshot()
        try:
            return [self._apply_op(op) for op in ops]
        except Exception:
            self._restore(snapshot)
            raise

    # ---- 版本发布 ----
    def publish(self):
        self.version += 1
        self.published = {
            "version": self.version,
            "output": [list(ch) for ch in self.output],
            "mask": list(self.mask),
        }
        return self.version

    # ---- Checkpoint：保留未封闭重叠尾 ----
    def checkpoint(self):
        """导出检查点。[0, seal) 为已封闭输出（后续追加帧不会再改动），
        [seal, num_samples) 为未封闭重叠尾，其累加器与权重原样保留。"""
        active_starts = [s for s, a in zip(self.starts, self.active) if a]
        seal = max(active_starts, default=0)
        seal = min(max(seal, 0), self.num_samples)
        return {
            "sample_rate": self.sample_rate,
            "num_samples": self.num_samples,
            "num_channels": self.num_channels,
            "win": self.win,
            "starts": list(self.starts),
            "active": list(self.active),
            "spectra": [[list(sp) for sp in f] for f in self.spectra],
            "seal": seal,
            "sealed_output": [ch[:seal] for ch in self.output],
            "sealed_mask": list(self.mask[:seal]),
            "sealed_weight": list(self.weight[:seal]),
            "tail_accum": [list(ch[seal:]) for ch in self.accum],
            "tail_weight": list(self.weight[seal:]),
            "version": self.version,
        }

    @classmethod
    def from_checkpoint(cls, cp):
        obj = cls.__new__(cls)
        obj.sample_rate = cp["sample_rate"]
        obj.num_samples = cp["num_samples"]
        obj.num_channels = cp["num_channels"]
        obj.win = cp["win"]
        obj.window = hann_window(obj.win)
        obj.starts = list(cp["starts"])
        obj.active = list(cp["active"])
        obj.spectra = [[list(sp) for sp in f] for f in cp["spectra"]]
        obj.contrib = [
            [obj._synth_contrib(sp) for sp in frame] for frame in obj.spectra
        ]
        seal = cp["seal"]
        obj.accum = [[0.0] * seal + list(tail) for tail in cp["tail_accum"]]
        obj.weight = list(cp["sealed_weight"]) + list(cp["tail_weight"])
        obj.output = [
            list(head) + [0.0] * (obj.num_samples - seal)
            for head in cp["sealed_output"]
        ]
        obj.mask = list(cp["sealed_mask"]) + [False] * (obj.num_samples - seal)
        obj._recompute_interval(seal, obj.num_samples)
        obj.version = cp["version"]
        obj.published = None
        return obj
