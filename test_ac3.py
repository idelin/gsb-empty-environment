import random
import unittest

from ac3 import CSP, Conflict, solve_all, solve_one
from reference import check_assignment, enumerate_solutions, reference_ac


TEMPLATES = [
    ("lt", lambda a, b: a < b),
    ("le", lambda a, b: a <= b),
    ("ne", lambda a, b: a != b),
    ("eq", lambda a, b: a == b),
    ("far", lambda a, b: abs(a - b) >= 2),
    ("sum4", lambda a, b: a + b <= 4),
]


def make_ordering_csp():
    csp = CSP()
    csp.add_variable("x", {1, 2})
    csp.add_variable("y", {1, 2})
    csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b, name="x<y")
    return csp


class RefSession:
    """Independent mutable model: forced exclusions + from-scratch AC."""

    def __init__(self, universe):
        self.universe = {v: frozenset(d) for v, d in universe.items()}
        self.cons = {}
        self.forced = {v: set() for v in universe}
        self.stack = []

    def push(self):
        self.stack.append(
            (dict(self.cons), {v: set(s) for v, s in self.forced.items()})
        )

    def pop(self):
        self.cons, self.forced = self.stack.pop()

    def add_constraint(self, cid, a, b, pred):
        self.cons[cid] = (a, b, pred)

    def remove_constraint(self, cid):
        del self.cons[cid]

    def delete_value(self, var, value):
        self.forced[var].add(value)

    def assign(self, var, value):
        self.forced[var] |= set(self.universe[var]) - {value}

    def domains(self):
        uni = {v: self.universe[v] - self.forced[v] for v in self.universe}
        return reference_ac(uni, list(self.cons.values()))

    def solutions(self):
        uni = {v: self.universe[v] - self.forced[v] for v in self.universe}
        return enumerate_solutions(uni, list(self.cons.values()))


def validate_reasons(testcase, csp):
    for var, entries in csp.deleted.items():
        for value, reason in entries.items():
            testcase.assertNotIn(value, csp.domains[var])
            testcase.assertIn(value, csp.universe[var])
            if reason.kind == "decision":
                continue
            testcase.assertIn(
                reason.constraint_id,
                csp.constraints,
                f"stale reason for {var}={value}",
            )
            con = csp.constraints[reason.constraint_id]
            testcase.assertEqual(con.other(var), reason.neighbor)
            testcase.assertFalse(
                any(con.holds(var, value, w) for w in csp.domains[reason.neighbor]),
                f"reason for {var}={value} does not hold",
            )
            testcase.assertLessEqual(
                set(reason.neighbor_domain), set(csp.universe[reason.neighbor])
            )


