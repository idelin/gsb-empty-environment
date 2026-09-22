"""内核判定测试：五个重点场景 + 不一致快照 + 证据不足。"""
import unittest

from common import MUTATIONS, PROGRAMS, oracle_obs, run_case, small_spec
from trace_kernel.kernel import INVALID, INSUFFICIENT, VALID, AnalysisKernel
from trace_kernel.replayer import run_mutations, run_task_program


class TestKeyScenarios(unittest.TestCase):
    def _check(self, prog, muts, expect):
        kernel, obs1 = run_case(PROGRAMS[prog], muts)
        obs2 = oracle_obs(PROGRAMS[prog], muts)
        v = kernel.verdicts["T"]
        self.assertEqual(v.status, expect, kernel.explain("T"))
        if expect == VALID:
            self.assertEqual(obs1, obs2)          # 批准必须与判据一致
        self.assertTrue(v.chain)                   # 结论必须带解释链
        return v

    def test_create_after_failed_lookup(self):
        v = self._check("P-failed-lookup", MUTATIONS["M-create-missing"], INVALID)
        self.assertIn("缺失名已被创建", v.chain[2])

    def test_path_replaced_by_other_object(self):
        # 打开后同路径换成另一对象：已打开句柄仍读原对象，但重跑会解析到新对象
        v = self._check("P-read-file", MUTATIONS["M-replace-f"], INVALID)
        self.assertTrue(any("目录成员映射" in c or "另一对象" in c
                            for c in v.chain), v.chain)

    def test_retarget_same_content(self):
        # 链接指向改变但最终内容相同：判据认为可复现，内核保守失效并给出具体链
        kernel, obs1 = run_case(PROGRAMS["P-via-symlink"], MUTATIONS["M-retarget-same"])
        obs2 = oracle_obs(PROGRAMS["P-via-symlink"], MUTATIONS["M-retarget-same"])
        self.assertEqual(obs1, obs2)               # 内容确实相同
        v = kernel.verdicts["T"]
        self.assertEqual(v.status, INVALID)
        self.assertIn("符号链接", v.chain[2])       # 具体原因而非“全部失效”

    def test_unlink_one_hardlink(self):
        # 删掉一个硬链接：经该链接解析的任务失效，经另一路径的任务不受影响
        kernel, _ = run_case(PROGRAMS["P-via-hardlink"], MUTATIONS["M-unlink-l"])
        self.assertEqual(kernel.verdicts["T"].status, INVALID)
        kernel2, _ = run_case(PROGRAMS["P-read-file"], MUTATIONS["M-unlink-l"])
        self.assertEqual(kernel2.verdicts["T"].status, VALID)

    def test_new_member_after_prefix_enum(self):
        v = self._check("P-enum-prefix", MUTATIONS["M-create-new"], INVALID)
        self.assertIn("前缀", v.chain[0])
        # 完整枚举同样失效
        self._check("P-enum-full", MUTATIONS["M-create-new"], INVALID)

    def test_unrelated_subtree_change(self):
        self._check("P-other-subtree", MUTATIONS["M-write-f"], VALID)
        self._check("P-read-file", MUTATIONS["M-write-g"], VALID)


class TestInconsistentSnapshot(unittest.TestCase):
    def test_read_spanning_write_never_approved(self):
        vfs = small_spec()
        vfs.current_task = "T"
        h = vfs.open("/a/f")
        vfs.read(h)                       # v1
        vfs.current_task = None
        vfs.write("/a/f", "f2")           # 读取跨越内容变更
        vfs.current_task = "T"
        vfs.read(h)                       # v2
        kernel = AnalysisKernel()
        kernel.ingest(vfs.trace.records)
        kernel.evaluate()
        v = kernel.verdicts["T"]
        self.assertEqual(v.status, INVALID)
        self.assertIn("快照不一致", v.chain[0])


class TestInsufficientEvidence(unittest.TestCase):
    def test_external_change_gives_specific_reason(self):
        kernel, _ = run_case(PROGRAMS["P-read-file"], [])
        kernel.add_external_event("/a", "备份系统重写，细节未知")
        kernel.evaluate()
        v = kernel.verdicts["T"]
        self.assertEqual(v.status, INSUFFICIENT)
        self.assertIn("/a", v.reason)
        self.assertIn("外部变更", v.reason)

    def test_unaffected_task_stays_valid_under_external(self):
        kernel, _ = run_case(PROGRAMS["P-other-subtree"], [])
        kernel.add_external_event("/a", "仅影响 /a")
        kernel.evaluate()
        self.assertEqual(kernel.verdicts["T"].status, VALID)

    def test_unknown_task_is_insufficient(self):
        kernel, _ = run_case(PROGRAMS["P-read-file"], [])
        v = kernel.verdict("ghost")
        self.assertEqual(v.status, INSUFFICIENT)
        self.assertTrue(v.reason)


if __name__ == "__main__":
    unittest.main()
