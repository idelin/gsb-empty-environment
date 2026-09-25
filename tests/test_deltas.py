import tempfile
import unittest

from axcon import (Budget, ChunkIndexCache, Executor, SparseTensor,
                   SubexpressionCache, build_plan, dense_contract)


def chain():
    a = SparseTensor("A", ("i", "j"), (3, 4),
                     {(0, 0): 1.0, (0, 2): 2.0, (1, 1): 3.0, (2, 3): 4.0})
    b = SparseTensor("B", ("j", "k"), (4, 2),
                     {(0, 0): 5.0, (1, 1): 6.0, (2, 0): 7.0, (3, 1): 8.0})
    c = SparseTensor("C", ("k", "l"), (2, 3),
                     {(0, 0): 1.0, (0, 2): 2.0, (1, 1): 3.0})
    return a, b, c


class TestDeltas(unittest.TestCase):
    def test_delta_application_is_idempotent(self):
        t = SparseTensor("T", ("i",), (3,), {(0,): 1.0})
        version = t.version
        self.assertTrue(t.apply_delta("d", {(1,): 2.0}))
        self.assertEqual(t.version, version + 1)
        entries = t.entries
        self.assertFalse(t.apply_delta("d", {(1,): 2.0}))  # no double apply
        self.assertEqual(t.version, version + 1)
        self.assertEqual(t.entries, entries)

    def test_retract_delta_restores_entries(self):
        t = SparseTensor("T", ("i",), (3,), {(0,): 1.0})
        before = t.entries
        t.apply_delta("d", {(0,): 4.0, (2,): 7.0})
        self.assertTrue(t.retract_delta("d"))
        self.assertEqual(t.entries, before)
        self.assertFalse(t.retract_delta("d"))  # unknown delta

    def test_chunk_invalidation_only_touches_related_buckets(self):
        cache = ChunkIndexCache(num_buckets=4)
        t = SparseTensor("T", ("i", "j"), (8, 8),
                         {(k, k): float(k + 1) for k in range(5)})
        cache.get_join_index(t, ("j",))
        self.assertEqual(cache.bucket_builds, 4)
        t.apply_delta("d", {(5, 5): 6.0})  # bucket 5 % 4 == 1
        cache.invalidate_delta(t, {(5, 5): 6.0})
        before = cache.bucket_builds
        index = cache.get_join_index(t, ("j",))
        self.assertEqual(cache.bucket_builds - before, 1)  # only bucket 1
        self.assertIn(((5, 5), 6.0), index[(5,)])

    def test_subexpression_cache_bound_to_versions(self):
        a, b, c = chain()
        tensors = [a, b, c]
        plan = build_plan(tensors, ("i", "l"))
        cache = SubexpressionCache()
        workdir = tempfile.mkdtemp()
        executor = Executor(subexpr_cache=cache, workdir=workdir)
        first = executor.execute(plan, tensors)
        self.assertEqual(cache.hits, 0)
        second = executor.execute(plan, tensors)
        self.assertEqual(second.tensor.entries, first.tensor.entries)
        self.assertGreater(cache.hits, 0)  # unchanged inputs -> cache hits
        c.apply_delta("d1", {(0, 1): 2.0})
        hits_before = cache.hits
        third = executor.execute(plan, tensors)
        self.assertEqual(third.tensor.entries,
                         dense_contract(tensors, ("i", "l")))
        # step0 (A*B) does not involve C and must be served from cache
        self.assertGreater(cache.hits, hits_before)

    def test_repeated_delta_after_crash_not_applied_twice(self):
        a, b, c = chain()
        tensors = [a, b, c]
        plan = build_plan(tensors, ("i", "l"))
        b.apply_delta("d2", {(3, 0): 1.0})
        version_after_delta = b.version
        workdir = tempfile.mkdtemp()
        crashy = Executor(Budget(chunk_size=1, interrupt_after_chunks=1),
                          workdir=workdir)
        crashed = crashy.execute(plan, tensors)
        self.assertFalse(crashed.completed)
        # client retries the same delta after the crash: must be a no-op
        self.assertFalse(b.apply_delta("d2", {(3, 0): 1.0}))
        self.assertEqual(b.version, version_after_delta)
        resumer = Executor(Budget(chunk_size=1), workdir=workdir)
        resumed = resumer.execute(plan, tensors, resume=True)
        self.assertTrue(resumed.completed)
        self.assertEqual(resumed.tensor.entries,
                         dense_contract(tensors, ("i", "l")))


if __name__ == "__main__":
    unittest.main()
