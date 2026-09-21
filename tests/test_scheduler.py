import json
import tempfile
import unittest
from pathlib import Path

from scheduler.errors import InfeasibleError, InputError
from scheduler.heuristic import build_schedule
from scheduler.loader import load_build_input, parse_build_input
from scheduler.lowerbound import critical_path_length, lower_bound
from scheduler.semver import is_valid_version, parse_version

EXAMPLE = Path(__file__).resolve().parent.parent / "examples" / "sample_build.json"


def make_build(tasks, cpu=4, memory=8):
    return parse_build_input(
        {"name": "t", "capacity": {"cpu": cpu, "memory": memory}, "tasks": tasks}
    )


class SemverTest(unittest.TestCase):
    def test_valid_versions(self):
        for v in ["1.2.3", "0.0.1", "3.1.0-rc1", "1.0.0-alpha.1", "2.0.0+build.5"]:
            self.assertTrue(is_valid_version(v), v)

    def test_invalid_versions(self):
        for v in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "1.2.x", "", None, 123]:
            self.assertFalse(is_valid_version(v), v)

    def test_parse_and_order(self):
        release = parse_version("3.1.0")
        rc = parse_version("3.1.0-rc1")
        self.assertEqual(str(rc), "3.1.0-rc1")
        self.assertLess(rc.sort_key(), release.sort_key())
        self.assertLess(
            parse_version("1.0.0-alpha").sort_key(),
            parse_version("1.0.0-alpha.1").sort_key(),
        )


class LoaderTest(unittest.TestCase):
    def test_load_example(self):
        build = load_build_input(EXAMPLE)
        self.assertEqual(build.name, "demo-build")
        self.assertEqual(len(build.tasks), 9)
        self.assertEqual(build.packages[1].version, "3.1.0-rc1")

    def test_bad_version_rejected(self):
        with self.assertRaises(InputError):
            parse_build_input(
                {
                    "capacity": {"cpu": 1, "memory": 1},
                    "packages": [{"name": "a", "version": "1.2"}],
                    "tasks": [{"id": "t", "cpu": 1, "memory": 1, "duration": 1}],
                }
            )

    def test_duplicate_task_id(self):
        with self.assertRaises(InputError):
            make_build(
                [
                    {"id": "a", "cpu": 1, "memory": 1, "duration": 1},
                    {"id": "a", "cpu": 1, "memory": 1, "duration": 1},
                ]
            )

    def test_unknown_dependency(self):
        with self.assertRaises(InputError):
            make_build(
                [{"id": "a", "cpu": 1, "memory": 1, "duration": 1, "deps": ["ghost"]}]
            )

    def test_cycle_detected(self):
        with self.assertRaises(InputError):
            make_build(
                [
                    {"id": "a", "cpu": 1, "memory": 1, "duration": 1, "deps": ["b"]},
                    {"id": "b", "cpu": 1, "memory": 1, "duration": 1, "deps": ["a"]},
                ]
            )

    def test_load_from_json_file(self):
        payload = {
            "capacity": {"cpu": 2, "memory": 2},
            "tasks": [{"id": "a", "cpu": 1, "memory": 1, "duration": 1}],
        }
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "in.json"
            path.write_text(json.dumps(payload), encoding="utf-8")
            build = load_build_input(path)
        self.assertEqual(build.capacity.cpu, 2)


class HeuristicTest(unittest.TestCase):
    def test_single_task_over_capacity_infeasible(self):
        build = make_build([{"id": "big", "cpu": 1, "memory": 99, "duration": 1}])
        with self.assertRaises(InfeasibleError):
            build_schedule(build)

    def test_memory_limit_prevents_coscheduling(self):
        # 两个任务各自可行，但内存之和 12 > 10，不能同排。
        build = make_build(
            [
                {"id": "a", "cpu": 1, "memory": 6, "duration": 3},
                {"id": "b", "cpu": 1, "memory": 6, "duration": 2},
            ],
            memory=10,
        )
        result = build_schedule(build)
        self.assertEqual(result.makespan, 5)
        spans = {e.task_id: (e.start, e.end) for e in result.entries}
        self.assertEqual(spans["a"], (0, 3))
        self.assertEqual(spans["b"], (3, 5))

    def test_dependencies_respected(self):
        build = load_build_input(EXAMPLE)
        result = build_schedule(build)
        start = {e.task_id: e.start for e in result.entries}
        end = {e.task_id: e.end for e in result.entries}
        for t in build.tasks:
            for dep in t.deps:
                self.assertLessEqual(end[dep], start[t.id], f"{dep} -> {t.id}")

    def test_capacity_never_exceeded(self):
        build = load_build_input(EXAMPLE)
        result = build_schedule(build)
        tasks = {t.id: t for t in build.tasks}
        moments = {e.start for e in result.entries} | {e.end for e in result.entries}
        for moment in moments:
            active = [e for e in result.entries if e.start <= moment < e.end]
            self.assertLessEqual(
                sum(tasks[e.task_id].cpu for e in active), build.capacity.cpu
            )
            self.assertLessEqual(
                sum(tasks[e.task_id].memory for e in active), build.capacity.memory
            )

    def test_deterministic(self):
        build = load_build_input(EXAMPLE)
        self.assertEqual(build_schedule(build), build_schedule(build))

    def test_critical_path_priority(self):
        # 容量只允许单任务运行：t=2 时就绪的是 b 和 c，
        # 关键路径位次更高的 c（rank 5）应优先于 b（rank 1）。
        build = make_build(
            [
                {"id": "a", "cpu": 2, "memory": 1, "duration": 2},
                {"id": "b", "cpu": 2, "memory": 1, "duration": 1},
                {"id": "c", "cpu": 2, "memory": 1, "duration": 5, "deps": ["a"]},
            ],
            cpu=2,
        )
        result = build_schedule(build)
        start = {e.task_id: e.start for e in result.entries}
        self.assertEqual(start["a"], 0)
        self.assertEqual(start["c"], 2)
        self.assertEqual(start["b"], 7)
        self.assertEqual(result.makespan, 8)


class LowerBoundTest(unittest.TestCase):
    def test_critical_path(self):
        build = make_build(
            [
                {"id": "a", "cpu": 1, "memory": 1, "duration": 3},
                {"id": "b", "cpu": 1, "memory": 1, "duration": 4, "deps": ["a"]},
                {"id": "c", "cpu": 1, "memory": 1, "duration": 2},
            ]
        )
        self.assertEqual(critical_path_length(build), 7)

    def test_resource_bound(self):
        # cpu 总工作量 2*5+2*5=20，容量 4 -> ceil(20/4)=5；关键路径也是 5。
        build = make_build(
            [
                {"id": "a", "cpu": 2, "memory": 1, "duration": 5},
                {"id": "b", "cpu": 2, "memory": 1, "duration": 5},
            ]
        )
        self.assertEqual(lower_bound(build), 5)

    def test_lower_bound_never_exceeds_makespan(self):
        build = load_build_input(EXAMPLE)
        result = build_schedule(build)
        self.assertLessEqual(lower_bound(build), result.makespan)


if __name__ == "__main__":
    unittest.main()
