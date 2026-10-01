import json
import os
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from lottery_assign.prng import SplitMix64
from lottery_assign.solver import (
    Award,
    Candidate,
    Problem,
    brute_force_lexmin,
    edge_order,
    solve,
)

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def boundary_problem(seed: int) -> Problem:
    """Candidate a can only take x; b can take x or y. Optimum is 2."""
    return Problem(
        candidates=[Candidate("a", "g1"), Candidate("b", "g2")],
        awards=[
            Award("x", 1, (("g1", 1), ("g2", 1))),
            Award("y", 1, (("g2", 1),)),
        ],
        forbidden=frozenset(),
        seed=seed,
    )


def pairs(result):
    return sorted((a["candidate"], a["award"]) for a in result["assignments"])


class TestPrng(unittest.TestCase):
    def test_fixed_reference_sequence_seed0(self):
        # Public SplitMix64 reference values for seed 0 (locks the definition).
        rng = SplitMix64(0)
        self.assertEqual(
            [rng.next_u64() for _ in range(5)],
            [
                16294208416658607535,  # 0xE220A8397B1DCDAF
                7960286522194355700,   # 0x6E789E6AA1B965F4
                487617019471545679,    # 0x06C45D188A6A0C4F
                17909611376780542444,
                1961750202426094747,
            ],
        )

    def test_fixed_reference_sequence_seed12345(self):
        rng = SplitMix64(12345)
        self.assertEqual(
            [rng.next_u64() for _ in range(3)],
            [2454886589211414944, 3778200017661327597, 2205171434679333405],
        )

    def test_same_seed_same_stream(self):
        self.assertEqual(
            [SplitMix64(42).next_u64() for _ in range(3)],
            [SplitMix64(42).next_u64() for _ in range(3)],
        )


class TestEdgeOrder(unittest.TestCase):
    def test_order_is_total_and_deterministic(self):
        problem = boundary_problem(7)
        edges = problem.feasible_edges()
        ordered1, draws1 = edge_order(edges, 7)
        ordered2, draws2 = edge_order(edges, 7)
        self.assertEqual(ordered1, ordered2)
        self.assertEqual(draws1, draws2)
        self.assertEqual(sorted(ordered1), sorted(edges))

    def test_seed_changes_order(self):
        problem = boundary_problem(0)
        edges = problem.feasible_edges()
        orders = {tuple(edge_order(edges, s)[0]) for s in range(20)}
        self.assertGreater(len(orders), 1)


class TestAcceptanceBoundary(unittest.TestCase):
    def test_a_gets_x_b_gets_y_for_many_seeds(self):
        for seed in range(50):
            with self.subTest(seed=seed):
                result = solve(boundary_problem(seed))
                self.assertEqual(result["optimum"], 2)
                self.assertEqual(pairs(result), [("a", "x"), ("b", "y")])

    def test_seed_exists_where_naive_greedy_would_underfill(self):
        # There must be a seed whose random order makes a naive "scan and take
        # any augmenting edge" greedy pick b-x first, after which a is stuck.
        # The solver must still deliver the full optimum for that seed.
        found = False
        for seed in range(200):
            problem = boundary_problem(seed)
            edges = problem.feasible_edges()
            ordered, _ = edge_order(edges, seed)
            if ordered[0] == ("b", "x"):
                found = True
                # Naive greedy in the random order: take an edge if both
                # endpoints are still free (no look-ahead).
                used_c, used_a, naive = set(), set(), []
                for c, a in ordered:
                    if c not in used_c and a not in used_a:
                        naive.append((c, a))
                        used_c.add(c)
                        used_a.add(a)
                self.assertEqual(len(naive), 1, "greedy should underfill here")
                result = solve(problem)
                self.assertEqual(result["num_assigned"], 2)
                self.assertEqual(pairs(result), [("a", "x"), ("b", "y")])
                break
        self.assertTrue(found, "no seed in 0..199 puts edge (b,x) first")


class TestConstraints(unittest.TestCase):
    def test_forbidden_and_quotas_and_one_award_per_person(self):
        problem = Problem(
            candidates=[Candidate(f"c{i}", "g1" if i % 2 == 0 else "g2") for i in range(6)],
            awards=[
                Award("x", 1, (("g1", 1), ("g2", 1))),
                Award("y", 2, (("g1", 1), ("g2", 1))),
                Award("z", 1, (("g2", 1),)),
            ],
            forbidden=frozenset({("c0", "x"), ("c1", "y")}),
            seed=99,
        )
        result = solve(problem)
        assigned_candidates = [a["candidate"] for a in result["assignments"]]
        self.assertEqual(len(assigned_candidates), len(set(assigned_candidates)))
        for a in result["assignments"]:
            self.assertNotIn((a["candidate"], a["award"]), problem.forbidden)
        # Per-award capacity and per-group quotas.
        count = {}
        group_count = {}
        group_of = {c.id: c.group for c in problem.candidates}
        award_by_id = {a.id: a for a in problem.awards}
        for a in result["assignments"]:
            count[a["award"]] = count.get(a["award"], 0) + 1
            key = (a["award"], group_of[a["candidate"]])
            group_count[key] = group_count.get(key, 0) + 1
        for aid, n in count.items():
            self.assertLessEqual(n, award_by_id[aid].capacity)
        for (aid, g), n in group_count.items():
            self.assertLessEqual(n, award_by_id[aid].quota_for(g))
        # Optimum agrees with the flow bound.
        self.assertEqual(result["num_assigned"], result["optimum"])


