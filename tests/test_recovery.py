"""崩溃恢复：在每个持久化操作边界注入崩溃，恢复后与连续运行逐项一致。"""
import json
import tempfile
import unittest
from pathlib import Path

from unbiased_shuffle import CountingSource, ExternalShuffle, InjectedCrash


class TestCrashRecovery(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.n = 12

    def _continuous_run(self):
        workdir = f"{self.tmp.name}/continuous"
        shuffler = ExternalShuffle(workdir, sync=False)
        steps = []
        shuffler.on_step = lambda i, j: steps.append((i, j))
        src = CountingSource(seed=2024)
        shuffler.initialize(list(range(self.n)), src)
        init_ops = shuffler.op_count
        result = shuffler.run(src)
        return result, src.meta(), steps, init_ops, shuffler.op_count

    def test_recover_from_crash_at_every_op_boundary(self):
        ref_result, ref_meta, ref_steps, init_ops, total_ops = self._continuous_run()
        self.assertGreater(total_ops, init_ops)
        for crash_at in range(init_ops, total_ops):
            with self.subTest(crash_at=crash_at):
                workdir = f"{self.tmp.name}/crash_{crash_at}"
                src = CountingSource(seed=2024)
                shuffler = ExternalShuffle(workdir, sync=False)
                shuffler.initialize(list(range(self.n)), src)
                shuffler.fail_after = crash_at  # 仅在洗牌阶段注入崩溃
                with self.assertRaises(InjectedCrash):
                    shuffler.run(src)
                # “进程重启”：全新的 shuffler 与全新的随机源
                new_src = CountingSource(seed=None)
                recovered = ExternalShuffle(workdir, sync=False)
                recovered_steps = []
                recovered.on_step = lambda i, j: recovered_steps.append((i, j))
                recovered.recover(new_src)
                # 恢复点已提交的步数（在续跑前读取检查点）
                ckpt = recovered._read_checkpoint()
                committed = (self.n - 1) - ckpt["frontier"]
                result = recovered.run(new_src)
                # 逐项一致 + 样本量一致
                self.assertEqual(result, ref_result)
                self.assertEqual(sorted(result), list(range(self.n)))
                self.assertEqual(new_src.meta(), ref_meta)
                # 恢复不重复交换：续跑步骤恰为连续运行步骤序列的后缀
                self.assertEqual(recovered_steps, ref_steps[committed:])

    def test_torn_journal_is_discarded_safely(self):
        workdir = f"{self.tmp.name}/torn"
        ref_result, ref_meta, _, _, _ = self._continuous_run()
        src = CountingSource(seed=2024)
        shuffler = ExternalShuffle(workdir, sync=False)
        shuffler.initialize(list(range(self.n)), src)
        shuffler.fail_after = shuffler.op_count + 4  # 洗牌阶段第 4 个操作前崩溃
        with self.assertRaises(InjectedCrash):
            shuffler.run(src)
        # 手工制造撕裂的日志文件
        Path(workdir, "journal.json").write_text('{"frontier": 11, "writes": [[11, "')
        new_src = CountingSource(seed=None)
        recovered = ExternalShuffle(workdir, sync=False)
        recovered.recover(new_src)
        result = recovered.run(new_src)
        self.assertEqual(result, ref_result)
        self.assertEqual(new_src.meta(), ref_meta)

    def test_recovery_with_durable_sync(self):
        # 开启真实 fsync 的冒烟测试
        workdir = f"{self.tmp.name}/durable"
        src = CountingSource(seed=8)
        shuffler = ExternalShuffle(workdir, sync=True, fail_after=3)
        shuffler.initialize(list(range(6)), src)
        with self.assertRaises(InjectedCrash):
            shuffler.run(src)
        new_src = CountingSource(seed=None)
        recovered = ExternalShuffle(workdir, sync=True)
        recovered.recover(new_src)
        result = recovered.run(new_src)
        self.assertEqual(sorted(result), list(range(6)))

    def test_no_duplicate_or_missing_after_recovery(self):
        # 多次连续崩溃也能恢复
        workdir = f"{self.tmp.name}/multi"
        src = CountingSource(seed=2024)
        shuffler = ExternalShuffle(workdir, sync=False, fail_after=4)
        shuffler.initialize(list(range(self.n)), src)
        for crash_at in (0, 2, 5):
            shuffler = ExternalShuffle(workdir, sync=False, fail_after=crash_at)
            try:
                shuffler.run(src)
            except InjectedCrash:
                pass
            src = CountingSource(seed=None)
            ExternalShuffle(workdir, sync=False).recover(src)
        result = ExternalShuffle(workdir, sync=False).run(src)
        self.assertEqual(sorted(result), list(range(self.n)))
        ref_result, ref_meta, _, _, _ = self._continuous_run()
        self.assertEqual(result, ref_result)
        self.assertEqual(src.meta(), ref_meta)


if __name__ == "__main__":
    unittest.main()