class TestRequiredExamples(unittest.TestCase):
    def test_xy_contraction(self):
        csp = make_ordering_csp()
        self.assertEqual(csp.current_domains(), {"x": [1], "y": [2]})
        deleted = csp.deleted_with_reasons()
        self.assertEqual(str(deleted["x"][2]), "no support in y (domain [1, 2]) for constraint c_xy")
        self.assertEqual(deleted["y"][1].constraint_id, "c_xy")
        validate_reasons(self, csp)

    def test_empty_domain_immediate_conflict(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1})
        with self.assertRaises(Conflict):
            csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b)

    def test_push_pop_restores_domains(self):
        csp = CSP()
        csp.add_variable("x", {1, 2, 3})
        csp.add_variable("y", {1, 2, 3})
        csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b)
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [2, 3]})
        csp.push()
        csp.assign("x", 2)
        self.assertEqual(csp.current_domains(), {"x": [2], "y": [3]})
        csp.pop()
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [2, 3]})
        self.assertEqual(csp.deleted_with_reasons(), {"x": {3: csp.deleted["x"][3]}, "y": {1: csp.deleted["y"][1]}})

    def test_pop_restores_queue_cache_and_reasons(self):
        csp = make_ordering_csp()
        csp.push()
        cache_snapshot = dict(csp._supports)
        csp.assign("x", 1)
        self.assertEqual(len(csp._queue), 0)
        csp.pop()
        self.assertEqual(dict(csp._supports), cache_snapshot)
        self.assertEqual(len(csp._queue), 0)
        self.assertEqual(len(csp._in_queue), 0)
        self.assertEqual(
            {v: dict(e) for v, e in csp.deleted.items()},
            {"x": {2: csp.deleted["x"][2]}, "y": {1: csp.deleted["y"][1]}},
        )
        validate_reasons(self, csp)

    def test_remove_constraint_restores_values(self):
        csp = make_ordering_csp()
        csp.remove_constraint("c_xy")
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [1, 2]})
        self.assertEqual(csp.deleted_with_reasons(), {})

    def test_remove_constraint_partial_restore_and_reason_refresh(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_variable("z", {2})
        csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b)
        csp.add_constraint("c_xz", "x", "z", lambda a, b: a < b)
        self.assertEqual(csp.current_domains(), {"x": [1], "y": [2], "z": [2]})
        csp.remove_constraint("c_xy")
        self.assertEqual(csp.current_domains(), {"x": [1], "y": [1, 2], "z": [2]})
        reason = csp.deleted["x"][2]
        self.assertEqual(reason.constraint_id, "c_xz")
        validate_reasons(self, csp)
        csp.remove_constraint("c_xz")
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [1, 2], "z": [2]})

    def test_decision_survives_constraint_removal(self):
        csp = CSP()
        csp.add_variable("x", {1, 2, 3})
        csp.add_variable("y", {1, 2, 3})
        csp.delete_value("x", 1)
        csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b)
        self.assertEqual(csp.current_domains(), {"x": [2], "y": [3]})
        csp.remove_constraint("c_xy")
        self.assertEqual(csp.current_domains(), {"x": [2, 3], "y": [1, 2, 3]})
        csp2 = CSP()
        csp2.add_variable("x", {1, 2, 3})
        csp2.add_variable("y", {1, 2, 3})
        csp2.push()
        csp2.delete_value("x", 1)
        csp2.add_constraint("c_xy", "x", "y", lambda a, b: a < b)
        csp2.pop()
        self.assertEqual(csp2.current_domains(), {"x": [1, 2, 3], "y": [1, 2, 3]})

    def test_mutually_supporting_values_restore_together(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.add_variable("z", {2})
        csp.add_constraint("c_xy", "x", "y", lambda a, b: a == b)
        csp.add_constraint("c_xz", "x", "z", lambda a, b: a < b)
        self.assertEqual(csp.current_domains(), {"x": [1], "y": [1], "z": [2]})
        csp.remove_constraint("c_xz")
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [1, 2], "z": [2]})
        self.assertEqual(csp.deleted_with_reasons(), {})

    def test_pop_undoes_remove_constraint(self):
        csp = make_ordering_csp()
        csp.push()
        csp.remove_constraint("c_xy")
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [1, 2]})
        csp.pop()
        self.assertIn("c_xy", csp.constraints)
        self.assertEqual(csp.current_domains(), {"x": [1], "y": [2]})
        validate_reasons(self, csp)

    def test_pop_undoes_add_constraint(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {1, 2})
        csp.push()
        csp.add_constraint("c_xy", "x", "y", lambda a, b: a < b)
        self.assertEqual(csp.current_domains(), {"x": [1], "y": [2]})
        csp.pop()
        self.assertEqual(csp.constraints, {})
        self.assertEqual(csp.current_domains(), {"x": [1, 2], "y": [1, 2]})

    def test_queue_dedup_keeps_rechecks(self):
        csp = CSP()
        csp.add_variable("x", {1, 2, 3})
        csp.add_variable("y", {1, 2, 3})
        csp.add_variable("z", {1, 2, 3})
        csp._enqueue("x", "c1")
        csp._enqueue("x", "c1")
        csp._enqueue("y", "c1")
        self.assertEqual(list(csp._queue), [("x", "c1"), ("y", "c1")])
        csp.add_constraint("c1", "x", "y", lambda a, b: a < b)
        csp.add_constraint("c2", "y", "z", lambda a, b: a < b)
        self.assertEqual(
            csp.current_domains(), {"x": [1], "y": [2], "z": [3]}
        )
        validate_reasons(self, csp)


