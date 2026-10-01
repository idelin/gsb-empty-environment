"""可重放样例：确定性演示 STFT 管线的全部关键操作。

运行：python3.11 examples.py
退出码 0 = 全部场景通过；输出可逐行复现（无随机源、无外部输入）。
"""

import sys

from fixtures import mono, multichannel
from pipeline import StftPipeline, SpectralEditError, WEIGHT_EPS
from stft import (
    ROUNDTRIP_TOL,
    centered_starts,
    fft,
    hann_window,
    variable_hop_starts,
)


def scenario_roundtrip():
    samples, sr = mono()
    win, hop = 64, 16
    pipe = StftPipeline([samples], sr, centered_starts(len(samples), win, hop), win)
    err = max(abs(pipe.output[0][n] - samples[n]) for n in range(len(samples)))
    ok = pipe.is_lossless() and err < ROUNDTRIP_TOL
    print(f"[1] 固定合法窗/hop 往返: win={win} hop={hop} "
          f"max_err={err:.3e} tol={ROUNDTRIP_TOL:.0e} "
          f"latency={pipe.algorithmic_latency_samples}样本"
          f"({pipe.latency_seconds * 1e3:.2f}ms) -> {'PASS' if ok else 'FAIL'}")
    return ok


def scenario_variable_hop():
    samples, sr = mono()
    win = 64
    starts = variable_hop_starts(len(samples), win, [8, 24, 16, 12, 32, 4, 28])
    pipe = StftPipeline([samples], sr, starts, win)
    distinct = len({round(w, 9) for w in pipe.weight})
    err = max(abs(pipe.output[0][n] - samples[n]) for n in range(len(samples)))
    ok = distinct > 1 and pipe.is_lossless() and err < ROUNDTRIP_TOL
    print(f"[2] 变 hop 往返: 每样本权重取值数={distinct}(非常数,禁用固定COLA) "
          f"max_err={err:.3e} -> {'PASS' if ok else 'FAIL'}")
    return ok


def _lossy_pipeline():
    samples, sr = mono(160)
    win = 32
    starts = [-16, 0, 16, 32, 48, 96, 112, 128, 144]
    return samples, sr, win, starts, StftPipeline([samples], sr, starts, win)


def scenario_frame_loss():
    samples, sr, win, starts, pipe = _lossy_pipeline()
    pre = [n for n in range(len(samples)) if pipe.mask[n]]
    pipe.delete_frame(4)  # 删除起点 48 的帧
    newly = [n for n in range(len(samples)) if pipe.mask[n] and n not in pre]
    masked_none = all(pipe.output[0][n] is None for n in newly)
    exact_elsewhere = all(
        pipe.mask[n] or abs(pipe.output[0][n] - samples[n]) < ROUNDTRIP_TOL
        for n in range(len(samples))
    )
    ok = newly == list(range(64, 80)) and masked_none and exact_elsewhere
    print(f"[3] 删帧: 无法恢复区=[{newly[0]},{newly[-1] + 1}) "
          f"输出=None(非补0冒充无损) 其余样本精确 -> {'PASS' if ok else 'FAIL'}")
    return ok


def scenario_insert_repair():
    samples, sr, win, starts, pipe = _lossy_pipeline()
    pre = [n for n in range(len(samples)) if pipe.mask[n]]
    pipe.delete_frame(4)
    w = hann_window(win)
    seg = [samples[48 + i] * w[i] for i in range(win)]
    pipe.insert_frame(48, [fft(seg)])  # 补帧：仅重建受影响区间
    restored = [n for n in range(len(samples)) if pipe.mask[n]] == pre
    err = max(
        abs(pipe.output[0][n] - samples[n])
        for n in range(len(samples)) if not pipe.mask[n]
    )
    ok = restored and err < ROUNDTRIP_TOL
    print(f"[4] 补帧修复: 掩码恢复至删帧前状态 max_err={err:.3e} "
          f"-> {'PASS' if ok else 'FAIL'}")
    return ok


