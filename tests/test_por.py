"""Tests for the POR verifier, including cross-checks vs full enumeration."""
import random
import unittest

from por import examples, explore
from por.explore import PENDING, SAFE, UNSAFE, dependent, enabled_actions, replay
from por.model import Action, Model, make_state, state_dict


def _const_action(name, guard_vars, guard_vals, write_vals):
    def guard(s, gv=guard_vals):
        d = state_dict(s)
        return all(d[v] == val for v, val in gv.items())

    def effect(s, wv=write_vals):
        d = state_dict(s)
        d.update(wv)
        return make_state(d)

    return Action(name, guard, effect, frozenset(guard_vals), frozenset(write_vals))


def random_model(rng, idx):
    """Small random finite-state model with exact read/write sets."""
    nvars = rng.randint(2, 3)
    dom = rng.randint(2, 3)
    variables = tuple(f"v{i}" for i in range(nvars))
    actions = []
    for i in range(rng.randint(2, 5)):
        gvals = {v: rng.randrange(dom) for v in variables if rng.random() < 0.5}
        wvars = [v for v in variables if rng.random() < 0.6] or [rng.choice(variables)]
        wvals = {v: rng.randrange(dom) for v in wvars}
        actions.append(_const_action(f"a{i}", None, gvals, wvals))
    if rng.random() < 0.7:
        pvar = rng.choice(variables)
        bad = rng.randrange(dom)
        prop = (lambda s, pv=pvar, b=bad: state_dict(s)[pv] != b)
        pvars = frozenset({pvar})
        pname = f"{pvar} != {bad}"
    else:
        prop, pvars, pname = None, frozenset(), ""
    return Model(
        name=f"random_{idx}",
        variables=variables,
        initial=make_state({v: 0 for v in variables}),
        actions=tuple(actions),
        property_fn=prop,
        property_vars=pvars,
        property_name=pname,
        check_deadlock=True,
    )


class TestIndependence(unittest.TestCase):
    def test_write_write_same_variable_is_dependent(self):
        a = _const_action("a", None, {}, {"x": 1})
        b = _const_action("b", None, {}, {"x": 2})
        self.assertTrue(dependent(a, b))

    def test_write_read_is_dependent(self):
        a = _const_action("a", None, {}, {"x": 1})
        b = _const_action("b", None, {"x": 0}, {"y": 1})
        self.assertTrue(dependent(a, b))
        self.assertTrue(dependent(b, a))

    def test_disjoint_footprints_are_independent(self):
        a = _const_action("a", None, {"x": 0}, {"x": 1})
        b = _const_action("b", None, {"y": 0}, {"y": 1})
        self.assertFalse(dependent(a, b))


class TestReduction(unittest.TestCase):
    def test_independent_actions_reduce(self):
        model = examples.independent_counters(n=3, k=3)
        full = explore.explore(model, use_por=False)
        por = explore.explore(model, use_por=True)
        self.assertEqual(full.verdict, SAFE)
        self.assertEqual(por.verdict, SAFE)
        self.assertEqual(full.states_explored, 4 ** 3)
        self.assertLess(por.states_explored, full.states_explored)
        # one path per process chain: n*k + 1 states
        self.assertEqual(por.states_explored, 3 * 3 + 1)

    def test_shared_variable_blocks_reduction(self):
        model = examples.shared_counter(n=2, k=4)
        full = explore.explore(model, use_por=False)
        por = explore.explore(model, use_por=True)
        self.assertEqual(por.verdict, full.verdict)
        self.assertEqual(por.states_explored, full.states_explored)

    def test_visible_property_blocks_reduction(self):
        model = examples.visible_property_counters(n=2, k=2)
        full = explore.explore(model, use_por=False)
        por = explore.explore(model, use_por=True)
        self.assertEqual(por.verdict, SAFE)
        self.assertEqual(por.states_explored, full.states_explored)

    def test_invisible_property_allows_reduction(self):
        model = examples.counters_with_flag(n=2, k=3)
        full = explore.explore(model, use_por=False)
        por = explore.explore(model, use_por=True)
        self.assertEqual(por.verdict, full.verdict)
        self.assertLess(por.states_explored, full.states_explored)

    def test_evidence_records_reduction_choices(self):
        model = examples.independent_counters(n=2, k=2)
        por = explore.explore(model, use_por=True)
        reduced = [r for r in por.log if not r.fully_expanded]
        self.assertTrue(reduced)
        self.assertTrue(any("persistent set" in n for r in reduced for n in r.notes))
        for rec in reduced:
            self.assertLess(len(rec.chosen), len(rec.enabled))


