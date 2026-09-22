import copy
import json
import unittest

from rebuildplanner.model import ModelError, Problem
from rebuildplanner.planner import build_plan, plan_to_dict, verify_plan


def pkg_index(extra=None):
    data = {
        "packages": {
            "util": {"versions": ["1.0.0"]},
            "core": {"versions": ["2.0.0"]},
            "web": {"versions": ["1.4.0"]},
            "api": {"versions": ["1.2.0"]},
            "app": {"versions": ["3.0.0"]},
            "cli": {"versions": ["2.0.0"]},
        }
    }
    if extra:
        data["packages"].update(extra)
    return data


def node(nid, pkg, version, cost, deps=None, digest=None):
    return {
        "id": nid,
        "package": pkg,
        "version": version,
        "digest": digest or f"sha256:{nid}",
        "rebuild_cost": cost,
        "deps": deps or [],
    }


DEP = {"node": "", "constraint": ""}


def dep(nid, constraint=""):
    return {"node": nid, "constraint": constraint}


def make_problem(nodes, failed=None, cache=None, targets=None):
    build = {"nodes": nodes, "failed": failed or [], "cache": cache or [],
             "targets": targets or []}
    return Problem.from_raw(pkg_index(), build)


def action_map(plan):
    return {a.node_id: (a.action, a.cost) for a in plan.actions}


class SharedCostTest(unittest.TestCase):
    """两个目标共享的高成本中间产物只计一次。"""

    def setUp(self):
        self.nodes = [
            node("util-1.0.0", "util", "1.0.0", 10),
            node("core-2.0.0", "core", "2.0.0", 100, [dep("util-1.0.0", "^1.0.0")]),
            node("web-1.4.0", "web", "1.4.0", 60, [dep("core-2.0.0", "~2.0.0")]),
            node("api-1.2.0", "api", "1.2.0", 50, [dep("core-2.0.0", ">=2.0.0")]),
        ]

    def test_shared_intermediate_counted_once(self):
        problem = make_problem(
            self.nodes,
            failed=["core-2.0.0"],
            targets=["web-1.4.0", "api-1.2.0"],
        )
        plan = build_plan(problem)
        # core 100（一次） + web 60 + api 50 联动重建
        self.assertEqual(plan.total_cost, 210)
        am = action_map(plan)
        self.assertEqual(am["core-2.0.0"], ("rebuild", 100))
        self.assertEqual(am["web-1.4.0"], ("rebuild", 60))
        self.assertEqual(am["api-1.2.0"], ("rebuild", 50))
        self.assertEqual(am["util-1.0.0"], ("reuse", 0))

    def test_target_closure_excludes_unneeded(self):
        problem = make_problem(
            self.nodes, failed=["core-2.0.0"], targets=["web-1.4.0"]
        )
        plan = build_plan(problem)
        self.assertEqual(
            set(a.node_id for a in plan.actions), {"util-1.0.0", "core-2.0.0", "web-1.4.0"}
        )


