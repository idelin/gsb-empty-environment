"""增量检测器:逐窗口推进、静音确认、片段提交,支持 checkpoint 序列化。"""
from __future__ import annotations

from .core import Hysteresis, absorb_short_sound, sound_cores


def _runs(flags):
    runs = []
    n = len(flags)
    i = 0
    while i < n:
        j = i
        while j < n and flags[j] == flags[i]:
            j += 1
        runs.append((flags[i], i, j))
        i = j
    return runs


class FlagFinalizer:
    """对布尔窗口流做静音确认与片段提交。

    ``committed`` 序列只增不改:一段静音一旦达到最短静音时长即被确认,
    其之前的有声核心区间随之定稿;后续数据不会改写已提交结果。
    """

    def __init__(self, min_sound_windows, min_silence_windows):
        self.min_sound_windows = min_sound_windows
        self.min_silence_windows = min_silence_windows
        self.flags = []
        self.committed = []        # 已提交核心区间 [(start_w, end_w)]
        self.commit_windows = []   # 每次提交发生时已处理的窗口数(用于延迟核验)
        self._silence_at = None    # 当前已确认静音的起点窗口

    def push(self, flag):
        self.flags.append(bool(flag))
        self._update()

    def _update(self):
        sm = absorb_short_sound(self.flags, self.min_sound_windows,
                                right_closed=False)
        last_false = None
        last_true = None
        for value, s, e in _runs(sm):
            if value:
                last_true = (s, e)
            else:
                last_false = (s, e)
        if last_false is not None:
            s, e = last_false
            if (e - s >= self.min_silence_windows and s > 0
                    and self._silence_at != s):
                self._commit(s)
                self._silence_at = s
        if last_true is not None and self._silence_at is not None:
            s, e = last_true
            if e - s >= self.min_sound_windows and s > self._silence_at:
                self._silence_at = None  # 有声确认,当前静音结束

    def _commit(self, silence_start):
        prefix = absorb_short_sound(self.flags[:silence_start],
                                    self.min_sound_windows, right_closed=True)
        cores = sound_cores(prefix, self.min_silence_windows)
        for core in cores[len(self.committed):]:
            self.committed.append(core)
            self.commit_windows.append(len(self.flags))

    def finish(self):
        """流结束:右端封闭,返回全部有声核心区间。"""
        sm = absorb_short_sound(self.flags, self.min_sound_windows,
                                right_closed=True)
        return sound_cores(sm, self.min_silence_windows)

    def checkpoint(self):
        """导出可 JSON 序列化的完整状态(含候选静音中途状态)。"""
        return {
            "min_sound_windows": self.min_sound_windows,
            "min_silence_windows": self.min_silence_windows,
            "flags": [1 if f else 0 for f in self.flags],
            "committed": [[s, e] for s, e in self.committed],
            "commit_windows": list(self.commit_windows),
            "silence_at": self._silence_at,
        }

    @classmethod
    def restore(cls, data):
        obj = cls(data["min_sound_windows"], data["min_silence_windows"])
        obj.flags = [bool(v) for v in data["flags"]]
        obj.committed = [tuple(c) for c in data["committed"]]
        obj.commit_windows = list(data["commit_windows"])
        obj._silence_at = data["silence_at"]
        return obj


class StreamingDetector:
    """RMS → 迟滞 → 多轨合并 → FlagFinalizer 的增量管线。"""

    def __init__(self, config, n_tracks=1):
        self.config = config
        self._hyst = [
            Hysteresis(config.open_threshold, config.close_threshold,
                       config.tolerance)
            for _ in range(n_tracks)
        ]
        self._finalizer = FlagFinalizer(config.min_sound_windows,
                                        config.min_silence_windows)

    def push_window(self, rms_per_track):
        flags = [h.classify(r) for h, r in zip(self._hyst, rms_per_track)]
        if self.config.strategy == "all":
            flag = all(flags)
        else:
            flag = any(flags)
        self._finalizer.push(flag)

    @property
    def committed(self):
        return list(self._finalizer.committed)

    @property
    def commit_windows(self):
        return list(self._finalizer.commit_windows)

    @property
    def windows_processed(self):
        return len(self._finalizer.flags)

    def finish(self):
        return self._finalizer.finish()

    def checkpoint(self):
        return {
            "hyst": [h.state for h in self._hyst],
            "finalizer": self._finalizer.checkpoint(),
        }

    @classmethod
    def restore(cls, config, n_tracks, data):
        obj = cls(config, n_tracks)
        for h, state in zip(obj._hyst, data["hyst"]):
            h.state = bool(state)
        obj._finalizer = FlagFinalizer.restore(data["finalizer"])
        return obj
