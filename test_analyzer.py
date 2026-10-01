"""unittest suite for the handle-state verifier.  Run: python3 -m unittest -v"""
from __future__ import annotations

import unittest

from analyzer import Analyzer
from concrete import confirm, explore, replay
from enumerate_check import run_check
from model import (Alias, BorrowMut, BorrowShared, Call, Close, EndBorrow,
                   Function, If, Move, Open, Read, Return, While, Write,
                   program)


def analyze(prog, **kw):
    return Analyzer(prog, **kw).analyze()


def kinds(rep):
    return {e.kind for e in rep.errors}


class TestLifecycle(unittest.TestCase):
    def test_open_close_read_is_safe(self):
        rep = analyze(program([Open("x"), Read("x"), Close("x")]))
        self.assertEqual(rep.verdict, "SAFE")

    def test_use_after_close(self):
        rep = analyze(program([Open("x"), Close("x"), Read("x")]))
        self.assertEqual(rep.verdict, "UNSAFE")
        self.assertIn("use-after-close", kinds(rep))

    def test_double_close(self):
        rep = analyze(program([Open("x"), Close("x"), Close("x")]))
        self.assertIn("double-close", kinds(rep))

    def test_close_on_one_branch_use_after_merge(self):
        # only the then-branch closes; the merged use is a *possible* error
        rep = analyze(program([Open("x"), If([Close("x")], []), Read("x")]))
        self.assertEqual(rep.verdict, "UNSAFE")
        self.assertIn("use-after-close", kinds(rep))
        # ... and it is concretizable (the then-branch really reaches it)
        confirm(program([Open("x"), If([Close("x")], []), Read("x")]), rep)
        err = next(e for e in rep.errors if e.kind == "use-after-close")
        self.assertIsNotNone(err.concrete_trace)
        kind, snap = replay(
            program([Open("x"), If([Close("x")], []), Read("x")]),
            err.concrete_trace)
        self.assertEqual(kind, "use-after-close")
        self.assertEqual(snap["res"][0]["status"], "closed")

    def test_merge_keeps_possible_ownership(self):
        # after the merge, x may be open or closed: both uses are checked
        rep = analyze(program([Open("x"), If([Close("x")], []),
                               Write("x"), Close("x")]))
        self.assertIn("use-after-close", kinds(rep))
        self.assertIn("double-close", kinds(rep))


class TestBorrow(unittest.TestCase):
    def test_shared_borrow_allows_reads(self):
        rep = analyze(program([Open("x"), BorrowShared("x", "r"),
                               Read("r"), Read("x"), EndBorrow("r"),
                               Close("x")]))
        self.assertEqual(rep.verdict, "SAFE")

    def test_shared_borrow_forbids_owner_write(self):
        rep = analyze(program([Open("x"), BorrowShared("x", "r"), Write("x")]))
        self.assertIn("write-while-shared-borrowed", kinds(rep))

    def test_write_through_shared_borrow(self):
        rep = analyze(program([Open("x"), BorrowShared("x", "r"), Write("r")]))
        self.assertIn("write-through-shared-borrow", kinds(rep))

    def test_mut_borrow_is_exclusive(self):
        rep = analyze(program([Open("x"), BorrowMut("x", "w"), Read("x")]))
        self.assertIn("read-while-mut-borrowed", kinds(rep))
        rep = analyze(program([Open("x"), BorrowMut("x", "w"),
                               BorrowShared("x", "r")]))
        self.assertIn("shared-borrow-while-mut", kinds(rep))
        rep = analyze(program([Open("x"), BorrowMut("x", "w"),
                               BorrowMut("x", "v")]))
        self.assertIn("double-mut-borrow", kinds(rep))

    def test_end_borrow_restores_owner(self):
        rep = analyze(program([Open("x"), BorrowMut("x", "w"), Write("w"),
                               EndBorrow("w"), Write("x"), Close("x")]))
        self.assertEqual(rep.verdict, "SAFE")

    def test_close_while_borrowed(self):
        rep = analyze(program([Open("x"), BorrowShared("x", "r"), Close("x")]))
        self.assertIn("close-while-borrowed", kinds(rep))

    def test_borrow_after_close(self):
        rep = analyze(program([Open("x"), Close("x"), BorrowShared("x", "r")]))
        self.assertIn("borrow-after-close", kinds(rep))


