"""本地夹具：确定性样本数组与采样率（不接音频设备）。"""

import math

SAMPLE_RATE = 8000
NUM_SAMPLES = 320


def mono(num_samples=NUM_SAMPLES):
    """单通道测试信号：多正弦叠加。"""
    out = []
    for i in range(num_samples):
        t = i / SAMPLE_RATE
        out.append(
            math.sin(2.0 * math.pi * 440.0 * t)
            + 0.25 * math.sin(2.0 * math.pi * 1750.0 * t)
            + 0.1 * math.cos(2.0 * math.pi * 90.0 * t)
        )
    return out, SAMPLE_RATE


def multichannel(num_samples=NUM_SAMPLES):
    """双通道时间对齐信号（等长、同采样率）。"""
    a, _ = mono(num_samples)
    b = []
    for i in range(num_samples):
        t = i / SAMPLE_RATE
        b.append(
            0.6
            * math.cos(2.0 * math.pi * 261.625 * t)
            * (0.5 + 0.5 * math.sin(2.0 * math.pi * 2.0 * t))
        )
    return [a, b], SAMPLE_RATE
