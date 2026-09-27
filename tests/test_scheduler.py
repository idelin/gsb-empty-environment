"""Unittest suite for the maintenance-aware single-machine scheduler."""

import json
import random
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import scheduler


ROOT = Path(__file__).resolve().parent.parent


def make_instance(jobs, maintenance=()):
    return {"maintenance": [list(w) for w in maintenance], "jobs": jobs}


def job(job_id, release, due, duration, weight=1, predecessors=()):
    return {
        "id": job_id,
        "release": release,
        "due": due,
        "duration": duration,
        "weight": weight,
        "predecessors": list(predecessors),
    }


def independent_overlap(start_a, end_a, start_b, end_b):
    """Independent half-open interval overlap check for the brute-force oracle."""
    return start_a < end_b and start_b < end_a


def brute_force_optimum(instance):
    """Enumerate every ordered subset; return (weight, -sum_completion).

    The placement rule is intentionally written independently of scheduler.py:
    at every position we try every legal integer start time explicitly.
    """
    jobs, maintenance = scheduler.parse_instance(instance)
    by_id = {j.id: j for j in jobs}
    horizon = max(j.due for j in jobs) + 1

    best_key = None

    def walk(chosen, used, placed):
        nonlocal best_key
        weight_sum = sum(by_id[name].weight for name in chosen)
        finish_sum = sum(end for _, _, end in placed)
        key = (weight_sum, -finish_sum)
        if best_key is None or key > best_key:
            best_key = key

        for candidate in jobs:
            if candidate.id in used:
                continue
            if not all(pred in used for pred in candidate.predecessors):
                continue
            pred_end = 0
            for pred in candidate.predecessors:
                pred_end = max(
                    pred_end, next(end for name, _, end in placed if name == pred)
                )
            machine_free = max([0] + [end for _, _, end in placed])
            for start in range(max(candidate.release, pred_end, machine_free), horizon):
                end = start + candidate.duration
                if end > candidate.due:
                    break
                if any(
                    independent_overlap(start, end, w0, w1) for w0, w1 in maintenance
                ):
                    continue
                if any(
                    independent_overlap(start, end, p_start, p_end)
                    for _, p_start, p_end in placed
                ):
                    continue
                walk(
                    chosen + [candidate.id],
                    used | {candidate.id},
                    placed + [(candidate.id, start, end)],
                )
                break  # the earliest feasible start dominates later ones

    walk([], set(), [])
    return best_key


class AcceptanceTests(unittest.TestCase):
    def test_acceptance_boundary_half_open(self):
        # Maintenance [3,5): duration-3 job may run [0,3), never [1,4).
        instance = make_instance(
            [job("A", 0, 3, 3), job("B", 1, 4, 3)], maintenance=[(3, 5)]
        )
        result = scheduler.solve(instance)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual([(s["id"], s["start"], s["end"]) for s in result["scheduled"]],
                         [("A", 0, 3)])
        self.assertEqual(result["statistics"]["total_weight"], 1)
        reasons = {u["id"]: u["reason"] for u in result["unscheduled"]}
        self.assertEqual(reasons["B"], "no_feasible_slot")

    def test_completion_exactly_at_due_is_on_time(self):
        instance = make_instance(
            [job("A", 0, 2, 2), job("B", 0, 5, 3)], maintenance=[]
        )
        result = scheduler.solve(instance)
        by_id = {s["id"]: s for s in result["scheduled"]}
        # A [0,2), B [2,5): B ends exactly at due 5 -> on time.
        self.assertEqual((by_id["A"]["start"], by_id["A"]["end"]), (0, 2))
        self.assertEqual((by_id["B"]["start"], by_id["B"]["end"]), (2, 5))
        self.assertEqual(len(result["scheduled"]), 2)

    def test_touching_maintenance_is_allowed(self):
        instance = make_instance([job("A", 0, 10, 3)], maintenance=[(3, 5)])
        result = scheduler.solve(instance)
        self.assertEqual(
            (result["scheduled"][0]["start"], result["scheduled"][0]["end"]),
            (0, 3),
        )

    def test_run_starts_at_maintenance_end_is_allowed(self):
        instance = make_instance([job("A", 0, 10, 3)], maintenance=[(0, 5)])
        result = scheduler.solve(instance)
        item = result["scheduled"][0]
        self.assertEqual((item["start"], item["end"]), (5, 8))


