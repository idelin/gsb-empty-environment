import io
import json
import unittest
from contextlib import redirect_stdout
from pathlib import Path

from recon import model, onetoone
from recon.cli import main as cli_main

EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


def make_instance(left, right, window=600):
    return model.load_instance({"window": window, "left": left, "right": right})


def row(rid, keys, amount, time):
    return {"id": rid, "keys": keys, "amount": amount, "time": time}


class AcceptanceBoundaryTest(unittest.TestCase):
    """Left a candidates {x, y}; left b only {x}. Maximum matching must be
    a<->r_y and b<->r_x; a greedy a<->r_x choice strands b."""

    def setUp(self):
        self.inst = model.load_instance(EXAMPLES / "onetoone_key_conflict.json")
        self.report = onetoone.solve(self.inst)

    def test_optimal_pairs(self):
        pairs = {(p["left"], p["right"]) for p in self.report["pairs"]}
        self.assertEqual(pairs, {("a", "r_y"), ("b", "r_x")})
        self.assertEqual(self.report["objective"]["matched_count"], 2)

    def test_greedy_would_lose_b(self):
        # Simulate the naive greedy: a takes its first candidate r_x.
        edges = onetoone.build_edges(self.inst.left, self.inst.right,
                                     self.inst.window)
        adj = {}
        for (li, ri) in edges:
            adj.setdefault(li, []).append(ri)
        greedy = {}
        used = set()
        for li in sorted(adj, key=lambda i: self.inst.left[i].id):
            for ri in sorted(adj[li], key=lambda j: self.inst.right[j].id):
                if ri not in used:
                    greedy[li] = ri
                    used.add(ri)
                    break
        greedy_pairs = {(self.inst.left[l].id, self.inst.right[r].id)
                        for l, r in greedy.items()}
        self.assertEqual(greedy_pairs, {("a", "r_x")})  # b stranded
        self.assertLess(len(greedy_pairs),
                        self.report["objective"]["matched_count"])

    def test_optimality_evidence(self):
        opt = self.report["optimality"]
        self.assertTrue(opt["max_cardinality"]["no_augmenting_path"])
        self.assertTrue(opt["min_cost"]["dual_feasible"])
        self.assertTrue(opt["lexicographic"]["recomputed_equal"])


class FeasibilityTest(unittest.TestCase):
    def test_requires_shared_key_equal_amount_within_window(self):
        inst = make_instance(
            left=[row("l1", ["k1"], "10.00", 0),
                  row("l2", ["k2"], "10.00", 0),
                  row("l3", ["k3"], "10.01", 0),
                  row("l4", ["k4"], "10.00", 1000)],
            right=[row("r1", ["k1", "k2", "k3", "k4"], "10.00", 0)],
            window=60,
        )
        report = onetoone.solve(inst)
        # l3 fails amount, l4 fails the window; l1/l2 compete for r1.
        self.assertEqual(report["objective"]["matched_count"], 1)
        pair = report["pairs"][0]
        self.assertIn((pair["left"], pair["right"]),
                      {("l1", "r1"), ("l2", "r1")})

    def test_row_used_at_most_once(self):
        inst = make_instance(
            left=[row("l1", ["k"], "5", 0), row("l2", ["k"], "5", 0)],
            right=[row("r1", ["k"], "5", 0)],
        )
        report = onetoone.solve(inst)
        self.assertEqual(report["objective"]["matched_count"], 1)


class ObjectiveOrderTest(unittest.TestCase):
    def test_min_total_time_diff_after_max_count(self):
        # Two max matchings of size 1; the smaller diff must win.
        inst = make_instance(
            left=[row("l", ["k"], "10", 100)],
            right=[row("r_far", ["k"], "10", 150),
                   row("r_near", ["k"], "10", 105)],
        )
        report = onetoone.solve(inst)
        self.assertEqual(report["pairs"][0]["right"], "r_near")
        self.assertEqual(report["objective"]["total_time_diff"], 5)

    def test_lexicographic_tie_break(self):
        # l1<->r1 + l2<->r2 and l1<->r2 + l2<->r1 both have diff 20.
        inst = make_instance(
            left=[row("l1", ["k"], "10", 0), row("l2", ["k"], "10", 20)],
            right=[row("r1", ["k"], "10", 10), row("r2", ["k"], "10", 30)],
        )
        report = onetoone.solve(inst)
        self.assertEqual(report["objective"]["total_time_diff"], 20)
        self.assertEqual(report["objective"]["pair_ids"],
                         [["l1", "r1"], ["l2", "r2"]])


class UnmatchedClassificationTest(unittest.TestCase):
    def test_no_candidates_vs_competition(self):
        report = onetoone.solve(
            model.load_instance(EXAMPLES / "onetoone_unmatched.json"))
        unmatched = {e["id"]: e for e in report["unmatched"]["left"]}
        self.assertEqual(unmatched["c"]["reason"], "no_candidates")
        self.assertIsNone(unmatched["c"]["evidence"])
        self.assertEqual(unmatched["d"]["reason"], "competition")
        ev = unmatched["d"]["evidence"]
        self.assertFalse(ev["augmenting_path_found"])
        self.assertGreaterEqual(ev["hall_deficiency"], 1)
        self.assertIn("r1", ev["reachable_other_side"])


class CliTest(unittest.TestCase):
    def test_cli_onetoone(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            rc = cli_main(["onetoone",
                           str(EXAMPLES / "onetoone_key_conflict.json")])
        self.assertEqual(rc, 0)
        report = json.loads(buf.getvalue())
        self.assertEqual(report["objective"]["matched_count"], 2)


if __name__ == "__main__":
    unittest.main()
