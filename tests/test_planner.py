import unittest

from buildplanner.loader import Package
from buildplanner.planner import build_plan
from buildplanner.semver import Version


def pkg(name, deps=(), generates=(), consumes=()):
    return Package(
        name=name,
        version=Version.parse("1.0.0"),
        dependencies=tuple(deps),
        generates=tuple(generates),
        consumes=tuple(consumes),
    )


def make(packages):
    return build_plan({p.name: p for p in packages})


class TestGroupsAndStages(unittest.TestCase):
    def test_intra_stage_cycle_becomes_single_group(self):
        plan = make([pkg("a", deps=["b"]), pkg("b", deps=["a"]), pkg("z")])
        cyclic = [g for g in plan.groups if g.cyclic]
        self.assertEqual(len(cyclic), 1)
        self.assertEqual(set(cyclic[0].members), {"a", "b"})
        self.assertIn("no topological order", cyclic[0].internal_order)
        self.assertTrue(plan.ok)
        self.assertEqual(plan.blocked_groups, set())

    def test_acyclic_groups_have_stages(self):
        plan = make([pkg("base"), pkg("mid", deps=["base"]),
                     pkg("top", deps=["mid"])])
        stage_of = {g.members[0]: g.stage for g in plan.groups}
        self.assertEqual(stage_of, {"base": 0, "mid": 1, "top": 2})
        self.assertTrue(plan.ok)

    def test_artifact_flow_forward_is_allowed(self):
        plan = make([
            pkg("gen", generates=["g.out"]),
            pkg("use", deps=["gen"], consumes=["g.out"]),
        ])
        self.assertTrue(plan.ok)
        self.assertEqual(plan.cycles, [])


class TestCrossStageCycles(unittest.TestCase):
    def blocked_fixture(self):
        return make([
            pkg("schema", generates=["schema.types"]),
            pkg("codegen", deps=["runtime"], generates=["generated.api"]),
            pkg("runtime", deps=["schema"], consumes=["generated.api"]),
            pkg("docs", deps=["schema"]),
        ])

    def test_generator_depending_on_own_output_is_rejected(self):
        plan = self.blocked_fixture()
        self.assertFalse(plan.ok)
        self.assertEqual(len(plan.cycles), 1)
        cycle = plan.cycles[0]
        self.assertEqual(cycle.artifact, "generated.api")
        self.assertEqual(cycle.generator, "codegen")
        self.assertEqual(cycle.consumer, "runtime")
        self.assertGreater(cycle.generator_stage, cycle.consumer_stage)
        # Evidence path proves the cycle: codegen -> runtime -(artifact)-> codegen.
        self.assertEqual(cycle.dependency_path, ("codegen", "runtime"))

    def test_unrelated_groups_remain_executable(self):
        plan = self.blocked_fixture()
        executable = {
            m for gid in plan.executable_groups for m in plan.groups[gid].members
        }
        self.assertEqual(executable, {"schema", "docs"})
        blocked = {
            m for gid in plan.blocked_groups for m in plan.groups[gid].members
        }
        self.assertEqual(blocked, {"codegen", "runtime"})

    def test_downstream_of_blocked_is_blocked(self):
        plan = make([
            pkg("gen", deps=["use"], generates=["g.out"]),
            pkg("use", consumes=["g.out"]),
            pkg("app", deps=["use"]),
        ])
        blocked = {
            m for gid in plan.blocked_groups for m in plan.groups[gid].members
        }
        self.assertEqual(blocked, {"gen", "use", "app"})

    def test_self_consumed_output_is_rejected(self):
        plan = make([pkg("loop", generates=["x"], consumes=["x"])])
        self.assertFalse(plan.ok)
        self.assertEqual(plan.cycles[0].generator, "loop")
        self.assertEqual(plan.cycles[0].consumer, "loop")


if __name__ == "__main__":
    unittest.main()