class TestSolverAndIndependentCheck(unittest.TestCase):
    def build_queens(self, n):
        csp = CSP()
        for i in range(n):
            csp.add_variable(f"r{i}", set(range(n)))
        for i in range(n):
            for j in range(i + 1, n):
                gap = j - i
                csp.add_constraint(
                    f"q{i}_{j}",
                    f"r{i}",
                    f"r{j}",
                    lambda a, b, gap=gap: a != b and abs(a - b) != gap,
                )
        return csp

    def test_queens_solution_verified_independently(self):
        csp = self.build_queens(4)
        solution = solve_one(csp)
        self.assertIsNotNone(solution)
        cons = [(c.a, c.b, c.pred) for c in csp.constraints.values()]
        self.assertTrue(check_assignment(csp.universe, cons, solution))

    def test_queens_all_solutions_match_enumeration(self):
        csp = self.build_queens(4)
        cons = [(c.a, c.b, c.pred) for c in csp.constraints.values()]
        found = solve_all(csp)
        expected = enumerate_solutions(csp.universe, cons)
        self.assertEqual(len(found), 2)
        self.assertEqual(
            {tuple(sorted(s.items())) for s in found},
            {tuple(sorted(s.items())) for s in expected},
        )
        for solution in found:
            self.assertTrue(check_assignment(csp.universe, cons, solution))

    def test_unsatisfiable_problem(self):
        n = 3
        universe = {f"r{i}": set(range(n)) for i in range(n)}
        cons = []
        for i in range(n):
            for j in range(i + 1, n):
                gap = j - i
                cons.append(
                    (f"r{i}", f"r{j}",
                     lambda a, b, gap=gap: a != b and abs(a - b) != gap)
                )
        self.assertEqual(enumerate_solutions(universe, cons), [])
        try:
            csp = self.build_queens(3)
        except Conflict:
            return
        self.assertIsNone(solve_one(csp))


class TestRandomSessions(unittest.TestCase):
    def run_session(self, seed, steps=250):
        rng = random.Random(seed)
        csp = CSP()
        universe = {}
        for i in range(4):
            size = rng.randint(2, 4)
            universe[f"v{i}"] = set(range(size))
            csp.add_variable(f"v{i}", universe[f"v{i}"])
        ref = RefSession(universe)
        csp.push()
        ref.push()
        depth = 1
        cid_counter = 0
        variables = sorted(universe)

        for step in range(steps):
            op = rng.choices(
                ["add", "remove", "delete", "assign", "push", "pop"],
                weights=[30, 15, 20, 10, 15, 10],
            )[0]
            conflict = None
            if op == "add":
                a, b = rng.sample(variables, 2)
                name, pred = rng.choice(TEMPLATES)
                cid = f"c{cid_counter}"
                cid_counter += 1
                try:
                    csp.add_constraint(cid, a, b, pred, name=name)
                except Conflict as exc:
                    conflict = exc
                ref.add_constraint(cid, a, b, pred)
            elif op == "remove":
                if not csp.constraints:
                    continue
                cid = rng.choice(sorted(csp.constraints))
                csp.remove_constraint(cid)
                ref.remove_constraint(cid)
            elif op == "delete":
                var = rng.choice(variables)
                value = rng.choice(sorted(universe[var]))
                try:
                    csp.delete_value(var, value)
                except Conflict as exc:
                    conflict = exc
                ref.delete_value(var, value)
            elif op == "assign":
                var = rng.choice(variables)
                value = rng.choice(sorted(universe[var]))
                try:
                    csp.assign(var, value)
                except Conflict as exc:
                    conflict = exc
                ref.assign(var, value)
            elif op == "push":
                csp.push()
                ref.push()
                depth += 1
            elif op == "pop":
                if depth > 1:
                    csp.pop()
                    ref.pop()
                    depth -= 1

            if conflict is not None:
                ref_domains = ref.domains()
                self.assertTrue(
                    any(len(d) == 0 for d in ref_domains.values()),
                    f"seed={seed} step={step}: CSP conflict but reference consistent",
                )
                csp.pop()
                ref.pop()
                depth -= 1
                csp.push()
                ref.push()
                depth += 1

            ref_domains = ref.domains()
            got = {v: set(d) for v, d in csp.domains.items()}
            self.assertEqual(
                got,
                ref_domains,
                f"seed={seed} step={step} op={op}: domains diverge",
            )
            validate_reasons(self, csp)

            if all(len(d) > 0 for d in ref_domains.values()):
                for solution in ref.solutions():
                    for var, value in solution.items():
                        self.assertIn(
                            value,
                            csp.domains[var],
                            f"seed={seed} step={step}: propagation deleted "
                            f"value {var}={value} of legal solution {solution}",
                        )

    def test_random_sessions(self):
        for seed in range(6):
            with self.subTest(seed=seed):
                self.run_session(seed)


if __name__ == "__main__":
    unittest.main(verbosity=2)
