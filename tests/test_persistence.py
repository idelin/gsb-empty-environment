"""证据持久化与恢复：保存 -> 恢复 -> 结论一致。"""
import os
import tempfile
import unittest

from common import MUTATIONS, PROGRAMS, run_case
from trace_kernel.index import evidence_keys
from trace_kernel.kernel import AnalysisKernel


class TestPersistence(unittest.TestCase):
    def test_restore_gives_same_verdicts(self):
        kernel, _ = run_case(PROGRAMS["P-read-file"], MUTATIONS["M-write-f"])
        kernel2, _ = run_case(PROGRAMS["P-failed-lookup"],
                              MUTATIONS["M-create-missing"], task="T2")
        kernel.evidence.update(kernel2.evidence)
        for t, ev in kernel2.evidence.items():
            kernel.index.add(t, evidence_keys(ev))
        kernel.events += kernel2.events
        kernel.evaluate()
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "evidence.json")
            kernel.save(p)
            restored = AnalysisKernel.restore(p, events=kernel.events)
            restored.evaluate()
        self.assertEqual(set(kernel.verdicts), set(restored.verdicts))
        for t in kernel.verdicts:
            a, b = kernel.verdicts[t], restored.verdicts[t]
            self.assertEqual(a.status, b.status)
            self.assertEqual(a.chain, b.chain)


if __name__ == "__main__":
    unittest.main()
