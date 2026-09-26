"""本地夹具：样本数组与采样率。不接触任何音频设备。

声明：
  DECLARED_LATENCY_S  采集管线声明延迟（秒），仅作元数据
  TIME_TOLERANCE      数值容差（秒），与 timeline.TIME_TOLERANCE 一致
"""
import math

from timeline import Block

DECLARED_LATENCY_S = 0.002
TIME_TOLERANCE = 1e-9


def ramp(n, start=0.0, step=0.5):
    """确定性斜坡样本。"""
    return tuple(start + step * i for i in range(n))


def tone(n, rate, freq=440.0, amp=1.0):
    """确定性正弦样本。"""
    return tuple(amp * math.sin(2.0 * math.pi * freq * i / rate) for i in range(n))


def rate_switch_blocks(latency=DECLARED_LATENCY_S):
    """采样率切换：8 kHz 块（0.1 s）接 16 kHz 块（0.1 s）。"""
    return [
        Block("a8k", 8000.0, ramp(800), nominal_start=0.0,
              version=1, declared_latency=latency),
        Block("b16k", 16000.0, tone(1600, 16000.0), nominal_start=0.1,
              version=1, declared_latency=latency),
    ]


def gap_blocks(latency=DECLARED_LATENCY_S):
    """缺口场景：两块之间留 25 ms 空洞。"""
    return [
        Block("g0", 8000.0, ramp(800), nominal_start=0.0,
              version=1, declared_latency=latency),
        Block("g1", 8000.0, ramp(800, start=1000.0), nominal_start=0.125,
              version=1, declared_latency=latency),
    ]


def overlap_blocks(latency=DECLARED_LATENCY_S):
    """重叠场景：v1 块 [0, 2)s 与 v2 块 [1, 3)s 重叠，v2 优先。"""
    return [
        Block("old", 8.0, ramp(16, start=0.0), nominal_start=0.0,
              version=1, declared_latency=latency),
        Block("new", 8.0, ramp(16, start=100.0), nominal_start=1.0,
              version=2, declared_latency=latency),
    ]


def same_time_overlap_blocks(latency=DECLARED_LATENCY_S):
    """同时间重叠：起点完全相同，版本高者独占。"""
    return [
        Block("dup_lo", 8.0, ramp(8, start=0.0), nominal_start=0.5,
              version=1, declared_latency=latency),
        Block("dup_hi", 8.0, ramp(8, start=50.0), nominal_start=0.5,
              version=2, declared_latency=latency),
    ]