class CacheTest(unittest.TestCase):
    def setUp(self):
        self.nodes = [
            node("util-1.0.0", "util", "1.0.0", 10),
            node("core-2.0.0", "core", "2.0.0", 100, [dep("util-1.0.0", "^1.0.0")]),
        ]

    def test_digest_match_restore(self):
        problem = make_problem(
            self.nodes,
            failed=["core-2.0.0"],
            cache=[{"node": "core-2.0.0", "digest": "sha256:core-2.0.0", "restore_cost": 20}],
            targets=["core-2.0.0"],
        )
        plan = build_plan(problem)
        self.assertEqual(action_map(plan)["core-2.0.0"], ("restore", 20))
        self.assertEqual(plan.total_cost, 20)

    def test_digest_mismatch_unrecoverable(self):
        problem = make_problem(
            self.nodes,
            failed=["core-2.0.0"],
            cache=[{"node": "core-2.0.0", "digest": "sha256:STALE", "restore_cost": 1}],
            targets=["core-2.0.0"],
        )
        plan = build_plan(problem)
        self.assertEqual(action_map(plan)["core-2.0.0"], ("rebuild", 100))

    def test_restore_cheaper_than_rebuild_chain(self):
        # 失效节点缓存可恢复且成本低：选择恢复，下游不受影响
        nodes = self.nodes + [
            node("web-1.4.0", "web", "1.4.0", 60, [dep("core-2.0.0", "^2.0.0")]),
        ]
        problem = make_problem(
            nodes,
            failed=["core-2.0.0"],
            cache=[{"node": "core-2.0.0", "digest": "sha256:core-2.0.0", "restore_cost": 5}],
            targets=["web-1.4.0"],
        )
        plan = build_plan(problem)
        am = action_map(plan)
        self.assertEqual(am["core-2.0.0"], ("restore", 5))
        self.assertEqual(am["web-1.4.0"], ("reuse", 0))

    def test_rebuild_cheaper_when_restore_expensive(self):
        # 恢复报价高于重建+连锁：求解器应选择重建
        nodes = self.nodes + [
            node("web-1.4.0", "web", "1.4.0", 60, [dep("core-2.0.0", "^2.0.0")]),
        ]
        problem = make_problem(
            nodes,
            failed=["core-2.0.0"],
            cache=[{"node": "core-2.0.0", "digest": "sha256:core-2.0.0", "restore_cost": 500}],
            targets=["web-1.4.0"],
        )
        plan = build_plan(problem)
        am = action_map(plan)
        self.assertEqual(am["core-2.0.0"], ("rebuild", 100))
        self.assertEqual(am["web-1.4.0"], ("rebuild", 60))
        self.assertEqual(plan.total_cost, 160)

    def test_downstream_failure_with_match_still_forced_rebuild(self):
        # 下游自身失效且缓存匹配、本可恢复；但上游必须重建 -> 下游被迫重建
        nodes = self.nodes + [
            node("web-1.4.0", "web", "1.4.0", 60, [dep("core-2.0.0", "^2.0.0")]),
        ]
        problem = make_problem(
            nodes,
            failed=["core-2.0.0", "web-1.4.0"],
            cache=[{"node": "web-1.4.0", "digest": "sha256:web-1.4.0", "restore_cost": 5}],
            targets=["web-1.4.0"],
        )
        plan = build_plan(problem)
        am = action_map(plan)
        self.assertEqual(am["core-2.0.0"], ("rebuild", 100))
        self.assertEqual(am["web-1.4.0"], ("rebuild", 60))


class VerifyTest(unittest.TestCase):
    def setUp(self):
        self.nodes = [
            node("util-1.0.0", "util", "1.0.0", 10),
            node("core-2.0.0", "core", "2.0.0", 100, [dep("util-1.0.0", "^1.0.0")]),
            node("web-1.4.0", "web", "1.4.0", 60, [dep("core-2.0.0", "^2.0.0")]),
        ]
        self.problem = make_problem(
            self.nodes,
            failed=["core-2.0.0"],
            targets=["web-1.4.0"],
        )

    def test_valid_plan_passes(self):
        payload = plan_to_dict(build_plan(self.problem), self.problem)
        self.assertEqual(verify_plan(self.problem, payload), [])

    def test_restore_after_dependency_rebuild_rejected(self):
        payload = plan_to_dict(build_plan(self.problem), self.problem)
        for item in payload["actions"]:
            if item["node"] == "web-1.4.0":
                item["action"] = "restore"
                item["cost"] = 1
                item["digest"] = "sha256:web-1.4.0"
        payload["total_cost"] = sum(i["cost"] for i in payload["actions"])
        errors = verify_plan(self.problem, payload)
        self.assertTrue(any("联动重建" in e for e in errors), errors)

    def test_stale_cache_restore_rejected(self):
        payload = plan_to_dict(build_plan(self.problem), self.problem)
        for item in payload["actions"]:
            if item["node"] == "core-2.0.0":
                item["action"] = "restore"
                item["cost"] = 1
        payload["total_cost"] = sum(i["cost"] for i in payload["actions"])
        errors = verify_plan(self.problem, payload)
        self.assertTrue(any("不可恢复" in e for e in errors), errors)

    def test_reuse_failed_node_rejected(self):
        payload = plan_to_dict(build_plan(self.problem), self.problem)
        for item in payload["actions"]:
            if item["node"] == "core-2.0.0":
                item["action"] = "reuse"
                item["cost"] = 0
        payload["total_cost"] = sum(i["cost"] for i in payload["actions"])
        errors = verify_plan(self.problem, payload)
        self.assertTrue(any("已失效" in e for e in errors), errors)

    def test_missing_dependency_rejected(self):
        payload = plan_to_dict(build_plan(self.problem), self.problem)
        payload["actions"] = [a for a in payload["actions"] if a["node"] != "util-1.0.0"]
        errors = verify_plan(self.problem, payload)
        self.assertTrue(any("缺少闭包内节点" in e for e in errors), errors)

    def test_nonoptimal_cost_rejected(self):
        problem = make_problem(
            self.nodes,
            failed=["core-2.0.0"],
            cache=[{"node": "core-2.0.0", "digest": "sha256:core-2.0.0", "restore_cost": 5}],
            targets=["web-1.4.0"],
        )
        # 错误地把可低成本恢复的节点重建
        payload = plan_to_dict(build_plan(problem), problem)
        for item in payload["actions"]:
            if item["node"] == "core-2.0.0":
                item["action"] = "rebuild"
                item["cost"] = 100
            if item["node"] == "web-1.4.0":
                item["action"] = "rebuild"
                item["cost"] = 60
        payload["total_cost"] = 160
        errors = verify_plan(problem, payload)
        self.assertTrue(any("不是最小" in e for e in errors), errors)