class TestMoveAlias(unittest.TestCase):
    def test_use_after_move(self):
        rep = analyze(program([Open("x"), Move("y", "x"), Read("x")]))
        self.assertIn("use-after-move", kinds(rep))

    def test_new_owner_usable(self):
        rep = analyze(program([Open("x"), Move("y", "x"), Write("y"),
                               Close("y")]))
        self.assertEqual(rep.verdict, "SAFE")

    def test_move_while_borrowed(self):
        rep = analyze(program([Open("x"), BorrowShared("x", "r"),
                               Move("y", "x")]))
        self.assertIn("move-while-borrowed", kinds(rep))

    def test_aliases_share_one_identity(self):
        # closing through one alias must affect uses through the other:
        # the two variables name the *same* resource, not two handles
        rep = analyze(program([Open("x"), Alias("y", "x"),
                               Close("y"), Read("x")]))
        self.assertEqual(rep.verdict, "UNSAFE")
        self.assertIn("use-after-close", kinds(rep))
        err = rep.errors[0]
        self.assertEqual(err.state.env["x"], err.state.env["y"])

    def test_double_close_through_alias(self):
        rep = analyze(program([Open("x"), Alias("y", "x"),
                               Close("x"), Close("y")]))
        self.assertIn("double-close", kinds(rep))


class TestFunctions(unittest.TestCase):
    def test_cross_function_close(self):
        shut = Function("shut", ("h",), [Close("h")])
        rep = analyze(program([Open("x"), Call("shut", ("x",)), Read("x")],
                              shut))
        self.assertIn("use-after-close", kinds(rep))

    def test_cross_function_move_transfer(self):
        take = Function("take", ("h",), [Move("g", "h"), Close("g")])
        rep = analyze(program([Open("x"), Call("take", ("x",)), Read("x")],
                              take))
        self.assertIn("use-after-move", kinds(rep))

    def test_return_transfers_handle(self):
        give = Function("give", (), [Open("g"), Return("g")])
        rep = analyze(program([Call("give", (), "x"), Close("x"), Read("x")],
                              give))
        self.assertIn("use-after-close", kinds(rep))

    def test_recursion_fixpoint_terminates(self):
        rec = Function("rec", ("h",),
                       [If([Close("h")], [Call("rec", ("h",))])])
        rep = analyze(program([Open("x"), Call("rec", ("x",)), Read("x")],
                              rec))
        self.assertEqual(rep.verdict, "UNSAFE")   # fixpoint reached, error kept
        self.assertIn("use-after-close", kinds(rep))

    def test_recursion_safe_program(self):
        rec = Function("rec", ("h",),
                       [If([Read("h")], [Call("rec", ("h",))])])
        rep = analyze(program([Open("x"), Call("rec", ("x",)), Close("x")],
                              rec))
        self.assertEqual(rep.verdict, "SAFE")


class TestLoops(unittest.TestCase):
    def test_loop_fixpoint_safe(self):
        rep = analyze(program([Open("x"), While([Write("x")]), Close("x")]))
        self.assertEqual(rep.verdict, "SAFE")

    def test_loop_double_close(self):
        rep = analyze(program([Open("x"), While([Close("x")])]))
        self.assertIn("double-close", kinds(rep))

    def test_alias_through_loop(self):
        # alias created in a loop still shares the same identity
        rep = analyze(program([Open("x"), While([Alias("y", "x")]),
                               Close("y"), Read("x")]))
        self.assertIn("use-after-close", kinds(rep))


class TestPending(unittest.TestCase):
    def test_step_budget_stays_pending(self):
        prog = program([Open("x"), Close("x"), Read("x")])
        rep = analyze(prog, max_steps=1)
        self.assertEqual(rep.verdict, "PENDING")
        self.assertIsNotNone(rep.pending_reason)

    def test_loop_budget_stays_pending(self):
        # instance_cap=0 forces the loop fixpoint to keep growing
        prog = program([While([Open("x")])])
        rep = analyze(prog, max_loop_iters=3, instance_cap=4)
        self.assertEqual(rep.verdict, "PENDING")

    def test_pending_never_claims_safe(self):
        prog = program([Open("x"), Close("x"), Read("x")])
        rep = analyze(prog, max_steps=2)
        self.assertNotEqual(rep.verdict, "SAFE")


class TestCounterexamples(unittest.TestCase):
    def test_every_error_concretizes_and_replays(self):
        progs = [
            program([Open("x"), Close("x"), Read("x")]),
            program([Open("x"), If([Close("x")], []), Read("x")]),
            program([Open("x"), BorrowShared("x", "r"), Write("x")]),
            program([Open("x"), Move("y", "x"), Read("x")]),
            program([Open("x"), Alias("y", "x"), Close("y"), Read("x")]),
            program([Open("x"), While([Close("x")])]),
        ]
        for prog in progs:
            rep = analyze(prog)
            self.assertEqual(rep.verdict, "UNSAFE")
            confirm(prog, rep)
            for e in rep.errors:
                self.assertIsNotNone(e.concrete_trace, e.kind)
                kind, snap = replay(prog, e.concrete_trace)
                self.assertEqual(kind, e.kind)
                self.assertIn("res", snap)   # concretizable state available


class TestEnumeration(unittest.TestCase):
    def test_small_programs_sound_and_complete(self):
        failures = run_check(max_len=3, with_if=True, verbose=False)
        self.assertEqual(failures, [])


if __name__ == "__main__":
    unittest.main()
