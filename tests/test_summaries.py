"""Cross-function transfer via summaries, recursion fixpoint, replay."""
import unittest

from handle_verifier import (READ, SAFE, SHARED, UNSAFE, Analyzer,
                             Borrow, Call, Close, EndBorrow, ErrorKind,
                             Certainty, Function, If, Move, Open, Program,
                             Return, Use, While, replay)


def kinds(result):
    return {e.kind for e in result.errors}


def certainty_of(result, kind):
    for e in result.errors:
        if e.kind is kind:
            return e.certainty
    return None


class TestSummaries(unittest.TestCase):
    def test_callee_consumes_resource_caller_sees_moved(self):
        program = Program({
            "close_it": Function("close_it", ("p",), (Close("p"),)),
            "main": Function("main", (), (
                Open("x"),
                Call(None, "close_it", ("x",)),
                Use("x", READ),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertEqual(result.verdict, UNSAFE)
        # Ownership moved into the callee: access after the call is
        # use-after-move; the callee's own close is legitimate.
        self.assertIn(ErrorKind.USE_AFTER_MOVE, kinds(result))
        self.assertNotIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))

    def test_callee_returns_closed_resource(self):
        program = Program({
            "close_and_return": Function("close_and_return", ("p",), (
                Close("p"),
                Return("p"),
            )),
            "main": Function("main", (), (
                Open("x"),
                Call("y", "close_and_return", ("x",)),
                Use("y", READ),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.DEFINITE)

    def test_error_inside_callee_surfaces_at_call_site(self):
        program = Program({
            "bad": Function("bad", ("p",), (
                Close("p"),
                Use("p", READ),
            )),
            "main": Function("main", (), (
                Open("x"),
                Call(None, "bad", ("x",)),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertEqual(result.verdict, UNSAFE)
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        record = next(e for e in result.errors
                      if e.kind is ErrorKind.USE_AFTER_CLOSE)
        self.assertIn("call to 'bad'", record.detail)
        self.assertTrue(replay(program, record))

    def test_callee_returns_fresh_local_resource(self):
        program = Program({
            "make": Function("make", (), (
                Open("t"),
                Return("t"),
            )),
            "main": Function("main", (), (
                Call("y", "make", ()),
                Use("y", READ),
                Close("y"),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertEqual(result.verdict, SAFE)

    def test_move_same_resource_twice_into_call_is_error(self):
        program = Program({
            "take2": Function("take2", ("p", "q"), ()),
            "main": Function("main", (), (
                Open("x"),
                Call(None, "take2", ("x", "x")),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertIn(ErrorKind.INVALID_OPERATION, kinds(result))

    def test_recursive_function_fixpoint(self):
        # rec(p): maybe closes p and returns it, or recurses and returns
        # whatever the recursive call returns.  The summary fixpoint must
        # discover that the returned resource can be closed.
        program = Program({
            "rec": Function("rec", ("p",), (
                If(
                    (Close("p"), Return("p")),
                    (Call("r", "rec", ("p",)), Return("r")),
                ),
            )),
            "main": Function("main", (), (
                Open("x"),
                Call("y", "rec", ("x",)),
                Use("y", READ),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertEqual(result.verdict, UNSAFE)
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        # Every path through rec closes the resource, so this is definite.
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.DEFINITE)

    def test_recursive_may_close_is_possible(self):
        # rec(p): maybe returns p untouched, maybe closes then recurses.
        program = Program({
            "rec": Function("rec", ("p",), (
                If(
                    (Return("p"),),
                    (Close("p"), Call("r", "rec", ("p",)), Return("r")),
                ),
            )),
            "main": Function("main", (), (
                Open("x"),
                Call("y", "rec", ("x",)),
                Use("y", READ),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertIn(ErrorKind.USE_AFTER_CLOSE, kinds(result))
        self.assertIs(certainty_of(result, ErrorKind.USE_AFTER_CLOSE),
                      Certainty.POSSIBLE)

    def test_borrow_returned_before_call_is_safe(self):
        program = Program({
            "use_it": Function("use_it", ("p",), (Use("p", READ),)),
            "main": Function("main", (), (
                Open("x"),
                Borrow("b", "x", SHARED),
                EndBorrow("b"),
                Call(None, "use_it", ("x",)),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertEqual(result.verdict, SAFE)

    def test_call_while_borrowed_is_conflict(self):
        program = Program({
            "use_it": Function("use_it", ("p",), (Use("p", READ),)),
            "main": Function("main", (), (
                Open("x"),
                Borrow("b", "x", SHARED),
                Call(None, "use_it", ("x",)),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertIn(ErrorKind.BORROW_CONFLICT, kinds(result))


class TestCounterexamples(unittest.TestCase):
    def test_branchy_counterexample_replays(self):
        program = Program({
            "main": Function("main", (), (
                Open("x"),
                If((Close("x"),), ()),
                Use("x", READ),
            )),
        })
        result = Analyzer(program).analyze()
        record = next(e for e in result.errors
                      if e.kind is ErrorKind.USE_AFTER_CLOSE)
        self.assertEqual(record.choices, ("then",))
        self.assertTrue(replay(program, record))

    def test_loop_counterexample_replays(self):
        program = Program({
            "main": Function("main", (), (
                Open("x"),
                While((Close("x"),)),
                Use("x", READ),
            )),
        })
        result = Analyzer(program).analyze()
        for record in result.errors:
            self.assertTrue(replay(program, record),
                            f"replay failed for {record}")

    def test_all_reported_errors_replay(self):
        program = Program({
            "bad": Function("bad", ("p",), (
                If((Close("p"),), (Move("p"),)),
                Use("p", READ),
            )),
            "main": Function("main", (), (
                Open("x"),
                Call(None, "bad", ("x",)),
            )),
        })
        result = Analyzer(program).analyze()
        self.assertEqual(result.verdict, UNSAFE)
        self.assertTrue(result.errors)
        for record in result.errors:
            self.assertTrue(replay(program, record),
                            f"replay failed for {record}")


if __name__ == "__main__":
    unittest.main()