class ModelValidationTest(unittest.TestCase):
    def _build(self, packages=None, **overrides):
        nodes = [node("util-1.0.0", "util", "1.0.0", 10)]
        raw = {"nodes": nodes, "failed": [], "cache": [], "targets": ["util-1.0.0"]}
        raw.update(overrides)
        return Problem.from_raw(packages or pkg_index(), raw)

    def test_unknown_package_rejected(self):
        with self.assertRaises(ModelError):
            self._build(nodes=[node("x-1.0.0", "ghost", "1.0.0", 1)])

    def test_local_version_must_exist(self):
        with self.assertRaises(ModelError):
            self._build(nodes=[node("util-9.9.9", "util", "9.9.9", 1)])

    def test_constraint_violation_rejected(self):
        nodes = [
            node("util-1.0.0", "util", "1.0.0", 10),
            node("core-2.0.0", "core", "2.0.0", 100, [dep("util-1.0.0", "^2.0.0")]),
        ]
        with self.assertRaises(ModelError):
            self._build(nodes=nodes, targets=["core-2.0.0"])

    def test_forward_dependency_rejected(self):
        nodes = [
            node("core-2.0.0", "core", "2.0.0", 100, [dep("util-1.0.0", "^1.0.0")]),
            node("util-1.0.0", "util", "1.0.0", 10),
        ]
        with self.assertRaises(ModelError):
            self._build(nodes=nodes, targets=["core-2.0.0"])

    def test_cycle_rejected(self):
        from rebuildplanner.model import Dependency, Node, _assert_dag
        from rebuildplanner.version import Version

        a = Node(id="a", package="util", version=Version.parse("1.0.0"),
                 digest="d1", rebuild_cost=1, deps=())
        b = Node(id="b", package="core", version=Version.parse("2.0.0"),
                 digest="d2", rebuild_cost=1, deps=(Dependency("a"),))
        a.deps = (Dependency("b"),)
        with self.assertRaises(ModelError):
            _assert_dag({"a": a, "b": b})


class PrereleasePackageTest(unittest.TestCase):
    def test_prerelease_version_in_plan(self):
        packages = pkg_index({"lib": {"versions": ["1.0.0-beta.1", "1.0.0"]}})
        raw = {
            "nodes": [
                {
                    "id": "lib-1.0.0-beta.1", "package": "lib", "version": "1.0.0-beta.1",
                    "digest": "sha256:lib-beta", "rebuild_cost": 5, "deps": [],
                }
            ],
            "failed": [],
            "cache": [],
            "targets": ["lib-1.0.0-beta.1"],
        }
        problem = Problem.from_raw(packages, raw)
        plan = build_plan(problem)
        self.assertEqual(plan.total_cost, 0)
        self.assertTrue(str(problem.nodes["lib-1.0.0-beta.1"].version).endswith("-beta.1"))


if __name__ == "__main__":
    unittest.main()
