import re
import unittest

from fsv import (
    ERROR,
    SAFE,
    UNKNOWN,
    UNSAFE,
    WRAP,
    IntDomain,
    Ref,
    State,
    assert_,
    assign,
    bin,
    explore,
    idx,
    iff,
    interpret,
    lit,
    newarr,
    normalize,
    store,
    try_,
    var,
    verify,
    while_,
)

DOM_ERR = IntDomain(-8, 7, ERROR)
DOM_WRAP = IntDomain(-8, 7, WRAP)


class TestOverflowModes(unittest.TestCase):
    def test_overflow_modes_differ(self):
        # x := 7; x := x + 1  on int[-8..7]
        prog = (assign("x", 7), assign("x", bin("+", "x", 1)))
        v_err = verify(prog, DOM_ERR)
        v_wrap = verify(prog, DOM_WRAP)
        self.assertEqual(v_err.result, UNSAFE)          # overflow is an error
        self.assertEqual(v_err.fault_kind, "overflow")
        self.assertEqual(v_wrap.result, SAFE)           # wraps to -8, no fault
        self.assertNotEqual(v_err.result, v_wrap.result)
        self.assertTrue(v_err.replay_ok)

    def test_wrap_semantics_observable(self):
        # Under WRAP, 7 + 1 wraps to -8, so the assertion holds.
        prog = (
            assign("x", 7),
            assign("x", bin("+", "x", 1)),
            assert_(bin("==", "x", -8)),
        )
        self.assertEqual(verify(prog, DOM_WRAP).result, SAFE)
        self.assertEqual(verify(prog, DOM_ERR).result, UNSAFE)


class TestFaults(unittest.TestCase):
    def test_uninitialized_read(self):
        prog = (assign("y", bin("+", "x", 1)),)
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertEqual(v.fault_kind, "uninit_read")
        self.assertTrue(v.replay_ok)

    def test_assert_failure_replayable(self):
        prog = (
            assign("x", 0),
            assign("x", bin("-", "x", 1)),
            assert_(bin(">=", "x", 0)),
        )
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertEqual(v.fault_kind, "assert")
        self.assertTrue(v.replay_ok)

    def test_index_out_of_bounds(self):
        prog = (newarr("a", 2), store("a", 5, 1))
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertEqual(v.fault_kind, "index_out_of_bounds")
        self.assertTrue(v.replay_ok)


class TestHeapAliasing(unittest.TestCase):
    def test_aliasing_visible_through_heap(self):
        # b aliases a, so a[0] := 5 is visible as b[0].
        prog = (
            newarr("a", 2),
            assign("b", var("a")),
            store("a", 0, 5),
            assert_(bin("==", idx("b", 0), 5)),
        )
        self.assertEqual(verify(prog, DOM_ERR).result, SAFE)

    def test_distinct_objects_do_not_alias(self):
        prog = (
            newarr("a", 2),
            newarr("b", 2),
            store("a", 0, 5),
            assert_(bin("==", idx("b", 0), 5)),
        )
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertEqual(v.fault_kind, "assert")
        self.assertTrue(v.replay_ok)

    def test_normalization_preserves_sharing_identity(self):
        # Same contents, different sharing: must normalize differently.
        shared = State((), (("x", Ref(5)), ("y", Ref(5))), ((5, (1, 2)),), 6)
        split = State(
            (),
            (("x", Ref(3)), ("y", Ref(7))),
            ((3, (1, 2)), (7, (1, 2))),
            8,
        )
        self.assertNotEqual(normalize(shared), normalize(split))
        # Same sharing, different id numbering: must normalize identically.
        shared2 = State((), (("x", Ref(9)), ("y", Ref(9))), ((9, (1, 2)),), 10)
        self.assertEqual(normalize(shared), normalize(shared2))
        # And the canonical form really keeps both names on one object.
        norm = normalize(shared)
        self.assertEqual(dict(norm.env)["x"], dict(norm.env)["y"])
        self.assertEqual(len(norm.heap), 1)

    def test_normalization_enables_state_merging(self):
        # Two allocation orders reach the same canonical state, so the
        # explored graph stays small instead of forking per id numbering.
        prog = (
            newarr("a", 1),
            newarr("b", 1),
            iff(1, (assign("t", var("a")),), (assign("t", var("b")),)),
        )
        g = explore(prog, DOM_ERR)
        self.assertTrue(g.complete)
        for s in g.states:
            self.assertIsNone(s.fault)


