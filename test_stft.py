"""unittest 测试套件：python3.11 -m unittest -v"""

import math
import unittest

from fixtures import mono, multichannel
from pipeline import StftPipeline, SpectralEditError, WEIGHT_EPS
from stft import (
    ROUNDTRIP_TOL,
    centered_starts,
    dft,
    fft,
    hann_window,
    idft,
    ifft,
    variable_hop_starts,
)


def brute_weights(starts, win, num, active=None):
    """独立参考：按定义直接累加每样本实际重叠窗权重。"""
    w = hann_window(win)
    weight = [0.0] * num
    for idx, s in enumerate(starts):
        if active is not None and not active[idx]:
            continue
        for i in range(win):
            n = s + i
            if 0 <= n < num:
                weight[n] += w[i] * w[i]
    return weight


class DftReferenceTest(unittest.TestCase):
    def test_fft_matches_direct_dft_small(self):
        sig = [math.sin(i * 1.7) + 0.3 * i for i in range(16)]
        for a, b in zip(dft(sig), fft(sig)):
            self.assertAlmostEqual(abs(a - b), 0.0, delta=1e-9)

    def test_non_pow2_falls_back_to_dft(self):
        sig = [float(i % 5) - 1.0 for i in range(12)]
        self.assertEqual(fft(sig), dft(sig))

    def test_ifft_inverts_fft(self):
        sig = [complex(math.sin(i), 0.2 * i) for i in range(8)]
        for a, b in zip(sig, ifft(fft(sig))):
            self.assertAlmostEqual(abs(a - b), 0.0, delta=1e-9)

    def test_tiny_pipeline_against_direct_dft(self):
        samples = [0.3, -1.2, 0.7, 2.0, -0.5, 0.9,
                   1.1, -0.4, 0.6, 0.2, -0.8, 1.4]
        win, hop = 8, 2
        starts = centered_starts(len(samples), win, hop)
        w = hann_window(win)
        accum = [0.0] * len(samples)
        weight = [0.0] * len(samples)
        for s in starts:
            seg = [(samples[s + i] if 0 <= s + i < len(samples) else 0.0) * w[i]
                   for i in range(win)]
            td = idft(dft(seg))  # 直接 DFT 参考路径
            for i in range(win):
                n = s + i
                if 0 <= n < len(samples):
                    accum[n] += td[i].real * w[i]
                    weight[n] += w[i] * w[i]
        ref = [accum[n] / weight[n] for n in range(len(samples))]
        pipe = StftPipeline([samples], 8000, starts, win)
        for got, want in zip(pipe.output[0], ref):
            self.assertAlmostEqual(got, want, delta=1e-12)


class RoundTripTest(unittest.TestCase):
    def _roundtrip_err(self, starts, win, num=320):
        samples, sr = mono(num)
        pipe = StftPipeline([samples], sr, starts, win)
        self.assertTrue(pipe.is_lossless())
        return max(abs(pipe.output[0][n] - samples[n]) for n in range(num))

    def test_fixed_legal_hop(self):
        for hop in (16, 32):
            err = self._roundtrip_err(centered_starts(320, 64, hop), 64)
            self.assertLess(err, ROUNDTRIP_TOL)

    def test_variable_hop_weights_and_roundtrip(self):
        win = 64
        hops = [8, 24, 16, 12, 32, 4, 28]
        starts = variable_hop_starts(320, win, hops)
        samples, sr = mono()
        pipe = StftPipeline([samples], sr, starts, win)
        expected = brute_weights(starts, win, 320)
        # 变 hop 下每样本权重并非常数：固定 COLA 系数在此必然失效
        self.assertGreater(len({round(w, 9) for w in expected}), 1)
        for got, want in zip(pipe.weight, expected):
            self.assertAlmostEqual(got, want, delta=1e-12)
        self.assertTrue(pipe.is_lossless())
        err = max(abs(pipe.output[0][n] - samples[n]) for n in range(320))
        self.assertLess(err, ROUNDTRIP_TOL)


