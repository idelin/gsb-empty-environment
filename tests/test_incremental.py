import os
import unittest

from invcheck import (FAIL_BASE, FAIL_STEP, PASS, IncrementalSession,
                      load_model)

EXAMPLES = os.path.join(os.path.dirname(os.path.dirname(
    os.path.abspath(__file__))), "examples")


class TestIncremental(unittest.TestCase):
    def test_session_recheck_after_edit(self):
        model = load_model(os.path.join(EXAMPLES, "mod3_counter.json"))
        session = IncrementalSession(model)
        r1 = session.check("~b1")            # too weak: FAIL_STEP
        self.assertEqual(r1.verdict, FAIL_STEP)
        r2 = session.check("~(b1 & b0)")     # corrected candidate
        self.assertEqual(r2.verdict, PASS)
        self.assertEqual(len(session.history), 2)

    def test_suggest_strengthening_excludes_cex(self):
        model = load_model(os.path.join(EXAMPLES, "mod5_unreachable_step.json"))
        session = IncrementalSession(model)  # candidate: v != 5 (not inductive)
        res = session.check()
        self.assertEqual(res.verdict, FAIL_STEP)
        suggestion = session.suggest_strengthening()
        self.assertIsNotNone(suggestion)
        # the suggested candidate must exclude the cex source cube
        import invcheck.expr as expr_mod
        ast = expr_mod.parse(suggestion)
        src = {v: res.step_cex.state.get(v, False) for v in model.variables}
        self.assertFalse(expr_mod.eval_ast(ast, src))

    def test_auto_strengthen_converges(self):
        model = load_model(os.path.join(EXAMPLES, "mod5_unreachable_step.json"))
        session = IncrementalSession(model)  # candidate: v != 5 (not inductive)
        final = session.auto_strengthen(max_rounds=8)
        self.assertEqual(final.verdict, PASS)
        self.assertLessEqual(len(session.history), 8)

    def test_auto_strengthen_detects_base_failure(self):
        # candidate "1" strengthened against reachable bad transitions must
        # eventually exclude initial states -> FAIL_BASE, not an infinite loop
        model = load_model(os.path.join(EXAMPLES, "mod3_bad_invariant.json"))
        session = IncrementalSession(model)  # candidate: ~b1, fails on reachable
        final = session.auto_strengthen(max_rounds=16)
        # strengthening against reachable bad transitions must eventually
        # exclude the initial state -> FAIL_BASE, not an infinite loop
        self.assertEqual(final.verdict, FAIL_BASE)


if __name__ == "__main__":
    unittest.main()
