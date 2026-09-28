import unittest

from symcheck import (
    PENDING,
    SAFE,
    VIOLATED,
    canonicalize,
    invert,
    permute_state,
    replay,
    verify,
)
from symcheck.examples import at_most_one_critical, make_mutex_model


class VerificationTests(unittest.TestCase):
    def test_safe_model_is_safe_with_and_without_reduction(self):
        model = make_mutex_model(num_workers=2, buggy=False)

        reduced = verify(model, at_most_one_critical, use_symmetry=True)
        unreduced = verify(model, at_most_one_critical, use_symmetry=False)

        self.assertEqual(reduced.status, SAFE)
        self.assertEqual(unreduced.status, SAFE)

    def test_buggy_model_is_violated_and_compression_does_not_hide_it(self):
        model = make_mutex_model(num_workers=2, buggy=True)

        reduced = verify(model, at_most_one_critical, use_symmetry=True)
        unreduced = verify(model, at_most_one_critical, use_symmetry=False)

        self.assertEqual(reduced.status, VIOLATED)
        self.assertEqual(unreduced.status, VIOLATED)

    def test_counterexample_is_replayable(self):
        model = make_mutex_model(num_workers=2, buggy=True)
        verdict = verify(model, at_most_one_critical, use_symmetry=True)
        cex = verdict.counterexample

        self.assertIsNotNone(cex)
        replayed = replay(model, cex.actions)

        self.assertEqual(replayed, cex.states)
        self.assertFalse(at_most_one_critical(cex.states[-1]))

    def test_counterexample_id_maps_are_preserved_and_restorable(self):
        model = make_mutex_model(num_workers=2, buggy=True)
        verdict = verify(model, at_most_one_critical, use_symmetry=True)

        for state, perm in zip(verdict.counterexample.states, verdict.counterexample.id_maps):
            canonical, used_perm = canonicalize(
                state, model.roles, model.pid_vars
            )
            self.assertEqual(perm, used_perm)
            self.assertEqual(
                permute_state(state, perm, model.pid_vars), canonical
            )
            self.assertEqual(
                permute_state(canonical, invert(perm), model.pid_vars), state
            )

    def test_search_remains_pending_when_budget_is_exhausted(self):
        model = make_mutex_model(num_workers=3, buggy=False)

        verdict = verify(
            model,
            at_most_one_critical,
            use_symmetry=True,
            max_states=1,
        )

        self.assertEqual(verdict.status, PENDING)
        self.assertFalse(verdict.decided)

    def test_full_budget_on_small_model_is_safe_not_pending(self):
        model = make_mutex_model(num_workers=2, buggy=False)
        verdict = verify(
            model,
            at_most_one_critical,
            use_symmetry=True,
            max_states=10_000,
        )
        self.assertEqual(verdict.status, SAFE)

    def test_small_model_properties_match_before_and_after_compression(self):
        for buggy in (False, True):
            with self.subTest(buggy=buggy):
                model = make_mutex_model(num_workers=2, buggy=buggy)
                reduced = verify(model, at_most_one_critical, use_symmetry=True)
                unreduced = verify(model, at_most_one_critical, use_symmetry=False)
                self.assertEqual(reduced.status, unreduced.status)
                self.assertLess(reduced.explored, unreduced.explored)

    def test_replay_rejects_actions_not_enabled(self):
        model = make_mutex_model(num_workers=2)
        with self.assertRaises(ValueError):
            replay(model, [(0, "acquire")])


if __name__ == "__main__":
    unittest.main()
