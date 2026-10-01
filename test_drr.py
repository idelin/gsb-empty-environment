"""Unittest suite for the flat DRR scheduler (synthetic data only)."""
import unittest

from drr import FlatDRR, InfeasibleTaskError


class AcceptanceBoundaryTest(unittest.TestCase):
    """Acceptance boundary: weight 1, quantum 2, head cost 5."""

    def test_cap5_executes_on_third_round(self):
        s = FlatDRR(quantum=2, deficit_cap=5)
        s.add_tenant("A", weight=1)
        s.enqueue("A", "t1", 5)

        e1 = s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 2)
        self.assertFalse(any(e["event"] == "dequeue" for e in e1))
        self.assertTrue(any(e["event"] == "head_blocked" for e in e1))

        e2 = s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 4)
        self.assertFalse(any(e["event"] == "dequeue" for e in e2))

        e3 = s.run_round()
        deq = [e for e in e3 if e["event"] == "dequeue"]
        self.assertEqual(len(deq), 1)
        self.assertEqual(deq[0]["task"], "t1")
        self.assertEqual(deq[0]["cost"], 5)
        self.assertEqual(deq[0]["deficit_before"], 5)  # 4 + 2 capped at 5
        self.assertEqual(deq[0]["deficit_after"], 0)
        self.assertEqual(s.tenants["A"].deficit, 0)

        credits = [e for e in e1 + e2 + e3 if e["event"] == "credit"]
        self.assertEqual([c["deficit_after"] for c in credits], [2, 4, 5])
        self.assertEqual([c["round"] for c in credits], [1, 2, 3])

    def test_cap4_rejects_task_explicitly(self):
        s = FlatDRR(quantum=2, deficit_cap=4)
        s.add_tenant("A", weight=1)
        ev = s.try_enqueue("A", "big", 5)
        self.assertEqual(ev["event"], "reject")
        self.assertEqual(ev["reason"], "cost_exceeds_deficit_cap")
        self.assertEqual(ev["cap"], 4)
        self.assertEqual(len(s.tenants["A"].queue), 0)
        with self.assertRaises(InfeasibleTaskError):
            s.enqueue("A", "big", 5)


class DynamicsTest(unittest.TestCase):
    def test_pause_freezes_and_resume_continues(self):
        s = FlatDRR(quantum=2, deficit_cap=10)
        s.add_tenant("A", weight=1)
        s.enqueue("A", "t1", 9)
        s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 2)
        s.pause("A")
        events = s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 2)  # frozen, no credit
        self.assertIn({"event": "skip", "round": 2, "tenant": "A", "reason": "paused"}, events)
        s.resume("A")
        s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 4)

    def test_dynamic_weight_update(self):
        s = FlatDRR(quantum=2, deficit_cap=20)
        s.add_tenant("A", weight=1)
        s.enqueue("A", "t1", 20)
        s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 2)
        s.set_weight("A", 3)
        s.run_round()
        self.assertEqual(s.tenants["A"].deficit, 2 + 6)  # new weight applies

    def test_deficit_never_exceeds_cap(self):
        s = FlatDRR(quantum=3, deficit_cap=5)
        s.add_tenant("A", weight=4)
        s.enqueue("A", "t1", 5)
        for _ in range(4):
            s.run_round()
            self.assertLessEqual(s.tenants["A"].deficit, 5)

    def test_idle_tenant_deficit_reset(self):
        s = FlatDRR(quantum=2, deficit_cap=10)
        s.add_tenant("A", weight=1)
        s.enqueue("A", "t1", 1)
        s.run_round()  # credit 2, serve cost 1, deficit 1 left
        self.assertEqual(s.tenants["A"].deficit, 1)
        events = s.run_round()  # queue empty -> reset
        self.assertEqual(s.tenants["A"].deficit, 0)
        self.assertTrue(any(e["event"] == "reset" for e in events))


class StarvationFreedomTest(unittest.TestCase):
    def test_big_feasible_task_served_within_bound(self):
        # Any feasible head task is served within ceil(cost/(weight*quantum))
        # rounds; verify across a sweep of synthetic configurations.
        for weight, quantum, cap, cost in [
            (1, 2, 5, 5), (1, 1, 7, 7), (2, 3, 11, 10), (3, 2, 9, 9),
        ]:
            s = FlatDRR(quantum=quantum, deficit_cap=cap)
            s.add_tenant("A", weight=weight)
            s.enqueue("A", "big", cost)
            bound = s.max_rounds_to_serve("A", cost)
            served_round = None
            for _ in range(bound):
                events = s.run_round()
                if any(e["event"] == "dequeue" for e in events):
                    served_round = s.round
                    break
            self.assertIsNotNone(served_round, f"starved: w{weight} q{quantum} cap{cap} c{cost}")
            self.assertLessEqual(served_round, bound)

    def test_fair_shares_between_two_tenants(self):
        s = FlatDRR(quantum=1, deficit_cap=100)
        s.add_tenant("A", weight=1)
        s.add_tenant("B", weight=3)
        for i in range(40):
            s.enqueue("A", f"a{i}", 1)
            s.enqueue("B", f"b{i}", 1)
        events = s.run_until_idle()
        served_a = sum(1 for e in events if e["event"] == "dequeue" and e["tenant"] == "A")
        served_b = sum(1 for e in events if e["event"] == "dequeue" and e["tenant"] == "B")
        self.assertEqual((served_a, served_b), (40, 40))  # all work done
        # Per-round service ratio tracks weights: in any single round B
        # serves at most 3x the quantum share of A.
        rounds = {}
        for e in events:
            if e["event"] == "dequeue":
                rounds.setdefault(e["round"], {}).setdefault(e["tenant"], 0)
                rounds[e["round"]][e["tenant"]] += 1
        for r, counts in rounds.items():
            if "A" in counts and "B" in counts:
                self.assertLessEqual(counts["B"], 3 * counts["A"] + 3)


if __name__ == "__main__":
    unittest.main()
