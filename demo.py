"""可重放样例:确定性重放关键场景,打印延迟声明、增益轨迹与超限证据。

运行: python3.11 demo.py    (全部断言通过时退出码为 0)
"""
import math

import fixtures
from dsp import Block, LookaheadLimiter, Pipeline

FS = fixtures.SAMPLE_RATE
L = fixtures.LIMITER_LOOKAHEAD


def db(x):
    return 20.0 * math.log10(max(x, 1e-300))


def scenario_bypass_reconstruction():
    print("[1] 旁路分频重组还原参考信号")
    n = 4096
    tones = [(150.0, 0.2), (800.0, 0.15), (2500.0, 0.1), (6000.0, 0.1)]
    data = fixtures.multitone(n, tones)
    pipe = Pipeline(FS, 2, fixtures.crossover_coefficients(),
                    fixtures.bypass_band_params(), fixtures.limiter_params())
    pipe.limiter.set_bypass(True)
    out, tel = pipe.process(Block(data))
    rep = tel["latency"]
    total = rep["total"]
    err = max(abs(out.channels[0][t + total] - data[0][t])
              for t in range(n - total))
    print(f"    逐阶段延迟: 分频 {rep['stages']['crossover']} + "
          f"限幅 {rep['stages']['limiter']} = {total} 样本 "
          f"({total / FS * 1e3:.3f} ms @ {FS} Hz)")
    print(f"    重组误差 {err:.3e} (声明容差 {fixtures.PR_TOLERANCE:g})")
    assert err < fixtures.PR_TOLERANCE


def scenario_linked_spike():
    print("[2] 仅右声道尖峰 → 两声道联动限幅")
    n = 2048
    t0 = 800
    left = fixtures.sine(n, 1000.0, 0.05, channels=1)[0]
    right = fixtures.impulse(n, t0, amp=3.0, channels=1)[0]
    lim = LookaheadLimiter(FS, 2, **{**fixtures.limiter_params(),
                                     "threshold_db": -6.0})
    out, tel = lim.process(Block([left, right]))
    gmin = min(tel.gain_db)
    print(f"    共享增益轨迹: 最小 {gmin:.2f} dB, "
          f"恢复速率 ≤ 120 dB/s (声明值)")
    print(f"    左声道(无尖峰)在尖峰离开时刻样本: "
          f"{out.channels[0][t0 + L]:+.6f} (联动衰减)")
    for ev in tel.events:
        print(f"    超限证据: 输入峰值 {ev['input_peak_db']:+.2f} dB, "
              f"阈值 {ev['threshold_db']:+.2f} dB, "
              f"施加增益 {ev['min_gain_db']:+.2f} dB, "
              f"输出峰值 {ev['output_peak_db']:+.2f} dB")
    assert abs(out.channels[0][t0 + L]) < 0.05 * 0.5
    assert max(abs(x) for x in out.channels[1]) <= 10 ** (-6 / 20) + 1e-6


def scenario_threshold_hot_update():
    print("[3] 阈值在前瞻区中途热更新")
    n = 2048
    t0 = 1000
    t_change = t0 + L // 2
    lim = LookaheadLimiter(FS, 2, lookahead_samples=L, threshold_db=0.0,
                           release_db_per_s=120.0,
                           threshold_slew_db_per_s=48000.0)
    block = Block(fixtures.impulse(n, t0, amp=4.0))
    o1, tel1 = lim.process(block.slice(0, t_change))
    lim.set_threshold_db(-12.0)
    o2, tel2 = lim.process(block.slice(t_change, n))
    merged = o1.channels[0] + o2.channels[0]
    thrs = tel1.threshold_db + tel2.threshold_db
    worst = max(abs(merged[t]) - 10 ** (thrs[t] / 20) for t in range(n))
    print(f"    t={t_change} 阈值 0 → -12 dB (斜率 1 dB/样本平滑), "
          f"全程超阈残余 {worst:+.2e} (声明容差 {fixtures.OVERSHOOT_TOLERANCE:g})")
    print(f"    峰值最终输出 {max(abs(x) for x in o2.channels[0]):.4f} "
          f"≈ 新阈值 {10 ** (-12 / 20):.4f}")
    assert worst <= fixtures.OVERSHOOT_TOLERANCE


def scenario_cancel_reengage():
    print("[4] 取消再恢复:不漏限幅、无双延迟")
    n = 4096
    data = fixtures.two_bursts(n, 1000.0, 2.0, [(400, 600), (3000, 3200)])
    lim = LookaheadLimiter(FS, 2, **{**fixtures.limiter_params(),
                                     "threshold_db": -6.0})
    block = Block(data)
    o1, _ = lim.process(block.slice(0, 1000))
    lim.set_bypass(True)
    o2, _ = lim.process(block.slice(1000, 2400))
    lim.set_bypass(False)
    o3, _ = lim.process(block.slice(2400, n))
    merged = o1.channels[0] + o2.channels[0] + o3.channels[0]
    thr = 10 ** (-6 / 20)
    b1 = max(abs(x) for x in merged[400 + L:600 + L])
    b2 = max(abs(x) for x in merged[3000 + L:3200 + L])
    aligned = merged[1000:2400] == data[0][1000 - L:2400 - L]
    print(f"    突发1 峰值 {b1:.4f}, 突发2(恢复后)峰值 {b2:.4f}, "
          f"阈值 {thr:.4f}; 旁路期输出=输入延迟 {L} 样本: {aligned}")
    assert b1 <= thr + 1e-6 and b2 <= thr + 1e-6 and aligned


def scenario_missing_samples():
    print("[5] 缺测样本标记传播")
    n = 2000
    block = fixtures.inject_missing(
        Block(fixtures.multitone(n, [(440.0, 0.1)])), [(1000, 1010)])
    pipe = Pipeline(FS, 2, fixtures.crossover_coefficients(),
                    fixtures.bypass_band_params(), fixtures.limiter_params())
    out, _ = pipe.process(block)
    invalid = [t for t, v in enumerate(out.valid[0]) if not v]
    print(f"    输入缺测 [1000, 1010) → 输出无效区间 "
          f"[{invalid[0]}, {invalid[-1]}] "
          f"(分频 FIR 延展 180 + 限幅前瞻 {L}), 输出无 NaN")
    assert invalid == list(range(1000, 1010 + 179 + L + 1))
    assert all(math.isfinite(x) for x in out.channels[0])


def main():
    print("三频带 FIR 分频 + 分带压缩 + 前瞻限幅 —— 可重放样例")
    print(f"采样率 {FS} Hz;声明总延迟 {fixtures.TOTAL_LATENCY} 样本; "
          f"容差: 重组 {fixtures.PR_TOLERANCE:g}, "
          f"超限 {fixtures.OVERSHOOT_TOLERANCE:g}, "
          f"相位 {fixtures.PHASE_TOLERANCE_RAD:g} rad")
    scenario_bypass_reconstruction()
    scenario_linked_spike()
    scenario_threshold_hot_update()
    scenario_cancel_reengage()
    scenario_missing_samples()
    print("ALL SCENARIOS REPLAYED OK")


if __name__ == "__main__":
    main()
