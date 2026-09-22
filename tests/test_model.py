import unittest
from pathlib import Path

from licsel.model import ModelError, load_problem, resolve

SAMPLES = Path(__file__).resolve().parent.parent / "samples"


def base_dict():
    return {
        "rules": {"license_tags": {"MIT": ["permissive"]},
                  "disclosure_tags": [], "propagation": []},
        "targets": [{"name": "t", "conditions": {},
                     "roots": [{"name": "a", "constraint": ">=1.0.0 <2.0.0"}]}],
        "packages": [
            {"name": "a", "version": "1.0.0", "license": "MIT"},
            {"name": "a", "version": "1.5.0", "license": "MIT"},
            {"name": "a", "version": "1.6.0-rc.1", "license": "MIT"},
            {"name": "a", "version": "2.0.0", "license": "MIT"},
        ],
    }


class TestResolution(unittest.TestCase):
    def test_highest_satisfying_is_prerelease_of_higher_core(self):
        problem = load_problem(base_dict())
        # 1.6.0-rc.1 satisfies <2.0.0 and outranks 1.5.0
        self.assertIn("a@1.6.0-rc.1", problem.graph.nodes)
        self.assertNotIn("a@1.5.0", problem.graph.nodes)

    def test_unresolvable_constraint_rejected(self):
        raw = base_dict()
        raw["targets"][0]["roots"] = [{"name": "a", "constraint": ">=9.0.0"}]
        with self.assertRaises(ModelError):
            load_problem(raw)

    def test_unknown_package_rejected(self):
        raw = base_dict()
        raw["targets"][0]["roots"] = [{"name": "nope", "constraint": "*"}]
        with self.assertRaises(ModelError):
            load_problem(raw)

    def test_duplicate_version_rejected(self):
        raw = base_dict()
        raw["packages"].append({"name": "a", "version": "1.0.0", "license": "MIT"})
        with self.assertRaises(ModelError):
            load_problem(raw)

    def test_bad_link_rejected(self):
        raw = base_dict()
        raw["packages"][0]["dependencies"] = [
            {"name": "a", "constraint": "1.0.0", "link": "telepathic"}
        ]
        with self.assertRaises(ModelError):
            load_problem(raw)


if __name__ == "__main__":
    unittest.main()
