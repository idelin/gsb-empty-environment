import unittest
from pathlib import Path

from vsets import (
    BuildInput,
    PackageIndex,
    brute_solve,
    explain,
    solve,
)
from vsets.solver import Unsatisfiable

FIXTURES = Path(__file__).parent / "fixtures"


class JsonPipelineTest(unittest.TestCase):
    def test_basic_build_from_local_json(self):
        index = PackageIndex.load(FIXTURES / "packages.json")
        build = BuildInput.load(FIXTURES / "build_basic.json")
        solution = solve(index, build.roots, build.context, build.locked)
        self.assertEqual(solution.as_dict(), {"a": "2.0.0", "b": "2.0.0"})
        self.assertEqual(
            brute_solve(index, build.roots, build.context).as_dict(),
            solution.as_dict(),
        )

    def test_unsat_explanation_names_paths_and_versions(self):
        index = PackageIndex.load(FIXTURES / "packages.json")
        build = BuildInput.load(FIXTURES / "build_basic.json")
        impossible_lock = {"a": index.versions("a")[0], "b": index.versions("b")[-1]}
        with self.assertRaises(Unsatisfiable) as caught:
            solve(index, build.roots, build.context, impossible_lock)
        explanation = explain(caught.exception)
        self.assertTrue(explanation.constraint_sets)
        self.assertTrue(any("<root>" in path or "<lock>" in path for path in explanation.paths))
        self.assertTrue(explanation.known_versions)


if __name__ == "__main__":
    unittest.main()
