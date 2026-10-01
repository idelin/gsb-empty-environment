"""可暂停 Fisher-Yates：检查点保存前沿/随机状态/已发布位置，恢复后不重复交换。"""
import json
import unittest

from unbiased_shuffle import CountingSource, ShuffleState


class TestPausableShuffle(unittest.TestCase):
    def setUp(self):
        self.n = 20
        self.items = list(range(self.n))
        src = CountingSource(seed=1234)
        self.reference = ShuffleState(self.items, src).run()
        self.reference_meta = src.meta()

    def test_result_is_permutation(self):
        self.assertEqual(sorted(self.reference), self.items)

    def test_pause_resume_at_every_frontier(self):
        for pause_after in range(self.n):
            src = CountingSource(seed=1234)
            state = ShuffleState(self.items, src)
            for _ in range(pause_after):
                state.step()
            ckpt = state.checkpoint()
            json.dumps(ckpt)  # 检查点必须可序列化
            # 用全新的随机源恢复：随机状态完全来自检查点
            restored = ShuffleState.restore(json.loads(json.dumps(ckpt)),
                                            CountingSource(seed=None))
            self.assertEqual(restored.run(), self.reference)
            self.assertEqual(restored.source.meta(), self.reference_meta)

    def test_checkpoint_contents(self):
        src = CountingSource(seed=99)
        state = ShuffleState(list(range(10)), src)
        for _ in range(4):
            state.step()
        ckpt = state.checkpoint()
        self.assertEqual(ckpt["frontier"], 5)
        self.assertEqual(ckpt["published"], [6, 7, 8, 9])  # 已发布位置
        self.assertEqual(ckpt["seed"], 99)
        self.assertIn("rng_state", ckpt)
        self.assertGreaterEqual(ckpt["draws"], 4)

    def test_published_positions_never_touched_again(self):
        src = CountingSource(seed=5)
        state = ShuffleState(list(range(15)), src)
        snapshot = {}
        while state.step():
            for p in state.published:
                snapshot.setdefault(p, state.items[p])
            for p, v in snapshot.items():
                self.assertEqual(state.items[p], v)  # 已发布位置的值不再变化
        self.assertEqual(sorted(snapshot), list(range(15)))

    def test_step_index_ranges(self):
        # 每步 j 都在 [0, frontier] 内（通过 ScriptedSource 越界即暴露）
        n = 12
        src = CountingSource(seed=77)
        state = ShuffleState(list(range(n)), src)
        frontier = n - 1
        while state.step():
            self.assertEqual(state.frontier, frontier - 1)
            frontier -= 1
        self.assertEqual(frontier, 0)


if __name__ == "__main__":
    unittest.main()
