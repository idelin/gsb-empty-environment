import unittest
from pathlib import Path

from licsel.core import conflict_core
from licsel.model import load_problem

SAMPLES = Path(__file__).resolve().parent.parent / "samples"


class TestConflictCore(unittest.TestCase):
    def test_core_isolates_minimal_conflict(self):
        problem = load_problem(str(SAMPLES / "conflict_core.json"))
        core = conflict_core(problem)
        self.assertTrue(core.minimal)
        self.assertEqual(len(core.instances), 1)
        inst = core.instances[0]
        self.assertEqual(inst.rule_id, "static-strong-copyleft-dependents")
        self.assertEqual(inst.trigger, "libP@1.0.0")
        self.assertEqual(set(inst.scope_nodes), {"app@1.0.0"})
        # the decoy package libQ must not appear in the core
        self.assertNotIn("libQ@1.0.0", core.involved_packages)

    def test_core_empty_when_feasible(self):
        problem = load_problem(str(SAMPLES / "diamond_shared.json"))
        core = conflict_core(problem)
        self.assertEqual(core.instances, ())

    def test_core_json_shape(self):
        problem = load_problem(str(SAMPLES / "conflict_core.json"))
        payload = conflict_core(problem).as_json()
        self.assertIn("instances", payload)
        self.assertIn("involved_packages", payload)
        self.assertTrue(payload["minimal"])


if __name__ == "__main__":
    unittest.main()