class ObjectiveTests(unittest.TestCase):
    def test_weight_beats_count(self):
        # Weight-3 job alone dominates two weight-1 jobs that occupy same slot.
        instance = make_instance(
            [
                job("heavy", 0, 2, 2, weight=3),
                job("lite1", 0, 2, 1, weight=1),
                job("lite2", 0, 2, 1, weight=1),
            ]
        )
        result = scheduler.solve(instance)
        ids = {s["id"] for s in result["scheduled"]}
        self.assertEqual(ids, {"heavy"})
        self.assertEqual(result["statistics"]["total_weight"], 3)

    def test_secondary_total_completion_tiebreak(self):
        # Two weight-1 jobs, only one fits the single pre-maintenance slot.
        instance = make_instance(
            [job("early", 0, 1, 1), job("late", 0, 1, 1)],
            maintenance=[(1, 100)],
        )
        result = scheduler.solve(instance)
        self.assertEqual(result["statistics"]["total_weight"], 1)
        self.assertEqual(result["statistics"]["total_completion_time"], 1)

    def test_schedule_picks_lower_completion_sequence(self):
        instance = make_instance(
            [job("short", 0, 10, 1), job("long", 0, 10, 3)]
        )
        result = scheduler.solve(instance)
        self.assertEqual([s["id"] for s in result["scheduled"]], ["short", "long"])
        self.assertEqual(result["statistics"]["total_completion_time"], 1 + 4)


class PrecedenceTests(unittest.TestCase):
    def test_precedence_order(self):
        instance = make_instance(
            [
                job("first", 0, 10, 2),
                job("second", 0, 10, 2, predecessors=["first"]),
            ]
        )
        result = scheduler.solve(instance)
        order = {s["id"]: (s["start"], s["end"]) for s in result["scheduled"]}
        self.assertEqual(order["first"], (0, 2))
        self.assertEqual(order["second"], (2, 4))

    def test_precedence_touch_is_legal(self):
        instance = make_instance(
            [
                job("p", 0, 3, 3),
                job("q", 3, 8, 3, predecessors=["p"]),
            ],
            maintenance=[(3, 5)],
        )
        result = scheduler.solve(instance)
        order = {s["id"]: (s["start"], s["end"]) for s in result["scheduled"]}
        self.assertEqual(order["p"], (0, 3))
        self.assertEqual(order["q"], (5, 8))

    def test_cycle_rejected(self):
        instance = make_instance(
            [
                job("a", 0, 10, 1, predecessors=["b"]),
                job("b", 0, 10, 1, predecessors=["a"]),
            ]
        )
        with self.assertRaises(scheduler.InvalidInstance):
            scheduler.solve(instance)


class ReasonTests(unittest.TestCase):
    def test_capacity_conflict_reason(self):
        # Both feasible alone; machine cannot take both -> loser is capacity.
        instance = make_instance(
            [job("a", 0, 2, 2), job("b", 0, 2, 2)]
        )
        result = scheduler.solve(instance)
        self.assertEqual(len(result["scheduled"]), 1)
        loser = result["unscheduled"][0]
        self.assertEqual(loser["reason"], "capacity_conflict")

    def test_predecessor_unscheduled_reason(self):
        instance = make_instance(
            [
                job("parent", 0, 1, 3),  # infeasible standalone
                job("child", 0, 10, 1, predecessors=["parent"]),
            ]
        )
        result = scheduler.solve(instance)
        reasons = {u["id"]: u["reason"] for u in result["unscheduled"]}
        self.assertEqual(reasons["parent"], "no_feasible_slot")
        self.assertEqual(reasons["child"], "predecessor_unscheduled")

    def test_zero_weight_excluded_by_secondary(self):
        # One 1-time slot exists; weight-0 job fits alone but cannot join the
        # weight-5 winner, so the union subset is capacity-infeasible.
        instance = make_instance(
            [
                job("must", 0, 1, 1, weight=5),
                job("extra", 0, 1, 1, weight=0),
            ],
            maintenance=[(1, 10)],
        )
        result = scheduler.solve(instance)
        ids = {s["id"] for s in result["scheduled"]}
        self.assertEqual(ids, {"must"})
        reasons = {u["id"]: u["reason"] for u in result["unscheduled"]}
        self.assertEqual(reasons["extra"], "capacity_conflict")

    def test_zero_weight_dropped_when_addable(self):
        # Both jobs fit on time; the zero-weight job would raise completion sum.
        instance = make_instance(
            [
                job("must", 0, 5, 1, weight=5),
                job("extra", 0, 5, 4, weight=0),
            ]
        )
        result = scheduler.solve(instance)
        ids = {s["id"] for s in result["scheduled"]}
        self.assertEqual(ids, {"must"})
        reasons = {u["id"]: u["reason"] for u in result["unscheduled"]}
        self.assertEqual(reasons["extra"], "excluded_by_secondary_objective")