class TestExceptionCleanup(unittest.TestCase):
    def test_finally_runs_on_overflow(self):
        # Overflow inside try must still execute the finally cleanup.
        prog = (
            try_(
                (assign("x", 7), assign("x", bin("+", "x", 1))),
                (assign("c", 1),),
            ),
        )
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertEqual(v.fault_kind, "overflow")
        self.assertTrue(v.replay_ok)
        # The final fault state shows the cleanup assignment happened.
        last = v.trace[-1]
        self.assertIn("c=1", last.state)
        kinds = [t.kind for t in v.trace]
        self.assertIn("cleanup", kinds)

    def test_finally_runs_on_normal_path(self):
        prog = (try_((assign("x", 1),), (assign("c", 2),)),)
        g = explore(prog, DOM_ERR)
        self.assertTrue(g.complete)
        finals = [s for s in g.states if s.halted]
        self.assertEqual(len(finals), 1)
        self.assertEqual(dict(finals[0].env)["c"], 2)

    def test_nested_try_propagates_through_both_cleanups(self):
        prog = (
            try_(
                (
                    try_(
                        (assign("x", 7), assign("x", bin("+", "x", 1))),
                        (assign("inner", 1),),
                    ),
                ),
                (assign("outer", 1),),
            ),
        )
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertTrue(v.replay_ok)
        last = v.trace[-1].state
        self.assertIn("inner=1", last)
        self.assertIn("outer=1", last)


class TestLoopsAndBudget(unittest.TestCase):
    def test_nonterminating_loop_does_not_hang_analyzer(self):
        # State cycles forever through the finite domain; exploration
        # terminates by fixpoint instead of hanging.
        prog = (assign("x", 0), while_(1, (assign("x", bin("+", "x", 1)),)))
        v = verify(prog, DOM_WRAP)
        self.assertEqual(v.result, SAFE)
        # In ERROR mode the same loop eventually overflows: modes differ.
        v_err = verify(prog, DOM_ERR)
        self.assertEqual(v_err.result, UNSAFE)
        self.assertEqual(v_err.fault_kind, "overflow")
        self.assertTrue(v_err.replay_ok)

    def test_true_infinite_loop_fixpoint(self):
        prog = (assign("x", 0), while_(1, (assign("x", var("x")),)))
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, SAFE)  # one repeating state, no fault

    def test_budget_exhaustion_is_unknown_not_success(self):
        prog = (assign("x", 0), while_(1, (assign("x", bin("+", "x", 1)),)))
        v = verify(prog, DOM_WRAP, max_states=4)
        self.assertEqual(v.result, UNKNOWN)
        self.assertNotEqual(v.result, SAFE)
        self.assertIn("not fully explored", v.detail)
        self.assertIsNone(v.trace)

    def test_step_budget_exhaustion_is_unknown(self):
        prog = (assign("x", 0), while_(1, (assign("x", bin("+", "x", 1)),)))
        v = verify(prog, DOM_WRAP, max_steps=3)
        self.assertEqual(v.result, UNKNOWN)


class TestDeepControlFlow(unittest.TestCase):
    def test_deep_nesting_uses_explicit_stack(self):
        # 200 nested conditionals would blow Python's recursion limit in a
        # recursive interpreter; the frame machine handles them iteratively.
        depth = 200
        prog = (assign("x", 1),)
        for _ in range(depth):
            prog = (iff(1, prog),)
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, SAFE)
        self.assertEqual(v.explored_states, depth + 2)


class TestEvidenceAndReplay(unittest.TestCase):
    def test_every_step_has_source_location(self):
        prog = (
            assign("x", 0),
            assign("x", bin("-", "x", 1)),
            assert_(bin(">=", "x", 0)),
        )
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, UNSAFE)
        self.assertTrue(v.trace)
        for t in v.trace:
            self.assertRegex(t.loc, re.compile(r"^test_fsv\.py:\d+$"))
            self.assertTrue(t.desc)

    def test_source_to_state_mapping(self):
        prog = (
            assign("x", 1),
            assign("y", 2),
        )
        g = explore(prog, DOM_ERR)
        self.assertEqual(len(g.loc_index), 2)
        for loc, states in g.loc_index.items():
            self.assertRegex(loc, re.compile(r"^test_fsv\.py:\d+$"))
            self.assertTrue(states)

    def test_independent_interpreter_matches_executor(self):
        prog = (
            newarr("a", 3),
            assign("i", 0),
            while_(
                bin("<", "i", 3),
                (store("a", "i", bin("*", "i", 2)), assign("i", bin("+", "i", 1))),
            ),
            assert_(bin("==", idx("a", 2), 4)),
        )
        v = verify(prog, DOM_ERR)
        self.assertEqual(v.result, SAFE)
        res = interpret(prog, DOM_ERR)
        self.assertTrue(res.completed)
        self.assertIsNone(res.fault)


if __name__ == "__main__":
    unittest.main()