class ShortTailTest(unittest.TestCase):
    def test_tail_frame_weights(self):
        win, num = 64, 300  # 300 非 hop 整数倍 -> 存在越界短尾帧
        samples, sr = mono(num)
        starts = centered_starts(num, win, 32)
        self.assertGreater(starts[-1] + win, num)
        pipe = StftPipeline([samples], sr, starts, win)
        expected = brute_weights(starts, win, num)
        for got, want in zip(pipe.weight, expected):
            self.assertAlmostEqual(got, want, delta=1e-12)
        self.assertTrue(pipe.is_lossless())
        err = max(abs(pipe.output[0][n] - samples[n]) for n in range(num))
        self.assertLess(err, ROUNDTRIP_TOL)


class FrameLossTest(unittest.TestCase):
    STARTS = [-16, 0, 16, 32, 48, 96, 112, 128, 144]
    WIN = 32
    NUM = 160

    def _pipe(self):
        samples, sr = mono(self.NUM)
        return samples, StftPipeline([samples], sr, self.STARTS, self.WIN)

    def test_delete_marks_unrecoverable_exactly(self):
        samples, pipe = self._pipe()
        pre_mask = [n for n in range(self.NUM) if pipe.mask[n]]
        # 既有覆盖空洞 [80,96)，加上样本 96 落在帧 @96 的 Hann 零端点
        self.assertEqual(pre_mask, list(range(80, 97)))
        lo, hi = pipe.delete_frame(4)  # 起点 48 的帧
        self.assertEqual((lo, hi), (48, 80))
        expected = brute_weights(self.STARTS, self.WIN, self.NUM, pipe.active)
        for n in range(self.NUM):
            self.assertEqual(pipe.mask[n], expected[n] <= WEIGHT_EPS,
                             msg=f"sample {n}")
        newly = [n for n in range(self.NUM)
                 if pipe.mask[n] and n not in pre_mask]
        self.assertEqual(newly, list(range(64, 80)))
        for n in range(self.NUM):
            if pipe.mask[n]:
                # 不可恢复区输出 None 掩码，而非补 0 冒充无损
                self.assertIsNone(pipe.output[0][n])
            else:
                self.assertAlmostEqual(pipe.output[0][n], samples[n],
                                       delta=ROUNDTRIP_TOL)
        self.assertFalse(pipe.is_lossless())

    def test_insert_replacement_frame_restores_region(self):
        samples, pipe = self._pipe()
        pre_mask = [n for n in range(self.NUM) if pipe.mask[n]]
        pipe.delete_frame(4)
        w = hann_window(self.WIN)
        seg = [samples[48 + i] * w[i] for i in range(self.WIN)]
        lo, hi = pipe.insert_frame(48, [fft(seg)])
        self.assertEqual((lo, hi), (48, 80))
        self.assertEqual([n for n in range(self.NUM) if pipe.mask[n]], pre_mask)
        for n in range(self.NUM):
            if not pipe.mask[n]:
                self.assertAlmostEqual(pipe.output[0][n], samples[n],
                                       delta=ROUNDTRIP_TOL)


