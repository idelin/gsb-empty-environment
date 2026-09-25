"""本地夹具:确定性合成信号,不接触任何音频设备。"""
from __future__ import annotations

import math

from .core import DetectorConfig

DEFAULT_RATE = 8000


def tone(n_samples, amp=0.5, freq=400.0, rate=DEFAULT_RATE):
    """正弦音;amp=0.5 时 RMS 约 0.354,明显高于默认 open 门限 0.2。"""
    return [amp * math.sin(2.0 * math.pi * freq * i / rate)
            for i in range(n_samples)]


def silence(n_samples):
    return [0.0] * n_samples


def constant(n_samples, value):
    return [float(value)] * n_samples


def concat(*parts):
    out = []
    for part in parts:
        out.extend(part)
    return out


def base_config(**overrides):
    """测试/样例共用配置:窗口 80 采样(10ms @8kHz)。"""
    params = dict(
        sample_rate=DEFAULT_RATE,
        window_size=80,
        open_threshold=0.2,
        close_threshold=0.1,
        min_silence=320,   # 4 窗口
        min_sound=160,     # 2 窗口
        pre_pad=80,
        post_pad=80,
    )
    params.update(overrides)
    return DetectorConfig(**params)
