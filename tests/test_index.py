"""反向索引测试：无关子树不触碰、重算/删除无残留。"""
import unittest

from common import PROGRAMS, run_case, small_spec
from trace_kernel.kernel import AnalysisKernel
from trace_kernel.replayer import run_task_program


def _build_five_subtrees():
    vfs = small_spec()
    for i in range(1, 6):
        vfs.mkdir(f"/s{i}")
        vfs.create(f"/s{i}/x", f"c{i}")
    vfs.trace.records.clear()
    for i in range(1, 6):
        vfs.current_task = f"T{i}"
        run_task_program(vfs, [("open", f"/s{i}/x", "h"), ("read", "h")])
    vfs.current_task = None
    return vfs


class TestReverseIndex(unittest.TestCase):
    def test_only_affected_entries_touched(self):
        vfs = _build_five_subtrees()
        vfs.write("/s3/x", "changed")
        kernel = AnalysisKernel(record_access=True)
        kernel.ingest(vfs.trace.records)
        kernel.evaluate()
        # 只访问了 /s3/x 相关的索引项
        touched = set(kernel.index.access_log)
        self.assertTrue(touched, "应当有索引访问")
        s3_x_oid = kernel.events[0]["obj"]
        self.assertEqual(touched, {("content", s3_x_oid)})
        # 只重算了 T3，其余任务直接判定有效
        self.assertEqual(kernel.evaluated, {"T3"})
        self.assertEqual(kernel.verdicts["T3"].status, "invalid")
        for i in (1, 2, 4, 5):
            self.assertEqual(kernel.verdicts[f"T{i}"].status, "valid")

    def test_recompute_leaves_no_stale_deps(self):
        vfs = _build_five_subtrees()
        kernel = AnalysisKernel()
        kernel.ingest(vfs.trace.records)
        old_keys = kernel.index.keys_of("T3")
        self.assertTrue(old_keys)
        # 重算 T3：改为依赖 /s3/y
        vfs.current_task = "T3"
        vfs.create("/s3/y", "y1")
        recs = vfs.trace.records
        vfs.current_task = None
        new_recs = [r for r in recs if r.get("task") == "T3"]
        kernel.replace_task("T3", new_recs)
        for k in old_keys - kernel.index.keys_of("T3"):
            self.assertNotIn("T3", kernel.index._map.get(k, set()))
        # 删除任务后索引完全无残留
        kernel.remove_task("T3")
        self.assertEqual(kernel.index.keys_of("T3"), set())
        for holders in kernel.index._map.values():
            self.assertNotIn("T3", holders)


if __name__ == "__main__":
    unittest.main()
