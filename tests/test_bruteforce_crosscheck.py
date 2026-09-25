import random
import unittest

from evidence import (
    FEASIBLE,
    FactVersion,
    MutexRule,
    Query,
    exhaustive_feasible_intervals,
    solve,
)


def random_case(rng):
    dims = ("d1", "d2")
    sources = ("sA", "sB", "sC")
    facts = []
    for fid in range(rng.randint(2, 6)):
        recorded = 0
        for version in range(1, rng.randint(1, 3) + 1):
            recorded += rng.randint(0, 4)
            lo = rng.randint(0, 8)
            hi = rng.randint(lo + 1, 10)
            facts.append(
                FactVersion(
                    f"f{fid}",
                    version,
                    rng.choice(dims),
                    rng.choice(sources),
                    lo,
                    hi,
                    recorded_at=recorded,
                )
            )
    rules = []
    if rng.random() < 0.7:
        rules.append(MutexRule("r1", 1, frozenset({"sA", "sB"})))
    if rng.random() < 0.3:
        rules.append(MutexRule("r2", 1, frozenset({"sB", "sC"})))
    query = Query("q", rng.randint(0, 12), dims)
    return facts, rules, query


class BruteForceCrossCheckTest(unittest.TestCase):
    def test_solver_matches_exhaustive_oracle(self):
        rng = random.Random(20260925)
        for trial in range(300):
            facts, rules, query = random_case(rng)
            result = solve(facts, rules, query)
            got = [(s.start, s.end) for s in result.segments if s.status == FEASIBLE]
            want = exhaustive_feasible_intervals(facts, rules, query)
            self.assertEqual(
                want,
                got,
                msg=f"trial {trial} query={query} facts={facts} rules={rules}",
            )

    def test_incremental_matches_oracle_after_random_updates(self):
        from evidence import IncrementalEngine

        rng = random.Random(777)
        for trial in range(60):
            facts, rules, query = random_case(rng)
            engine = IncrementalEngine(rules, query)
            engine.load(facts)
            for uid in range(rng.randint(1, 4)):
                lo = rng.randint(0, 8)
                hi = rng.randint(lo + 1, 10)
                update = FactVersion(
                    f"f{rng.randint(0, 5)}",
                    rng.randint(1, 5),
                    rng.choice(query.dimensions),
                    rng.choice(("sA", "sB", "sC")),
                    lo,
                    hi,
                    recorded_at=rng.randint(0, 12),
                )
                engine.apply_update(update)
                facts.append(update)
            got = [(s.start, s.end) for s in engine.result.segments if s.status == FEASIBLE]
            want = exhaustive_feasible_intervals(facts, rules, query)
            self.assertEqual(want, got, msg=f"trial {trial}")
            self.assertTrue(engine.replay(), msg=f"trial {trial} replay")


if __name__ == "__main__":
    unittest.main()
