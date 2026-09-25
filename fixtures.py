"""本地夹具:采样率、显式 FIR 系数、测试信号与声明的数值容差。

所有滤波系数在此显式构造并作为普通列表传入处理器;
样本数组与采样率全部来自本模块,不接触任何音频设备。
"""
from __future__ import annotations

import math

from dsp import Block

SAMPLE_RATE = 48000

# ---- 声明的数值容差 ----
PR_TOLERANCE = 1e-6        # 旁路重组相对参考信号的最大绝对误差
OVERSHOOT_TOLERANCE = 1e-6  # 限幅输出相对当前平滑阈值的残余超限(线性幅度)
PHASE_TOLERANCE_RAD = 1e-4  # 相位核验容差
RATE_TOLERANCE_DB = 1e-9    # 增益变化速率核验容差(dB/样本)

# ---- 声明的延迟(样本, @48 kHz) ----
CROSSOVER_BAND_GROUP_DELAYS = (60, 90, 90)
CROSSOVER_LATENCY = 90
LIMITER_LOOKAHEAD = 64
TOTAL_LATENCY = CROSSOVER_LATENCY + LIMITER_LOOKAHEAD  # 154 样本 ≈ 3.208 ms


def _sinc_lowpass(fc_norm, num_taps):
    """窗化 sinc 低通,fc_norm 为归一化频率(周期/样本)。"""
    m = num_taps - 1
    h = []
    for n in range(num_taps):
        k = n - m / 2
        if k == 0:
            h.append(2.0 * fc_norm)
        else:
            h.append(math.sin(2.0 * math.pi * fc_norm * k) / (math.pi * k))
    return [h[n] * (0.5 - 0.5 * math.cos(2.0 * math.pi * n / m))
            for n in range(num_taps)]


def _conv(a, b):
    out = [0.0] * (len(a) + len(b) - 1)
    for i, av in enumerate(a):
        if av == 0.0:
            continue
        for j, bv in enumerate(b):
            out[i + j] += av * bv
    return out


def crossover_coefficients(fs=SAMPLE_RATE, fc_low=300.0, fc_high=3000.0,
                           d1=60, d2=30):
    """显式构造三带互补系数,返回 (low, mid, high) 三个列表。

    树式互补结构的并联等效:
      low  = L1                    (群延迟 d1)
      mid  = (δ(d1) - L1) * L2     (群延迟 d1+d2)
      high = (δ(d1) - L1) * (δ(d2) - L2)  (群延迟 d1+d2)
    按声明群延迟补偿后,三带之和 = δ(d1+d2),即精确重建(浮点舍入内)。
    """
    lp1 = _sinc_lowpass(fc_low / fs, 2 * d1 + 1)
    rest = [-v for v in lp1]
    rest[d1] += 1.0
    lp2 = _sinc_lowpass(fc_high / fs, 2 * d2 + 1)
    hp2 = [-v for v in lp2]
    hp2[d2] += 1.0
    mid = _conv(rest, lp2)
    high = _conv(rest, hp2)
    return [lp1, mid, high]


def band_compressor_params():
    """三个频带的压缩参数(阈值/压缩比/软膝/包络时间常数)。"""
    return [
        {"threshold_db": -18.0, "ratio": 3.0, "knee_db": 6.0,
         "attack_s": 0.003, "release_s": 0.08},
        {"threshold_db": -15.0, "ratio": 2.5, "knee_db": 4.0,
         "attack_s": 0.002, "release_s": 0.06},
        {"threshold_db": -12.0, "ratio": 2.0, "knee_db": 4.0,
         "attack_s": 0.001, "release_s": 0.05},
    ]


def bypass_band_params():
    """旁路压缩(压缩比 1:1,恒 0 dB 增益)。"""
    return [{"ratio": 1.0, "knee_db": 0.0} for _ in range(3)]


def limiter_params():
    return {
        "lookahead_samples": LIMITER_LOOKAHEAD,
        "threshold_db": -1.0,
        "release_db_per_s": 120.0,
        "threshold_slew_db_per_s": 480.0,
    }


# ---- 信号 ----

def multitone(n, tones, fs=SAMPLE_RATE, channels=2):
    """多频信号,tones 为 (频率Hz, 幅度) 列表,各通道相同。"""
    sig = [sum(amp * math.sin(2.0 * math.pi * f * t / fs)
               for f, amp in tones)
           for t in range(n)]
    return [list(sig) for _ in range(channels)]


def sine(n, freq, amp, fs=SAMPLE_RATE, channels=2):
    sig = [amp * math.sin(2.0 * math.pi * freq * t / fs) for t in range(n)]
    return [list(sig) for _ in range(channels)]


def impulse(n, at, amp=1.0, channels=2, only_channel=None):
    """可控脉冲;only_channel 指定时仅该通道含脉冲。"""
    out = [[0.0] * n for _ in range(channels)]
    for c in range(channels):
        if only_channel is None or c == only_channel:
            out[c][at] = amp
    return out


def constant(n, values):
    """每通道恒定电平,values 为每通道幅度。"""
    return [[float(v)] * n for v in values]


def two_bursts(n, freq, burst_amp, burst_ranges, base_amp=0.05,
               fs=SAMPLE_RATE, channels=2):
    """低电平正弦背景上叠加两段高电平突发。"""
    sig = [base_amp * math.sin(2.0 * math.pi * freq * t / fs)
           for t in range(n)]
    for a, b in burst_ranges:
        for t in range(a, b):
            sig[t] = burst_amp * math.sin(2.0 * math.pi * freq * t / fs)
    return [list(sig) for _ in range(channels)]


def inject_missing(block, ranges):
    """在 [a, b) 区间注入缺测:标记无效并写入 NaN。"""
    chans = [list(ch) for ch in block.channels]
    valid = [list(v) for v in block.valid]
    for a, b in ranges:
        for c in range(len(chans)):
            for t in range(a, b):
                chans[c][t] = float("nan")
                valid[c][t] = False
    return Block(chans, valid)
