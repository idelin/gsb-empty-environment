import unittest

from symcheck import (
    NO_PID,
    State,
    canonicalize,
    check_symmetry_condition,
    invert,
    permute_state,
    role_permutations,
)
from symcheck.examples import make_hidden_leader_model, make_mutex_model


class CanonicalizationTests(unittest.TestCase):
    def test_two_isomorphic_processes_share_one_representative(self):
        roles = ("leader", "worker", "worker")
        state_a = State(shared=(NO_PID,), locals=("idle", "critical", "waiting"))
        state_b = State(shared=(NO_PID,), locals=("idle", "waiting", "critical"))

        canonical_a, _ = canonicalize(state_a, roles, frozenset({0}))
        canonical_b, _ = canonicalize(state_b, roles, frozenset({0}))

        self.assertEqual(canonical_a, canonical_b)

    def test_leader_is_never_permuted_with_workers(self):
        roles = ("leader", "worker", "worker")
        for perm in role_permutations(roles):
            self.assertEqual(perm[0], 0)

    def test_leader_state_is_not_confused_with_worker_state(self):
        roles = ("leader", "worker", "worker")
        leader_critical = State(shared=(NO_PID,), locals=("critical", "idle", "idle"))
        worker_critical = State(shared=(NO_PID,), locals=("idle", "critical", "idle"))

        canonical_leader, _ = canonicalize(leader_critical, roles, frozenset({0}))
        canonical_worker, _ = canonicalize(worker_critical, roles, frozenset({0}))

        self.assertNotEqual(canonical_leader, canonical_worker)

    def test_holder_pid_in_shared_state_is_renamed(self):
        roles = ("leader", "worker", "worker")
        state = State(shared=(2,), locals=("idle", "idle", "critical"))

        canonical, perm = canonicalize(state, roles, frozenset({0}))

        self.assertEqual(canonical.locals, ("idle", "critical", "idle"))
        self.assertEqual(canonical.shared, (1,))
        self.assertEqual(perm, (0, 2, 1))

    def test_id_mapping_restores_the_original_state(self):
        roles = ("leader", "worker", "worker", "worker")
        original = State(
            shared=(3,),
            locals=("idle", "waiting", "idle", "critical"),
        )

        canonical, perm = canonicalize(original, roles, frozenset({0}))
        restored = permute_state(canonical, invert(perm), frozenset({0}))

        self.assertEqual(restored, original)


class SymmetryConditionTests(unittest.TestCase):
    def test_mutex_model_satisfies_the_symmetry_condition(self):
        holds, witness = check_symmetry_condition(make_mutex_model(num_workers=2))
        self.assertTrue(holds, witness)

    def test_hidden_leader_model_violates_the_symmetry_condition(self):
        holds, witness = check_symmetry_condition(make_hidden_leader_model())
        self.assertFalse(holds)
        state, perm, action, target, required_target = witness
        self.assertEqual(action, (0, "special_request"))
        self.assertEqual(perm[0], 1)
        self.assertNotEqual(required_target, target)


if __name__ == "__main__":
    unittest.main()
