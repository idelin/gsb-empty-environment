"""Core semantics: aliasing identity, borrow discipline, state checks."""
import unittest

from handle_verifier import (EXCLUSIVE, PENDING, READ, SAFE, SHARED, UNSAFE,
                             WRITE, Analyzer, Assign, Borrow, Call, Close,
                             EndBorrow, ErrorKind, Certainty, Function, If,
                             Move, Open, Program, Use, While)


def analyze(body, **kwargs):
    program = Program({"main": Function("main", (), tuple(body))})
    return Analyzer(program, **kwargs).analyze()


def kinds(result):
    return {e.kind for e in result.errors}


def certainty_of(result, kind):
    for e in result.errors:
        if e.kind is kind:
            return e.certainty
    return None


class TestLifecycle(unittest.TestCase):
    def test_open_use_close_is_safe(self):
        result = analyze([Open("x"), Use("x", READ), Use("x", WRITE),
                          Close("x")])
        self.assertEqual(result.verdict, SAFE)
        self.assertEqual(result.errors, [])

    def test_use_after_close_is_definite(self):
        result = analyze([Open("x"), Close("x"), Use("x", READ)])
        self.assertEqual(result.verdict, UNSAFE)
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.DEFINITE)

    def test_double_close(self):
        result = analyze([Open("x"), Close("x"), Close("x")])
        self.assertIn(ErrorKind.DOUBLE_CLOSE, kinds(result))

    def test_close_on_one_branch_then_merged_use_is_possible(self):
        # Only one branch closes; the merged use must be a *possible* error.
        result = analyze([Open("x"),
                          If((Close("x"),), ()),
                          Use("x", READ)])
        self.assertEqual(result.verdict, UNSAFE)
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.POSSIBLE)

    def test_close_on_both_branches_then_use_is_definite(self):
        result = analyze([Open("x"),
                          If((Close("x"),), (Close("x"),)),
                          Use("x", READ)])
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.DEFINITE)

    def test_move_then_access(self):
        result = analyze([Open("x"), Move("x"), Use("x", READ)])
        self.assertIn(ErrorKind.USE_AFTER_MOVE, kinds(result))

    def test_unbound_variable(self):
        result = analyze([Use("nope", READ)])
        self.assertIn(ErrorKind.UNBOUND_VAR, kinds(result))


class TestAliasing(unittest.TestCase):
    def test_aliases_share_one_resource_identity(self):
        # Closing through one alias must be visible through the other:
        # aliases are never treated as two independent open resources.
        result = analyze([Open("x"), Assign("y", "x"),
                          Close("y"), Use("x", READ)])
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.DEFINITE)

    def test_alias_move_blocks_owner_access(self):
        result = analyze([Open("x"), Assign("y", "x"),
                          Move("y"), Use("x", WRITE)])
        self.assertIn(ErrorKind.USE_AFTER_MOVE, kinds(result))

    def test_alias_close_blocks_double_close(self):
        result = analyze([Open("x"), Assign("y", "x"),
                          Close("x"), Close("y")])
        self.assertIn(ErrorKind.DOUBLE_CLOSE, kinds(result))


class TestBorrowing(unittest.TestCase):
    def test_shared_reads_are_safe(self):
        result = analyze([Open("x"),
                          Borrow("b", "x", SHARED), Borrow("c", "x", SHARED),
                          Use("b", READ), Use("c", READ), Use("x", READ),
                          EndBorrow("b"), EndBorrow("c"),
                          Close("x")])
        self.assertEqual(result.verdict, SAFE)

    def test_exclusive_write_is_safe(self):
        result = analyze([Open("x"),
                          Borrow("b", "x", EXCLUSIVE),
                          Use("b", WRITE), Use("b", READ),
                          EndBorrow("b"),
                          Use("x", WRITE), Close("x")])
        self.assertEqual(result.verdict, SAFE)

    def test_write_through_shared_borrow_conflicts(self):
        result = analyze([Open("x"), Borrow("b", "x", SHARED),
                          Use("b", WRITE)])
        self.assertIn(ErrorKind.BORROW_CONFLICT, kinds(result))

    def test_exclusive_borrow_excludes_others(self):
        result = analyze([Open("x"), Borrow("b", "x", SHARED),
                          Borrow("c", "x", EXCLUSIVE)])
        self.assertIn(ErrorKind.BORROW_CONFLICT, kinds(result))

    def test_owner_write_while_borrowed_conflicts(self):
        result = analyze([Open("x"), Borrow("b", "x", SHARED),
                          Use("x", WRITE)])
        self.assertIn(ErrorKind.BORROW_CONFLICT, kinds(result))

    def test_close_during_borrow(self):
        result = analyze([Open("x"), Borrow("b", "x", SHARED), Close("x")])
        self.assertIn(ErrorKind.CLOSE_WHILE_BORROWED, kinds(result))

    def test_borrow_return_restores_state(self):
        # After the borrow ends the resource is fully usable again.
        result = analyze([Open("x"), Borrow("b", "x", EXCLUSIVE),
                          EndBorrow("b"), Use("x", WRITE), Close("x")])
        self.assertEqual(result.verdict, SAFE)

    def test_handle_dead_after_endborrow(self):
        result = analyze([Open("x"), Borrow("b", "x", SHARED),
                          EndBorrow("b"), Use("b", READ)])
        self.assertIn(ErrorKind.UNBOUND_VAR, kinds(result))


class TestControlFlow(unittest.TestCase):
    def test_loop_close_then_use_is_possible(self):
        result = analyze([Open("x"),
                          While((Close("x"),)),
                          Use("x", READ)])
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.POSSIBLE)
        # Second iteration closes an already closed resource.
        self.assertIn(ErrorKind.DOUBLE_CLOSE, kinds(result))

    def test_loop_borrow_return_is_safe(self):
        result = analyze([Open("x"),
                          While((Borrow("b", "x", SHARED),
                                 Use("b", READ),
                                 EndBorrow("b"))),
                          Close("x")])
        self.assertEqual(result.verdict, SAFE)

    def test_loop_move_inside_then_use(self):
        result = analyze([Open("x"),
                          While((Move("x"),)),
                          Use("x", READ)])
        self.assertIn(ErrorKind.USE_AFTER_MOVE, kinds(result))

    def test_merge_preserves_ownership_possibilities(self):
        # x may be moved on one branch; the join must keep both options.
        result = analyze([Open("x"),
                          If((Move("x"),), ()),
                          Use("x", READ)])
        self.assertIn(ErrorKind.USE_AFTER_MOVE, kinds(result))
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_MOVE),
                      Certainty.POSSIBLE)


class TestPending(unittest.TestCase):
    def test_unknown_function_keeps_verdict_pending(self):
        result = analyze([Open("x"), Call(None, "mystery", ("x",))])
        self.assertEqual(result.verdict, PENDING)
        self.assertTrue(any("mystery" in r for r in result.reasons))

    def test_exhausted_budget_keeps_verdict_pending(self):
        body = [Open("x")]
        for _ in range(30):
            body.append(If((Use("x", READ),), (Use("x", WRITE),)))
        result = analyze(body, max_steps=20)
        self.assertEqual(result.verdict, PENDING)
        self.assertTrue(any("budget" in r for r in result.reasons))


if __name__ == "__main__":
    unittest.main()