class ValidationTests(unittest.TestCase):
    def test_invalid_window_and_fields(self):
        bad = make_instance([job("a", 0, 1, 1)], maintenance=[(5, 3)])
        with self.assertRaises(scheduler.InvalidInstance):
            scheduler.solve(bad)
        bad2 = make_instance([job("a", -1, 1, 1)])
        with self.assertRaises(scheduler.InvalidInstance):
            scheduler.solve(bad2)
        bad3 = make_instance([job("a", 5, 1, 1)])
        with self.assertRaises(scheduler.InvalidInstance):
            scheduler.solve(bad3)
        bad4 = make_instance([{"id": "a", "release": 0, "due": 1, "duration": 0}])
        with self.assertRaises(scheduler.InvalidInstance):
            scheduler.solve(bad4)

    def test_unknown_predecessor(self):
        bad = make_instance([job("a", 0, 1, 1, predecessors=["ghost"])])
        with self.assertRaises(scheduler.InvalidInstance):
            scheduler.solve(bad)


class CrossCheckTests(unittest.TestCase):
    def test_exact_matches_independent_bruteforce(self):
        rng = random.Random(20260927)
        checked = 0
        for n in range(1, 8):
            for trial in range(12):
                jobs_spec = []
                for i in range(n):
                    release = rng.randint(0, 4)
                    duration = rng.randint(1, 3)
                    due = release + duration + rng.randint(0, 4)
                    predecessors = []
                    if i > 0 and rng.random() < 0.3:
                        predecessors.append(chr(ord("A") + rng.randint(0, i - 1)))
                    jobs_spec.append(
                        job(
                            chr(ord("A") + i),
                            release,
                            due,
                            duration,
                            weight=rng.choice([1, 1, 2, 3, 0]),
                            predecessors=predecessors,
                        )
                    )
                maintenance = []
                if rng.random() < 0.5:
                    ws = rng.randint(0, 6)
                    length = rng.randint(1, 3)
                    maintenance.append((ws, ws + length))
                instance = make_instance(jobs_spec, maintenance=maintenance)
                result = scheduler.solve(instance, mode="exact")
                self.assertTrue(result["validation"]["valid"])
                expected = brute_force_optimum(instance)
                got = (
                    result["statistics"]["total_weight"],
                    -result["statistics"]["total_completion_time"],
                )
                self.assertEqual(got, expected, msg=json.dumps(instance))
                checked += 1
        self.assertGreaterEqual(checked, 80)

    def test_exact_handles_14_jobs(self):
        rng = random.Random(42)
        jobs_spec = [
            job(f"J{i:02d}", rng.randint(0, 20), 40 + rng.randint(0, 10),
                 rng.randint(1, 3), weight=rng.randint(1, 5))
            for i in range(14)
        ]
        instance = make_instance(jobs_spec, maintenance=[(10, 14), (30, 33)])
        result = scheduler.solve(instance)
        self.assertTrue(result["statistics"]["proven_optimal"])
        self.assertEqual(result["statistics"]["subset_states"], 1 << 14)
        self.assertTrue(result["validation"]["valid"])

    def test_heuristic_flag_and_validity(self):
        rng = random.Random(7)
        jobs_spec = [
            job(f"J{i}", rng.randint(0, 10), 12 + rng.randint(0, 6),
                 rng.randint(1, 3), weight=rng.randint(1, 4))
            for i in range(20)
        ]
        instance = make_instance(jobs_spec, maintenance=[(5, 8)])
        result = scheduler.solve(instance, mode="heuristic")
        self.assertFalse(result["statistics"]["proven_optimal"])
        self.assertEqual(result["status"], "feasible_not_proven_optimal")
        self.assertTrue(result["validation"]["valid"])
        self.assertEqual(
            len(result["scheduled"]) + len(result["unscheduled"]), 20
        )


class CliTests(unittest.TestCase):
    def test_cli_file_input(self):
        instance = make_instance(
            [job("A", 0, 3, 3), job("B", 1, 4, 3)], maintenance=[(3, 5)]
        )
        path = ROOT / "examples" / "acceptance.json"
        path.parent.mkdir(exist_ok=True)
        path.write_text(json.dumps(instance), encoding="utf-8")
        proc = subprocess.run(
            [sys.executable, str(ROOT / "scheduler.py"), str(path)],
            capture_output=True,
            text=True,
            check=True,
        )
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["scheduled"][0]["id"], "A")

    def test_cli_invalid_exit_code(self):
        proc = subprocess.run(
            [sys.executable, str(ROOT / "scheduler.py")],
            input=json.dumps({"jobs": []}),
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 2)
        payload = json.loads(proc.stderr)
        self.assertEqual(payload["status"], "invalid_instance")


if __name__ == "__main__":
    unittest.main(verbosity=2)
