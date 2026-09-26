import unittest

from solution_enumerator import (
    EnumerationResult,
    ProjectedSolution,
    VerificationError,
    independent_check,
    enumerate_solutions,
    ProjectingEnumerator,
)


def example_domains():
    return {"x": tuple(range(3)), "a": tuple(range(4))}


def example_constraints():
    return (lambda env: env["x"] + env["a"] <= 3,)


def projected_values(result):
    return [solution.values["x"] for solution in result.solutions]


class SolutionEnumeratorTests(unittest.TestCase):
    def test_enumerates_all_complete_solutions(self):
        result = enumerate_solutions(example_domains(), example_constraints())

        self.assertTrue(result.complete)
        self.assertEqual(result.status, "complete")
        self.assertEqual(len(result.solutions), 9)
        self.assertEqual(
            {(s.values["x"], s.values["a"]) for s in result.solutions},
            {(x, a) for x in range(3) for a in range(4) if x + a <= 3},
        )

    def test_projection_deduplicates_auxiliary_assignments(self):
        enumerator = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=("x",)
        )
        result = enumerator.run()

        self.assertTrue(result.complete)
        self.assertEqual(projected_values(result), [0, 1, 2])
        self.assertEqual(len(enumerator.blocking_clauses), 3)
        self.assertTrue(
            all(example_constraints()[0](solution.witness) for solution in result.solutions)
        )

    def test_duplicate_projection_does_not_add_full_solution_clause(self):
        enumerator = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=("x",)
        )

        first = enumerator.run(max_solutions=1)
        self.assertEqual(projected_values(first), [0])
        self.assertEqual(len(enumerator.blocking_clauses), 1)

        resumed = enumerator.run()
        self.assertEqual(projected_values(resumed), [1, 2])
        self.assertEqual(len(enumerator.blocking_clauses), 3)
        self.assertTrue(enumerator.complete)

    def test_empty_projection_emits_at_most_one_solution(self):
        enumerator = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=()
        )
        result = enumerator.run(max_solutions=5)

        self.assertEqual(len(result.solutions), 1)
        self.assertEqual(result.solutions[0].values, {})
        self.assertTrue(result.complete)
        self.assertTrue(enumerator.complete)
        self.assertEqual(enumerator.search_stack, ())

    def test_max_solution_pause_resume_has_no_duplicates(self):
        enumerator = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=("x",)
        )

        first = enumerator.run(max_solutions=2)
        second = enumerator.run(max_solutions=2)

        self.assertFalse(first.complete)
        self.assertEqual(first.status, "solution_limit")
        self.assertEqual(projected_values(first), [0, 1])
        self.assertTrue(second.complete)
        self.assertEqual(projected_values(second), [2])
        self.assertEqual(
            sorted(enumerator.emitted_projection_keys),
            [(0,), (1,), (2,)],
        )

    def test_node_budget_preserves_stack_and_resumes(self):
        budgeted = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=("x",)
        )
        first = budgeted.run(node_budget=2)

        self.assertFalse(first.complete)
        self.assertEqual(first.status, "node_budget")
        self.assertEqual(projected_values(first), [0])
        self.assertEqual(first.nodes_used, 2)
        self.assertNotEqual(budgeted.search_stack, ())

        rest = budgeted.run()
        self.assertTrue(rest.complete)
        self.assertEqual(projected_values(rest), [1, 2])

    def test_budget_resume_matches_uninterrupted_enumeration(self):
        budgeted = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=("x",)
        )
        collected = []
        while True:
            result = budgeted.run(node_budget=3)
            collected.extend(projected_values(result))
            if result.complete:
                break

        complete = enumerate_solutions(
            example_domains(), example_constraints(), projection=("x",)
        )
        self.assertEqual(collected, projected_values(complete))

    def test_blocking_clause_excludes_only_emitted_full_assignment(self):
        enumerator = ProjectingEnumerator(
            example_domains(), example_constraints(), projection=("x",)
        )
        enumerator.run(max_solutions=1)

        clause = enumerator.blocking_clauses[0]
        self.assertEqual(clause, (("x", "!=", 0), ("a", "!=", 0)))
        self.assertFalse(enumerator._is_blocked({"x": 0, "a": 1}))
        self.assertFalse(enumerator._is_blocked({"x": 1, "a": 0}))
        self.assertTrue(enumerator._is_blocked({"x": 0, "a": 0}))

    def test_independent_checker_validates_original_constraints(self):
        checked = []

        def checker(assignment, constraints):
            checked.append(dict(assignment))
            independent_check(assignment, constraints)

        enumerate_solutions(
            example_domains(),
            example_constraints(),
            projection=("x",),
            checker=checker,
        )

        self.assertEqual(len(checked), 9)
        self.assertTrue(all(env["x"] + env["a"] <= 3 for env in checked))

    def test_failed_independent_check_is_not_claimed(self):
        def rejecting_checker(assignment, constraints):
            raise VerificationError("rejected")

        enumerator = ProjectingEnumerator(
            example_domains(),
            example_constraints(),
            projection=("x",),
            checker=rejecting_checker,
        )

        with self.assertRaises(VerificationError):
            enumerator.run()
        self.assertEqual(enumerator.emitted_projection_keys, frozenset())
        self.assertEqual(enumerator.blocking_clauses, ())

    def test_custom_checker_does_not_replace_original_check(self):
        enumerator = ProjectingEnumerator(
            example_domains(),
            (lambda env: False,),
            projection=("x",),
            checker=lambda assignment, constraints: None,
        )

        result = enumerator.run()
        self.assertEqual(result.solutions, ())

    def test_no_solutions(self):
        result = enumerate_solutions(
            {"x": (0, 1)}, (lambda env: env["x"] > 5,), projection=("x",)
        )

        self.assertTrue(result.complete)
        self.assertEqual(result.solutions, ())

    def test_result_and_solution_value_types(self):
        result = enumerate_solutions(
            {"x": (0,), "a": (0,)},
            example_constraints(),
            projection=("x",),
        )

        self.assertIsInstance(result, EnumerationResult)
        self.assertIsInstance(result.solutions[0], ProjectedSolution)
        self.assertIsInstance(result.solutions[0].values, dict)
        self.assertEqual(result.solutions[0].witness, {"x": 0, "a": 0})

    def test_invalid_configuration(self):
        with self.assertRaises(ValueError):
            ProjectingEnumerator({"x": ()}, example_constraints())
        with self.assertRaises(ValueError):
            ProjectingEnumerator(
                example_domains(), example_constraints(), projection=("z",)
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