def scenario_spectral_edit():
    samples, sr = mono()
    win = 64
    pipe = StftPipeline([samples], sr,
                        centered_starts(len(samples), win, 16), win)
    before = list(pipe.output[0])
    edits = {k: 0j for k in range(8, 20)}  # 局部置零一个频带
    lo, hi = pipe.edit_spectrum(0, 6, edits, mode="mirror")
    outside_unchanged = all(
        pipe.output[0][n] == before[n]
        for n in range(len(samples)) if not lo <= n < hi
    )
    inside_changed = any(pipe.output[0][n] != before[n] for n in range(lo, hi))
    rejected = False
    try:
        pipe.edit_spectrum(0, 6, {3: 0.5j}, mode="strict")  # 缺共轭项
    except SpectralEditError:
        rejected = True
    ok = outside_unchanged and inside_changed and rejected
    print(f"[5] 局部频谱编辑: 仅重建区间[{lo},{hi}) 区外逐样本不变 "
          f"非对称编辑被明确拒绝 -> {'PASS' if ok else 'FAIL'}")
    return ok


def scenario_checkpoint():
    samples, sr = mono()
    win = 64
    starts = centered_starts(len(samples), win, 16)
    full = StftPipeline([samples], sr, starts, win)
    split = 10
    part = StftPipeline([samples], sr, starts[:split], win)
    cp = part.checkpoint()
    restored = StftPipeline.from_checkpoint(cp)
    for fi in range(split, len(starts)):
        restored.append_frame(starts[fi], [list(full.spectra[fi][0])])
    tail_ok = all(
        abs(a - b) < 1e-12
        for a, b in zip(restored.output[0], full.output[0])
    )
    sealed_ok = cp["sealed_output"][0] == full.output[0][:cp["seal"]]
    ok = tail_ok and sealed_ok
    print(f"[6] checkpoint 续跑: 未封闭重叠尾(seal={cp['seal']})保留 "
          f"续跑结果与连续运行一致 -> {'PASS' if ok else 'FAIL'}")
    return ok


def scenario_batch_atomicity():
    samples, sr = mono()
    win = 64
    pipe = StftPipeline([samples], sr,
                        centered_starts(len(samples), win, 16), win)
    pipe.publish()
    published = [list(ch) for ch in pipe.published["output"]]
    try:
        pipe.apply_batch([
            ("edit", 0, 4, {2: 1.0j}, "mirror"),
            ("edit", 0, 5, {3: 1.0j}, "strict"),  # 缺共轭项 -> 整批失败
        ])
        ok = False
    except SpectralEditError:
        ok = (pipe.version == 1
              and pipe.published["output"] == published
              and all(a == b for a, b in zip(pipe.output[0], published[0])))
    print(f"[7] 批量更正原子性: 失败批次已回滚 发布版本保持v{pipe.version} "
          f"-> {'PASS' if ok else 'FAIL'}")
    return ok


def scenario_multichannel():
    channels, sr = multichannel()
    win = 64
    pipe = StftPipeline(channels, sr,
                        centered_starts(len(channels[0]), win, 16), win)
    errs = [
        max(abs(pipe.output[c][n] - channels[c][n]) for n in range(len(channels[c])))
        for c in range(2)
    ]
    ch1_before = list(pipe.output[1])
    pipe.edit_spectrum(0, 4, {5: 2.0j}, mode="mirror")
    aligned = pipe.output[1] == ch1_before and len(pipe.output[0]) == len(pipe.output[1])
    ok = all(e < ROUNDTRIP_TOL for e in errs) and aligned
    print(f"[8] 多通道时间对齐: 共享帧网格/掩码 通道0编辑不影响通道1 "
          f"max_err={max(errs):.3e} -> {'PASS' if ok else 'FAIL'}")
    return ok


def main():
    scenarios = [
        scenario_roundtrip,
        scenario_variable_hop,
        scenario_frame_loss,
        scenario_insert_repair,
        scenario_spectral_edit,
        scenario_checkpoint,
        scenario_batch_atomicity,
        scenario_multichannel,
    ]
    results = [fn() for fn in scenarios]
    if all(results):
        print("ALL SCENARIOS PASSED")
        return 0
    print("SOME SCENARIOS FAILED")
    return 1


if __name__ == "__main__":
    sys.exit(main())
