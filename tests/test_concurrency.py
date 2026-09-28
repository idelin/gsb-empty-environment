import threading
import unittest
from concurrent.futures import ThreadPoolExecutor

from dp_synth import Condition, CountQuery, DPQueryEngine, Predicate, make_synthetic_employee_data


class ConcurrencyTests(unittest.TestCase):
    def test_identical_concurrent_queries_coalesced(self):
        data = make_synthetic_employee_data(n=800, seed=5)
        engine = DPQueryEngine(total_epsilon_per_version=4.0, rng_seed=9)
        q = CountQuery(
            predicate=Predicate((Condition("city", "in", ["Beijing", "Shenzhen"]),)),
            epsilon=1.0,
        )
        barrier = threading.Barrier(16)

        def call(_):
            barrier.wait()
            return engine.run(q, data)

        with ThreadPoolExecutor(max_workers=16) as pool:
            results = list(pool.map(call, range(16)))

        values = {r.value for r in results}
        self.assertEqual(len(values), 1)
        self.assertAlmostEqual(engine.remaining_budget(data.version), 3.0)
        entries = engine._ledger(data.version).entries
        self.assertEqual(sum(1 for e in entries if e.kind == "charge"), 1)
        self.assertEqual(sum(1 for e in entries if e.kind == "reuse"), 15)
        self.assertEqual(len(engine.cached_keys(data.version)), 1)

    def test_mixed_distinct_queries_charged_separately(self):
        data = make_synthetic_employee_data(n=800, seed=5)
        engine = DPQueryEngine(total_epsilon_per_version=4.0, rng_seed=9)
        queries = [
            CountQuery(epsilon=1.0, threshold=float(i))
            for i in range(4)
        ]

        with ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(lambda q: engine.run(q, data), queries * 3))

        self.assertEqual(len(engine.cached_keys(data.version)), 4)
        self.assertAlmostEqual(engine.remaining_budget(data.version), 0.0)
        self.assertEqual({results[0].value}, {results[0].value})
        for i in range(4):
            self.assertEqual(results[i].value, results[i + 4].value)
            self.assertEqual(results[i].value, results[i + 8].value)


if __name__ == "__main__":
    unittest.main()