class TestDeadlockAndProviso(unittest.TestCase):
    def test_deadlock_preserved_under_reduction(self):
        model = examples.deadlock_model()
        full = explore.explore(model, use_por=False)
        por = explore.explore(model, use_por=True)
        self.assertEqual((full.verdict, full.kind), (UNSAFE, "deadlock"))
        self.assertEqual((por.verdict, por.kind), (UNSAFE, "deadlock"))
        # DFS finds a deadlock at the end of its first maximal path, so POR
        # cannot explore more states than full enumeration here.
        self.assertLessEqual(por.states_explored, full.states_explored)
        self.assertTrue(any(not r.fully_expanded for r in por.log))
        final = replay(model, por.trace)
        self.assertEqual(enabled_actions(model, final), [])

    def test_cycle_proviso_fixes_ignoring_problem(self):
        model = examples.flag_race()
        full = explore.explore(model, use_por=False)
        naive = explore.explore(model, use_por=True, cycle_proviso=False)
        por = explore.explore(model, use_por=True)
        self.assertEqual(full.verdict, UNSAFE)
        # without the proviso the loop hides the violation (documented bug)
        self.assertEqual(naive.verdict, SAFE)
        # with the proviso the violation is found again
        self.assertEqual((por.verdict, por.kind), (UNSAFE, "property_violation"))
        self.assertTrue(
            any("cycle proviso" in n for r in por.log for n in r.notes)
        )
        final = replay(model, por.trace)
        self.assertFalse(model.property_fn(final))


class TestVerdictsAndReplay(unittest.TestCase):
    def test_truncation_stays_pending(self):
        model = examples.independent_counters(n=3, k=3)
        res = explore.explore(model, use_por=False, max_states=5)
        self.assertEqual(res.verdict, PENDING)
        self.assertTrue(res.truncated)
        res = explore.explore(model, use_por=True, max_depth=2)
        self.assertEqual(res.verdict, PENDING)

    def test_property_violation_trace_replays(self):
        model = examples.flag_race()
        res = explore.explore(model, use_por=True)
        self.assertEqual(res.verdict, UNSAFE)
        final = replay(model, res.trace)
        self.assertFalse(model.property_fn(final))

    def test_replay_rejects_bad_trace(self):
        model = examples.deadlock_model()
        with self.assertRaises(explore.ReplayError):
            replay(model, ["setx", "setx"])


class TestCrossCheckRandomModels(unittest.TestCase):
    def test_por_matches_full_enumeration(self):
        for seed in range(300):
            rng = random.Random(seed)
            model = random_model(rng, seed)
            full = explore.explore(model, use_por=False)
            por = explore.explore(model, use_por=True)
            self.assertEqual(
                full.verdict, por.verdict, f"seed={seed} model={model.name}"
            )
            self.assertLessEqual(por.states_explored, full.states_explored)
            if por.verdict != UNSAFE:
                continue
            final = replay(model, por.trace)
            if por.kind == "property_violation":
                self.assertFalse(model.property_fn(final), f"seed={seed}")
            else:
                self.assertEqual(por.kind, "deadlock")
                self.assertEqual(enabled_actions(model, final), [], f"seed={seed}")


if __name__ == "__main__":
    unittest.main()
