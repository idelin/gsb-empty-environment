"""核心检测算法:窗口 RMS、迟滞状态机、最短持续规则与有声核心区间计算。

延迟与数值容差声明
------------------
* 检测粒度为一个窗口(``window_size`` 个采样点);``min_silence`` /
  ``min_sound`` 等时长量以采样点给出,内部按窗口向上取整量化。
* 静音确认延迟上界 = ``min_silence + window_size`` 个采样点
  (见 :attr:`DetectorConfig.confirmation_latency_samples`)。
* 片段导出延迟上界 = 确认延迟 + ``post_pad``
  (见 :attr:`DetectorConfig.export_latency_samples`)。
* RMS 与门限比较采用相对容差 ``NUMERIC_TOLERANCE``(默认 1e-9)。
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .constants import NUMERIC_TOLERANCE


@dataclass(frozen=True)
class DetectorConfig:
    """检测配置。时长量以采样点为单位,内部按窗口向上取整量化。"""

    sample_rate: int
    window_size: int
    open_threshold: float
    close_threshold: float
    min_silence: int
    min_sound: int
    pre_pad: int = 0
    post_pad: int = 0
    strategy: str = "any"  # "any": 任一轨有声即有声; "all": 全部轨有声才有声
    tolerance: float = NUMERIC_TOLERANCE

    def __post_init__(self):
        if self.window_size <= 0:
            raise ValueError("window_size must be positive")
        if not 0.0 <= self.close_threshold <= self.open_threshold:
            raise ValueError("require 0 <= close_threshold <= open_threshold")
        if self.min_silence < 0 or self.min_sound < 0:
            raise ValueError("durations must be non-negative")
        if self.pre_pad < 0 or self.post_pad < 0:
            raise ValueError("pads must be non-negative")
        if self.strategy not in ("any", "all"):
            raise ValueError("strategy must be 'any' or 'all'")
        if self.tolerance < 0:
            raise ValueError("tolerance must be non-negative")

    @property
    def min_silence_windows(self) -> int:
        return max(1, -(-self.min_silence // self.window_size))

    @property
    def min_sound_windows(self) -> int:
        return max(1, -(-self.min_sound // self.window_size))

    @property
    def confirmation_latency_samples(self) -> int:
        """静音确认延迟上界(采样点):静音起点最多再经过这么多采样点即被确认。"""
        return self.min_silence + self.window_size

    @property
    def export_latency_samples(self) -> int:
        """片段导出延迟上界(采样点):确认延迟 + 后保留量。"""
        return self.confirmation_latency_samples + self.post_pad


def window_rms(samples) -> float:
    """窗口均方根,使用 math.fsum 保证求和数值稳定。"""
    n = len(samples)
    if n == 0:
        return 0.0
    return math.sqrt(math.fsum(x * x for x in samples) / n)


class Hysteresis:
    """双门限施密特触发器。

    RMS 高于 open 门限(含相对容差)进入有声;低于 close 门限进入静音;
    介于两门限之间(死区)时保持原状态,避免门限附近抖动。
    """

    def __init__(self, open_threshold, close_threshold,
                 tolerance=NUMERIC_TOLERANCE, state=False):
        self._open = open_threshold
        self._close = close_threshold
        self._tol = tolerance
        self.state = bool(state)

    def classify(self, rms) -> bool:
        if self.state:
            if rms < self._close * (1.0 - self._tol):
                self.state = False
        else:
            if rms > self._open * (1.0 + self._tol):
                self.state = True
        return self.state


def absorb_short_sound(flags, min_sound_windows, right_closed):
    """把短于 ``min_sound_windows`` 且两侧均为静音的有声段吸收为静音。

    * 接触序列左端点的有声段不吸收(起点之前的状态未知);
    * 右端点只有在 ``right_closed``(流已结束)时才允许吸收,
      流式处理中末尾的短有声段可能随后续数据变长,不能提前吸收。
    """
    out = list(flags)
    n = len(out)
    i = 0
    while i < n:
        if out[i]:
            j = i
            while j < n and out[j]:
                j += 1
            if (j - i) < min_sound_windows and i > 0 and (j < n or right_closed):
                for k in range(i, j):
                    out[k] = False
            i = j
        else:
            i += 1
    return out


def sound_cores(smoothed_flags, min_silence_windows):
    """由(已吸收短噪的)标记序列求有声核心区间 ``[start, end)``(窗口单位)。

    短于 ``min_silence_windows`` 的静音间隔被合并进同一片段;
    达到该长度的静音切分片段。序列首尾的静音不产生片段。
    """
    cores = []
    n = len(smoothed_flags)
    i = 0
    while i < n:
        if not smoothed_flags[i]:
            i += 1
            continue
        start = i
        end = i
        j = i
        while j < n:
            if smoothed_flags[j]:
                j += 1
                end = j
            else:
                k = j
                while k < n and not smoothed_flags[k]:
                    k += 1
                if k < n and k - j < min_silence_windows:
                    j = k  # 短静音:合并,继续延伸同一片段
                else:
                    break
        cores.append((start, end))
        i = j
    return cores