class TestLexMinOptimality(unittest.TestCase):
    def test_matches_brute_force_on_random_small_instances(self):
        # Deterministic pseudo-random instance generator driven by SplitMix64
        # itself (no runtime randomness in the tests either).
        for case in range(30):
            rng = SplitMix64(1000 + case)
            n_cand = 2 + rng.next_u64() % 4
            n_award = 2 + rng.next_u64() % 3
            groups = ["g1", "g2"]
            candidates = [
                Candidate(f"c{i}", groups[rng.next_u64() % 2]) for i in range(n_cand)
            ]
            awards = []
            for j in range(n_award):
                quotas = tuple(
                    (g, 1 + rng.next_u64() % 2) for g in groups if rng.next_u64() % 4 != 0
                )
                if not quotas:
                    quotas = (("g1", 1),)
                awards.append(Award(f"a{j}", 1, quotas))
            forbidden = frozenset(
                (f"c{i}", f"a{j}")
                for i in range(n_cand)
                for j in range(n_award)
                if rng.next_u64() % 5 == 0
            )
            seed = rng.next_u64()
            problem = Problem(candidates, awards, forbidden, seed)
            with self.subTest(case=case):
                result = solve(problem)
                edges = problem.feasible_edges()
                ordered, _ = edge_order(edges, seed)
                rank_of = {e: p for p, e in enumerate(ordered)}
                actual = tuple(sorted(rank_of[(c, a)] for c, a in pairs(result)))
                expected = brute_force_lexmin(problem)
                self.assertEqual(actual, expected)


class TestCli(unittest.TestCase):
    def run_cli(self, *argv, env_extra=None, cwd=REPO_ROOT):
        env = dict(os.environ)
        if env_extra:
            env.update(env_extra)
        return subprocess.run(
            [sys.executable, "-m", "lottery_assign", *argv],
            capture_output=True,
            text=True,
            cwd=cwd,
            env=env,
        )

    def test_solve_and_verify_boundary_example(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = os.path.join(tmp, "result.json")
            proc = self.run_cli(
                "solve", "examples/boundary.json", "-o", out, "--exhaustive-check"
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            with open(out) as fh:
                result = json.load(fh)
            self.assertEqual(pairs(result), [("a", "x"), ("b", "y")])
            self.assertEqual(result["optimum"], 2)
            self.assertEqual(result["exhaustive_check"], "passed")

            proc = self.run_cli("verify", "examples/boundary.json", out)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("verify OK", proc.stdout)

    def test_verify_rejects_tampered_result(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = os.path.join(tmp, "result.json")
            proc = self.run_cli("solve", "examples/boundary.json", "-o", out)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            with open(out) as fh:
                result = json.load(fh)
            result["assignments"] = [
                {"candidate": "b", "award": "x", "group": "g2", "edge_rank": 0}
            ]
            with open(out, "w") as fh:
                json.dump(result, fh)
            proc = self.run_cli("verify", "examples/boundary.json", out)
            self.assertEqual(proc.returncode, 1)
            self.assertIn("verify FAILED", proc.stderr)

    def test_reproducible_across_process_restarts(self):
        outputs = []
        for _ in range(2):
            proc = self.run_cli("solve", "examples/demo.json")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            outputs.append(proc.stdout)
        self.assertEqual(outputs[0], outputs[1])

    def test_independent_of_python_hash_seed(self):
        outputs = []
        for hash_seed in ("0", "1", "42"):
            proc = self.run_cli(
                "solve", "examples/demo.json", env_extra={"PYTHONHASHSEED": hash_seed}
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            outputs.append(proc.stdout)
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[1], outputs[2])

    def test_seed_override_flag(self):
        proc1 = self.run_cli("solve", "examples/demo.json", "--seed", "1")
        proc2 = self.run_cli("solve", "examples/demo.json", "--seed", "2")
        self.assertEqual(proc1.returncode, 0, proc1.stderr)
        self.assertEqual(proc2.returncode, 0, proc2.stderr)
        r1, r2 = json.loads(proc1.stdout), json.loads(proc2.stdout)
        self.assertEqual(r1["seed"], 1)
        self.assertEqual(r2["seed"], 2)


if __name__ == "__main__":
    unittest.main()
