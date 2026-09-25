import os
import tempfile
import unittest

from axcon import (Budget, Executor, SparseTensor, build_plan,
                   dense_contract)


def matmul_tensors():
    a = SparseTensor("A", ("i", "j"), (3, 4),
                     {(0, 0): 1.0, (0, 2): 2.0, (1, 1): 3.0, (2, 3): 4.0})
    b = SparseTensor("B", ("j", "k"), (4, 3),
                     {(0, 0): 5.0, (1, 1): 6.0, (2, 0): 7.0, (3, 2): 8.0})
    return a, b


class TestExecutor(unittest.TestCase):
    def test_matches_oracle_matmul(self):
        a, b = matmul_tensors()
        plan = build_plan([a, b], ("i", "k"))
        result = Executor().execute(plan, [a, b])
        self.assertEqual(result.tensor.entries,
                         dense_contract([a, b], ("i", "k")))

    def test_broadcast_outer_product(self):
        a = SparseTensor("A", ("i",), (3,), {(0,): 1.0, (2,): 2.0})
        b = SparseTensor("B", ("j",), (2,), {(0,): 3.0, (1,): 4.0})
        plan = build_plan([a, b], ("i", "j"))
        result = Executor().execute(plan, [a, b])
        self.assertEqual(result.tensor.entries,
                         {(0, 0): 3.0, (0, 1): 4.0,
                          (2, 0): 6.0, (2, 1): 8.0})

    def test_repeated_label_diagonal(self):
        d = SparseTensor("D", ("i", "i"), (3, 3),
                         {(0, 0): 2.0, (2, 2): 5.0})
        m = SparseTensor("M", ("i", "j"), (3, 2),
                         {(0, 0): 1.0, (1, 1): 1.0, (2, 1): 3.0})
        plan = build_plan([d, m], ("i", "j"))
        result = Executor().execute(plan, [d, m])
        self.assertEqual(result.tensor.entries,
                         dense_contract([d, m], ("i", "j")))
        self.assertEqual(result.tensor.entries,
                         {(0, 0): 2.0, (2, 1): 15.0})

    def test_huge_dims_without_dense_expansion(self):
        big = 10 ** 6
        a = SparseTensor("A", ("i", "j"), (big, big), {(3, 5): 2.0})
        b = SparseTensor("B", ("j", "k"), (big, big), {(5, 7): 3.0})
        plan = build_plan([a, b], ("i", "k"))
        result = Executor().execute(plan, [a, b])
        self.assertTrue(result.completed)
        self.assertEqual(result.tensor.entries, {(3, 7): 6.0})
        self.assertEqual(result.tensor.shape, (big, big))

    def test_spill_to_disk_preserves_result(self):
        a, b = matmul_tensors()
        plan = build_plan([a, b], ("i", "k"))
        workdir = tempfile.mkdtemp()
        budget = Budget(chunk_size=1, max_entries_in_memory=1)
        result = Executor(budget, workdir=workdir).execute(plan, [a, b])
        self.assertTrue(result.completed)
        self.assertEqual(result.tensor.entries,
                         dense_contract([a, b], ("i", "k")))
        self.assertGreater(result.stats["spilled_bytes"], 0)
        self.assertGreater(sum(s.spilled_bytes for s in plan.steps), 0)

    def test_budget_abort_marks_incomplete(self):
        a, b = matmul_tensors()
        plan = build_plan([a, b], ("i", "k"))
        budget = Budget(chunk_size=1, max_chunks=1)
        result = Executor(budget).execute(plan, [a, b])
        self.assertFalse(result.completed)
        self.assertIsNone(result.tensor)
        self.assertEqual(result.stats["abort_reason"], "budget-exceeded")
        self.assertFalse(result.plan.stats["completed"])

    def test_checkpoint_resume_after_interruption(self):
        a, b = matmul_tensors()
        plan = build_plan([a, b], ("i", "k"))
        workdir = tempfile.mkdtemp()
        crashy = Executor(Budget(chunk_size=1, interrupt_after_chunks=2),
                          workdir=workdir)
        crashed = crashy.execute(plan, [a, b])
        self.assertFalse(crashed.completed)
        self.assertEqual(crashed.stats["abort_reason"], "interrupted")
        self.assertTrue(os.path.exists(
            os.path.join(workdir, "checkpoint.json")))
        resumer = Executor(Budget(chunk_size=1), workdir=workdir)
        resumed = resumer.execute(plan, [a, b], resume=True)
        self.assertTrue(resumed.completed)
        self.assertEqual(resumed.tensor.entries,
                         dense_contract([a, b], ("i", "k")))

    def test_checkpoint_invalidated_when_inputs_change(self):
        a, b = matmul_tensors()
        plan = build_plan([a, b], ("i", "k"))
        workdir = tempfile.mkdtemp()
        crashy = Executor(Budget(chunk_size=1, interrupt_after_chunks=1),
                          workdir=workdir)
        crashed = crashy.execute(plan, [a, b])
        self.assertFalse(crashed.completed)
        a.apply_delta("dx", {(0, 1): 9.0})  # version bump invalidates state
        resumer = Executor(Budget(chunk_size=1), workdir=workdir)
        resumed = resumer.execute(plan, [a, b], resume=True)
        self.assertTrue(resumed.completed)
        self.assertEqual(resumed.tensor.entries,
                         dense_contract([a, b], ("i", "k")))


if __name__ == "__main__":
    unittest.main()