class SpectralEditTest(unittest.TestCase):
    def setUp(self):
        self.win = 64
        self.samples, self.sr = mono()
        self.starts = centered_starts(len(self.samples), self.win, 16)
        self.pipe = StftPipeline([self.samples], self.sr, self.starts, self.win)

    def test_mirror_mode_enforces_conjugacy(self):
        self.pipe.edit_spectrum(0, 5, {3: 0.5 + 0.25j}, mode="mirror")
        sp = self.pipe.spectra[5][0]
        self.assertAlmostEqual(abs(sp[self.win - 3] - (0.5 - 0.25j)),
                               0.0, delta=1e-12)
        td = ifft(sp)
        self.assertLess(max(abs(v.imag) for v in td), 1e-9)

    def test_strict_mode_rejects_asymmetric_edit(self):
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 5, {3: 0.5j}, mode="strict")

    def test_strict_mode_accepts_consistent_pair(self):
        self.pipe.edit_spectrum(0, 5, {3: 0.5j, self.win - 3: -0.5j},
                                mode="strict")

    def test_dc_and_nyquist_must_stay_real(self):
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 5, {0: 1.0 + 0.5j})
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 5, {self.win // 2: 2.0j})
        self.pipe.edit_spectrum(0, 5, {0: 3.0}, mode="strict")  # 实数 DC 合法

    def test_edit_rebuilds_only_affected_interval(self):
        before = list(self.pipe.output[0])
        lo, hi = self.pipe.edit_spectrum(0, 6, {7: 1.0 - 0.5j}, mode="mirror")
        self.assertEqual((lo, hi), (self.starts[6], self.starts[6] + self.win))
        for n in range(len(self.samples)):
            if lo <= n < hi:
                continue
            self.assertEqual(self.pipe.output[0][n], before[n])
        self.assertTrue(
            any(self.pipe.output[0][n] != before[n] for n in range(lo, hi)))


class BatchAtomicityTest(unittest.TestCase):
    def setUp(self):
        self.samples, self.sr = mono()
        self.win = 64
        starts = centered_starts(len(self.samples), self.win, 16)
        self.pipe = StftPipeline([self.samples], self.sr, starts, self.win)

    def test_failed_batch_preserves_published_version(self):
        self.pipe.publish()
        published = [list(ch) for ch in self.pipe.published["output"]]
        current = [list(ch) for ch in self.pipe.output]
        ops = [
            ("edit", 0, 4, {2: 1.0j}, "mirror"),
            ("edit", 0, 5, {3: 1.0j}, "strict"),  # 缺共轭项 -> 整批失败
        ]
        with self.assertRaises(SpectralEditError):
            self.pipe.apply_batch(ops)
        self.assertEqual(self.pipe.version, 1)
        self.assertEqual(self.pipe.published["output"], published)
        self.assertEqual(self.pipe.output, current)  # 已回滚

    def test_successful_batch_then_publish(self):
        self.pipe.publish()
        self.pipe.apply_batch([
            ("edit", 0, 4, {2: 1.0j}, "mirror"),
            ("delete", 6),
        ])
        self.assertEqual(self.pipe.publish(), 2)
        self.assertFalse(self.pipe.active[6])


class CheckpointTest(unittest.TestCase):
    def test_resume_matches_continuous_run(self):
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
        for a, b in zip(restored.weight, full.weight):
            self.assertAlmostEqual(a, b, delta=1e-12)
        for a, b in zip(restored.output[0], full.output[0]):
            self.assertAlmostEqual(a, b, delta=1e-12)
        # 未封闭重叠尾之外的前缀已封闭：与最终输出逐样本一致
        self.assertEqual(cp["sealed_output"][0], full.output[0][:cp["seal"]])


class MultiChannelTest(unittest.TestCase):
    def test_channels_time_aligned(self):
        channels, sr = multichannel()
        win = 64
        starts = centered_starts(len(channels[0]), win, 16)
        pipe = StftPipeline(channels, sr, starts, win)
        for c in range(2):
            err = max(abs(pipe.output[c][n] - channels[c][n])
                      for n in range(len(channels[c])))
            self.assertLess(err, ROUNDTRIP_TOL)
        ch1_before = list(pipe.output[1])
        pipe.edit_spectrum(0, 4, {5: 2.0j}, mode="mirror")
        self.assertEqual(pipe.output[1], ch1_before)  # 无串扰、无时间漂移
        self.assertEqual(len(pipe.output[0]), len(pipe.output[1]))

    def test_misaligned_channels_rejected(self):
        with self.assertRaises(ValueError):
            StftPipeline([[0.0] * 100, [0.0] * 99], 8000, [0], 32)


class LatencyDeclarationTest(unittest.TestCase):
    def test_latency_declaration(self):
        pipe = StftPipeline([[0.0] * 128], 8000,
                            centered_starts(128, 64, 16), 64)
        self.assertEqual(pipe.algorithmic_latency_samples, 64)
        self.assertAlmostEqual(pipe.latency_seconds, 64 / 8000)


if __name__ == "__main__":
    unittest.main()
