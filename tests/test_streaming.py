"""流式检测:整段与分块等价、checkpoint 在候选静音期间恢复、延迟上界。"""
import json
import unittest

from silence_split import fixtures
from silence_split.core import window_rms
from silence_split.streaming import StreamingDetector

W = 80


def rms_sequence(pattern):
    """pattern: [("tone"|"silence", n_windows)] → 每窗口一个 RMS 值。"""
    out = []
    for kind, n in pattern:
        samples = fixtures.tone(W) if kind == "tone" else fixtures.silence(W)
        out.extend([window_rms(samples)] * n)
    return out


class TestChunkedEqualsWhole(unittest.TestCase):
    def setUp(self):
        self.cfg = fixtures.base_config()
        self.rms = rms_sequence([("tone", 30), ("silence", 10), ("tone", 30)])

    def run_detector(self, chunk_sizes):
        det = StreamingDetector(self.cfg)
        idx = 0
        for size in chunk_sizes:
            for r in self.rms[idx:idx + size]:
                det.push_window([r])
            idx += size
        return det

    def test_various_chunkings_match_whole(self):
        ref = self.run_detector([len(self.rms)])
        for sizes in ([1] * 70, [7] * 10, [13, 3, 54], [69, 1]):
            det = self.run_detector(sizes)
            self.assertEqual(det.finish(), ref.finish())
            self.assertEqual(det.committed, ref.committed)
            self.assertEqual(det.commit_windows, ref.commit_windows)

    def test_committed_is_prefix_of_final(self):
        det = self.run_detector([len(self.rms)])
        final = det.finish()
        committed = det.committed
        self.assertEqual(final[:len(committed)], committed)
        self.assertEqual(committed, [(0, 30)])
        self.assertEqual(final, [(0, 30), (40, 70)])


class TestCheckpoint(unittest.TestCase):
    def setUp(self):
        self.cfg = fixtures.base_config()
        self.rms = rms_sequence([("tone", 30), ("silence", 10), ("tone", 30)])

    def test_restore_during_candidate_silence(self):
        det = StreamingDetector(self.cfg)
        for r in self.rms[:32]:  # 候选静音进行到 2/4 个窗口,尚未确认
            det.push_window([r])
        self.assertEqual(det.committed, [])
        blob = json.dumps(det.checkpoint())  # 必须可 JSON 序列化
        restored = StreamingDetector.restore(self.cfg, 1, json.loads(blob))
        for r in self.rms[32:]:
            det.push_window([r])
            restored.push_window([r])
        # 恢复后继续,与不间断全量参考一致
        self.assertEqual(restored.finish(), det.finish())
        self.assertEqual(restored.finish(), [(0, 30), (40, 70)])
        self.assertEqual(restored.committed, [(0, 30)])
        self.assertEqual(restored.commit_windows, det.commit_windows)

    def test_restore_after_confirmation_keeps_commits(self):
        det = StreamingDetector(self.cfg)
        for r in self.rms[:34]:  # 静音 4 窗口已满,提交发生
            det.push_window([r])
        self.assertEqual(det.committed, [(0, 30)])
        blob = json.dumps(det.checkpoint())
        restored = StreamingDetector.restore(self.cfg, 1, json.loads(blob))
        self.assertEqual(restored.committed, [(0, 30)])
        for r in self.rms[34:]:
            restored.push_window([r])
        self.assertEqual(restored.finish(), [(0, 30), (40, 70)])


class TestLatencyBound(unittest.TestCase):
    def test_confirmation_within_declared_latency(self):
        cfg = fixtures.base_config()
        rms = rms_sequence([("tone", 30), ("silence", 10), ("tone", 30)])
        det = StreamingDetector(cfg)
        for r in rms:
            det.push_window([r])
        silence_start_sample = 30 * W
        commit_time = det.commit_windows[0] * W
        lag = commit_time - silence_start_sample
        self.assertGreaterEqual(lag, cfg.min_silence)
        self.assertLessEqual(lag, cfg.confirmation_latency_samples)


if __name__ == "__main__":
    unittest.main()
