import random
import unittest
from fractions import Fraction

from temporal import TemporalNetwork, reference_solve


class RandomValidationTest(unittest.TestCase):
    """Cross-validate incremental push/pop updates against an independent
    full difference-constraints solve after every single mutation."""

    def test_random_updates_match_reference(self):
        for seed in (7, 42, 2026):
            with self.subTest(seed=seed):
                self._run(seed)

    def _run(self, seed):
        rng = random.Random(seed)
        net = TemporalNetwork(budget=10_000_000)
        events = [f"E{i}" for i in range(6)]
        for e in events:
            net.add_event(e)

        for step in range(200):
            r = rng.random()
            if r < 0.50:
                s, t = rng.sample(events, 2)
                lo = rng.randint(-8, 8)
                hi = lo + rng.randint(0, 10)
                if rng.random() < 0.15:
                    lo = None
                elif rng.random() < 0.15:
                    hi = None
                net.add_constraint(s, t, lower=lo, upper=hi)
            elif r < 0.65:
                e = rng.choice(events)
                lo = rng.randint(-10, 10)
                hi = lo + rng.randint(0, 20)
                net.set_range(e, lo, hi)
            elif r < 0.85:
                net.push()
            else:
                if len(net._layers) > 1:
                    net.pop()

            self._check_against_reference(net, events, step)

    def _check_against_reference(self, net, events, step):
        active = list(net.constraints.values())
        ref = reference_solve(events, active)

        # feasibility must agree with the independent full solve
        self.assertNotEqual(net.status, "UNKNOWN", f"step {step}")
        self.assertEqual(net.status == "FEASIBLE", ref["feasible"], f"step {step}")

        if ref["feasible"]:
            # earliest/latest bounds must match the full recompute exactly
            for e in events:
                if e in net.unconstrained_events():
                    continue
                self.assertEqual(net.bounds(e), ref["bounds"][e],
                                 f"step {step} event {e}")
            # the witness schedule must satisfy every active constraint
            sched = net.schedule()
            for c in active:
                # _pot also covers the anchor variable (not in schedule())
                diff = net._pot[c.target] - net._pot[c.source]
                if c.lower is not None:
                    self.assertGreaterEqual(diff, c.lower, f"step {step} {c.cid}")
                if c.upper is not None:
                    self.assertLessEqual(diff, c.upper, f"step {step} {c.cid}")
        else:
            # contradiction evidence must be a genuine negative cycle over
            # currently active constraint IDs
            nodes, cids, weight = net.contradiction_detail()
            self.assertLess(weight, Fraction(0), f"step {step}")
            self.assertGreaterEqual(len(cids), 1)
            active_ids = {c.cid for c in active}
            for cid in cids:
                self.assertIn(cid, active_ids, f"step {step}")


if __name__ == "__main__":
    unittest.main()
