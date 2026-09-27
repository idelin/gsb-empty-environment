import unittest

from fsmcheck import ActionSpec, LocalTransition, Model, Participant, Status, check, replay, verify


def party(name, transitions):
    return Participant(name, [LocalTransition(*t) for t in transitions])


class DeadlockVsTerminalTest(unittest.TestCase):
    def test_declared_terminal_without_edges_is_not_deadlock(self):
        model = Model(
            participants=[party("P", [("go", "s0", "s1")])],
            actions=[ActionSpec("go", ["P"])],
            initials=[("s0",)],
            terminals=[("s1",)],
        )
        result = check(model)
        self.assertIs(result.status, Status.NO_DEADLOCK)
        self.assertIsNone(result.counterexample)

    def test_undeclared_stuck_state_is_deadlock(self):
        model = Model(
            participants=[party("P", [("go", "s0", "s1")])],
            actions=[ActionSpec("go", ["P"])],
            initials=[("s0",)],
            terminals=[],
        )
        result = check(model)
        self.assertIs(result.status, Status.DEADLOCK)
        self.assertEqual(result.counterexample.final_state, ("s1",))

    def test_terminal_with_actions_warns_and_is_not_expanded(self):
        model = Model(
            participants=[party("P", [("go", "s0", "s1"), ("loop", "s1", "s1")])],
            actions=[ActionSpec("go", ["P"]), ActionSpec("loop", ["P"])],
            initials=[("s0",)],
            terminals=[("s1",)],
        )
        result = check(model)
        self.assertIs(result.status, Status.NO_DEADLOCK)
        self.assertTrue(any("terminal-has-actions" in w for w in result.warnings))


class SynchronizationTest(unittest.TestCase):
    def test_local_enabled_is_not_globally_executable(self):
        model = Model(
            participants=[
                party("A", [("sync", "a0", "a1")]),
                party("B", [("sync", "b1", "b2")]),
            ],
            actions=[ActionSpec("sync", ["A", "B"])],
            initials=[("a0", "b0")],
        )
        result = check(model)
        self.assertIs(result.status, Status.DEADLOCK)
        diag = result.counterexample.diagnosis
        self.assertEqual(diag.state, ("a0", "b0"))
        blocked = {b.action: b for b in diag.blocked_actions}
        self.assertIn("sync", blocked)
        self.assertEqual(blocked["sync"].missing, ["B"])
        self.assertIn("A", blocked["sync"].providers)
        preconditions = blocked["sync"].preconditions()
        self.assertTrue(any("A" in p and "sync" in p for p in preconditions))
        self.assertEqual(diag.no_provider, ["B"])
        self.assertIsNone(diag.minimal_cycle)

    def test_minimal_blocking_cycle(self):
        model = Model(
            participants=[
                party("A", [("ab", "a0", "a1")]),
                party("B", [("ba", "b0", "b1")]),
            ],
            actions=[ActionSpec("ab", ["A", "B"]), ActionSpec("ba", ["A", "B"])],
            initials=[("a0", "b0")],
        )
        result = check(model)
        self.assertIs(result.status, Status.DEADLOCK)
        diag = result.counterexample.diagnosis
        self.assertEqual(diag.minimal_cycle, ["A", "B", "A"])
        self.assertEqual(diag.no_provider, [])


class MultiInitialShortestTest(unittest.TestCase):
    def model(self):
        return Model(
            participants=[party("P", [
                ("a", "s0", "s1"), ("b", "s1", "s2"), ("c", "t0", "t1"),
            ])],
            actions=[ActionSpec(x, ["P"]) for x in "abc"],
            initials=[("s0",), ("t0",)],
            terminals=[],
        )

    def test_shortest_counterexample_across_initials(self):
        result = check(self.model())
        self.assertIs(result.status, Status.DEADLOCK)
        ev = result.counterexample
        self.assertEqual(ev.initial, ("t0",))
        self.assertEqual(len(ev.steps), 1)
        self.assertEqual(ev.final_state, ("t1",))

    def test_all_initials_covered(self):
        result = check(self.model())
        self.assertEqual(set(result.initials_covered), {("s0",), ("t0",)})

    def test_replayable(self):
        result = check(self.model())
        final = replay(self.model(), result.counterexample.initial,
                       result.counterexample.steps)
        self.assertEqual(final, result.counterexample.final_state)


class BudgetPendingTest(unittest.TestCase):
    def model(self):
        return Model(
            participants=[party("P", [(f"m{i}", f"s{i}", f"s{i+1}") for i in range(5)])],
            actions=[ActionSpec(f"m{i}", ["P"]) for i in range(5)],
            initials=[("s0",)],
        )

    def test_budget_exhausted_is_pending_not_terminated(self):
        result = check(self.model(), budget=2)
        self.assertIs(result.status, Status.PENDING)
        self.assertIsNone(result.counterexample)
        self.assertTrue(result.frontier)

    def test_pending_verification_inconclusive(self):
        result = check(self.model(), budget=2)
        report = verify(self.model(), result)
        self.assertFalse(report.conclusive)
        self.assertFalse(report.ok)

    def test_sufficient_budget_finds_deadlock(self):
        result = check(self.model(), budget=10)
        self.assertIs(result.status, Status.DEADLOCK)
        self.assertEqual(result.counterexample.final_state, ("s5",))


class LivelockTest(unittest.TestCase):
    def test_self_loop_is_livelock_candidate_not_deadlock(self):
        model = Model(
            participants=[party("P", [("spin", "s0", "s0")])],
            actions=[ActionSpec("spin", ["P"])],
            initials=[("s0",)],
        )
        result = check(model)
        self.assertIs(result.status, Status.NO_DEADLOCK)
        self.assertTrue(any("livelock-candidate" in w for w in result.warnings))


class VerificationTest(unittest.TestCase):
    def test_deadlock_result_verified(self):
        model = Model(
            participants=[
                party("A", [("ab", "a0", "a1")]),
                party("B", [("ba", "b0", "b1")]),
            ],
            actions=[ActionSpec("ab", ["A", "B"]), ActionSpec("ba", ["A", "B"])],
            initials=[("a0", "b0")],
        )
        report = verify(model, check(model))
        self.assertTrue(report.conclusive)
        self.assertTrue(report.ok, report.summary())

    def test_clean_result_verified(self):
        model = Model(
            participants=[party("P", [("go", "s0", "s1")])],
            actions=[ActionSpec("go", ["P"])],
            initials=[("s0",)],
            terminals=[("s1",)],
        )
        report = verify(model, check(model))
        self.assertTrue(report.conclusive)
        self.assertTrue(report.ok, report.summary())

    def test_replay_rejects_invalid_step(self):
        from fsmcheck.checker import Step
        model = Model(
            participants=[party("P", [("go", "s0", "s1")])],
            actions=[ActionSpec("go", ["P"])],
            initials=[("s0",)],
        )
        bad = Step("go", (("P", "s0", "s1"),), ("s9",))
        with self.assertRaises(ValueError):
            replay(model, ("s0",), [bad])


if __name__ == "__main__":
    unittest.main()
