"""外存洗牌：确定性、元素守恒、读写重试不影响随机抽样。"""
import random
import tempfile
import unittest

from unbiased_shuffle import CountingSource, ExternalShuffle


class FlakyShuffle(ExternalShuffle):
    """每次读写先失败若干次再成功（瞬时错误，调用方重试）。"""

    def __init__(self, *args, failures_per_op=2, **kwargs):
        super().__init__(*args, **kwargs)
        self.failures_per_op = failures_per_op
        self._attempts = {}

    def _flaky(self, key, fn):
        seen = self._attempts.get(key, 0)
        self._attempts[key] = seen + 1
        if seen < self.failures_per_op:
            raise OSError("transient io error")
        return fn()

    def _read_record(self, pos):
        key = ("r", pos, self._attempts.get("gen", 0))
        for _ in range(self.failures_per_op + 1):
            try:
                return self._flaky(key, lambda: super(FlakyShuffle, self)._read_record(pos))
            except OSError:
                continue
        raise AssertionError("unreachable")

    def _write_record(self, pos, data):
        key = ("w", pos, self._attempts.get("gen", 0))
        for _ in range(self.failures_per_op + 1):
            try:
                return self._flaky(key, lambda: super(FlakyShuffle, self)._write_record(pos, data))
            except OSError:
                continue
        raise AssertionError("unreachable")


class TestExternalShuffle(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.n = 24

    def _run_fresh(self, subdir, cls=ExternalShuffle, **kwargs):
        shuffler = cls(f"{self.tmp.name}/{subdir}", sync=False, **kwargs)
        src = CountingSource(seed=555)
        shuffler.initialize(list(range(self.n)), src)
        result = shuffler.run(src)
        ckpt = shuffler._read_checkpoint()
        return result, src.meta(), ckpt

    def test_continuous_run_is_permutation(self):
        result, meta, ckpt = self._run_fresh("a")
        self.assertEqual(sorted(result), list(range(self.n)))  # 不重复不缺元素
        self.assertTrue(ckpt["done"])
        self.assertEqual(ckpt["published"], list(range(self.n)))
        self.assertEqual(meta["seed"], 555)
        self.assertGreaterEqual(meta["draws"], self.n - 1)

    def test_deterministic_same_seed(self):
        r1, m1, _ = self._run_fresh("b1")
        r2, m2, _ = self._run_fresh("b2")
        self.assertEqual(r1, r2)
        self.assertEqual(m1, m2)

    def test_retries_do_not_change_sampling(self):
        # 读写重试是幂等绝对写，不消耗随机性：结果与抽样计数逐项一致
        clean_result, clean_meta, _ = self._run_fresh("c1")
        flaky_result, flaky_meta, _ = self._run_fresh(
            "c2", cls=FlakyShuffle, failures_per_op=3)
        self.assertEqual(flaky_result, clean_result)
        self.assertEqual(flaky_meta, clean_meta)

    def test_checkpoint_tracks_frontier_and_published(self):
        shuffler = ExternalShuffle(f"{self.tmp.name}/d", sync=False)
        src = CountingSource(seed=1)
        shuffler.initialize(list(range(6)), src)
        shuffler.step(src)
        ckpt = shuffler._read_checkpoint()
        self.assertEqual(ckpt["frontier"], 4)
        self.assertEqual(ckpt["published"], [5])
        shuffler.step(src)
        ckpt = shuffler._read_checkpoint()
        self.assertEqual(ckpt["published"], [4, 5])
        self.assertEqual(ckpt["seed"], 1)


if __name__ == "__main__":
    unittest.main()
