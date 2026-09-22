"""穷举对拍：小对象集上的合法读写交错，内核判定 vs 独立重放器判据。

不变式：
  1. 内核判 valid => 判据必须可复现（绝不错误批准）。
  2. 判据不可复现 => 内核不得判 valid。
  3. 保守失效（判据可复现但内核判 invalid）必须带具体解释链。
  4. insufficient 必须带具体原因；不允许用“全部失效”满足正确性
     （断言存在大量内核判 valid 的用例）。
"""
import itertools
import unittest

from common import MUTATIONS, PROGRAMS, oracle_obs, run_case, small_spec
from trace_kernel.kernel import INSUFFICIENT, VALID, AnalysisKernel
from trace_kernel.replayer import (
    is_legal_mutation,
    run_mutations,
    run_task_program,
)
from trace_kernel.vfs import VFS


def _legal_mutation_sequences(max_len=2):
    """枚举长度 <= max_len 的合法变更序列（在 small_spec 副本上过滤）。"""
    singles = list(MUTATIONS.values())
    seqs = [list(s) for s in singles]
    flat = [m for s in singles for m in s]
    for a, b in itertools.product(flat, flat):
        vfs = small_spec()
        if is_legal_mutation(vfs, a) and is_legal_mutation(vfs, b):
            seqs.append([a, b])
    return seqs


class TestExhaustive(unittest.TestCase):
    def test_kernel_never_over_approves(self):
        stats = {"valid": 0, "invalid": 0, "insufficient": 0,
                 "conservative": 0, "cases": 0}
        conservative_chains = []
        for pname, program in PROGRAMS.items():
            if pname == "P-read-twice":
                continue  # 交错场景单独测
            for muts in _legal_mutation_sequences():
                kernel, obs1 = run_case(program, muts)
                obs2 = oracle_obs(program, muts)
                v = kernel.verdicts["T"]
                stats["cases"] += 1
                stats[v.status] += 1
                reproducible = obs1 == obs2
                if v.status == VALID:
                    self.assertTrue(
                        reproducible,
                        f"错误批准: {pname} x {muts}\n{kernel.explain('T')}")
                else:
                    self.assertTrue(v.chain, f"失效缺少解释链: {pname} x {muts}")
                    if reproducible:
                        stats["conservative"] += 1
                        conservative_chains.append((pname, muts, v.chain))
                if v.status == INSUFFICIENT:
                    self.assertTrue(v.reason)
        # 不允许用“全部失效”满足正确性
        self.assertGreater(stats["valid"], stats["cases"] // 3,
                           f"批准率过低，疑似全部失效: {stats}")
        # 保守失效必须逐条带具体解释（抽样检查含对象/路径信息）
        for pname, muts, chain in conservative_chains:
            self.assertTrue(any(("o" in c and "#" in c) or "依赖" in c
                                for c in chain),
                            f"保守失效解释不具体: {pname} x {muts}: {chain}")
        print(f"\n穷举统计: {stats}")

    def test_interleaved_read_write(self):
        """读取跨越内容变更的所有交错位置：写入落在两次读之间时不得批准。"""
        program = PROGRAMS["P-read-twice"]
        write = ("write", "/a/f", "f2")
        for pos in range(len(program) + 1):
            vfs = small_spec()
            vfs.current_task = "T"
            obs1, handles = [], {}
            for i, step in enumerate(program):
                if i == pos:
                    vfs.current_task = None
                    run_mutations(vfs, [write])
                    vfs.current_task = "T"
                obs1 += run_task_program(vfs, [step], handles)
            if pos == len(program):
                vfs.current_task = None
                run_mutations(vfs, [write])
            kernel = AnalysisKernel()
            kernel.ingest(vfs.trace.records)
            kernel.evaluate()
            v = kernel.verdicts["T"]
            vfs2 = small_spec()
            run_mutations(vfs2, [write])
            obs2 = run_task_program(vfs2, program)
            reproducible = obs1 == obs2
            if v.status == VALID:
                self.assertTrue(reproducible,
                                f"pos={pos} 错误批准\n{kernel.explain('T')}")
            if pos == 0:
                # 写入发生在任务观察之前 => 观察已反映新内容，必须批准
                self.assertEqual(v.status, VALID)
                self.assertTrue(reproducible)
            if pos == 2:
                # 写入夹在两次读之间 => 快照不一致，绝不能批准
                self.assertNotEqual(v.status, VALID, f"pos={pos}")
                self.assertFalse(reproducible)
                self.assertIn("快照不一致", v.chain[0])


if __name__ == "__main__":
    unittest.main()
