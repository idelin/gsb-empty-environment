import unittest

import concrete as C
import samples
from checker import (
    DEFINITE,
    PENDING,
    POSSIBLE,
    VERIFIED,
    VIOLATED,
    check,
    soundness_check,
)
from model import Acquire, Borrow, Edge, Node, Nop, Program


class VerdictTests(unittest.TestCase):
    def test_ok_program_verified(self):
        rep = check(samples.ok_with_finally())
        self.assertEqual(rep.verdict, VERIFIED)
        self.assertEqual(rep.findings, [])

    def test_alias_release_verified(self):
        rep = check(samples.alias_ok())
        self.assertEqual(rep.verdict, VERIFIED)
        self.assertEqual(rep.findings, [])

    def test_cancel_leak_definite(self):
        rep = check(samples.cancel_leak())
        self.assertEqual(rep.verdict, VIOLATED)
        leaks = [f for f in rep.findings if f.kind == C.LEAK]
        self.assertEqual(len(leaks), 1)
        self.assertEqual(leaks[0].certainty, DEFINITE)
        self.assertEqual(leaks[0].node, "exit_cancel")

    def test_double_release_definite(self):
        rep = check(samples.double_release())
        self.assertEqual(rep.verdict, VIOLATED)
        kinds = {f.kind for f in rep.findings if f.certainty == DEFINITE}
        self.assertIn(C.DOUBLE_RELEASE, kinds)

    def test_release_after_move_definite(self):
        rep = check(samples.moved_release())
        self.assertEqual(rep.verdict, VIOLATED)
        kinds = {f.kind for f in rep.findings if f.certainty == DEFINITE}
        self.assertIn(C.RELEASE_AFTER_MOVE, kinds)

    def test_release_while_borrowed_definite(self):
        rep = check(samples.borrow_release())
        self.assertEqual(rep.verdict, VIOLATED)
        kinds = {f.kind for f in rep.findings if f.certainty == DEFINITE}
        self.assertIn(C.RELEASE_WHILE_BORROWED, kinds)

    def test_false_positives_filtered_by_replay(self):
        rep = check(samples.guarded_false_positive())
        self.assertEqual(rep.verdict, VERIFIED)
        self.assertEqual(rep.findings, [])
        kinds = {c.kind for c in rep.filtered}
        self.assertIn(C.LEAK, kinds)
        self.assertIn(C.RELEASE_WITHOUT_ACQUIRE, kinds)

    def test_pending_when_search_incomplete(self):
        # A tiny replay bound cannot reach the leak witness: the result
        # must stay pending (possible leak), never verified.
        rep = check(samples.cancel_leak(), max_replay_states=2, max_replay_steps=2)
        self.assertEqual(rep.verdict, PENDING)
        poss = [f for f in rep.findings if f.certainty == POSSIBLE]
        self.assertTrue(poss)
        self.assertTrue(rep.pending_reasons)


class WitnessReplayTests(unittest.TestCase):
    """Counterexamples must be replayable concrete executions."""

    def _check_witness(self, prog, finding):
        self.assertIsNotNone(finding.trace)
        node_name, cs = C.replay(prog, finding.trace)
        self.assertEqual(node_name, finding.node)
        return node_name, cs

    def test_leak_witness_replays_to_held_resource(self):
        prog = samples.cancel_leak()
        rep = check(prog)
        leak = next(f for f in rep.findings if f.kind == C.LEAK)
        _n, cs = self._check_witness(prog, leak)
        self.assertIn("file", cs.held_map())

    def test_error_witness_replays_to_error_state(self):
        prog = samples.moved_release()
        rep = check(prog)
        f = next(f for f in rep.findings if f.kind == C.RELEASE_AFTER_MOVE)
        _n, cs = self._check_witness(prog, f)
        err = C.apply_op(prog, prog.nodes[f.node], cs)[1]
        self.assertEqual(err, (C.RELEASE_AFTER_MOVE, "file"))

    def test_double_release_witness_replays(self):
        prog = samples.double_release()
        rep = check(prog)
        f = next(f for f in rep.findings if f.kind == C.DOUBLE_RELEASE)
        _n, cs = self._check_witness(prog, f)
        err = C.apply_op(prog, prog.nodes[f.node], cs)[1]
        self.assertEqual(err, (C.DOUBLE_RELEASE, "file"))


class OwnershipSemanticsTests(unittest.TestCase):
    """Ownership is explicit per owner; no global reference counting."""

    def test_transfer_moves_ownership(self):
        prog = samples.ok_with_finally()
        # after handoff the logger owns the file, main does not
        violations, complete, _ = C.explore(prog)
        self.assertTrue(complete)
        self.assertEqual(violations, [])

    def test_unreturned_borrow_at_exit(self):
        nodes = [
            Node("entry", Nop(), [Edge("acquire")]),
            Node("acquire", Acquire("file", "main"), [Edge("borrow")]),
            Node("borrow", Borrow("file", "main", "reader"), [Edge("exit_normal")]),
            Node("exit_normal", Nop(), [], exit_kind="normal"),
        ]
        prog = Program("borrow_leak", {n.name: n for n in nodes}, "entry",
                       ("file",), ("main", "reader"))
        rep = check(prog)
        self.assertEqual(rep.verdict, VIOLATED)
        kinds = {f.kind for f in rep.findings if f.certainty == DEFINITE}
        self.assertIn(C.LEAK, kinds)
        self.assertIn(C.UNRETURNED_BORROW, kinds)


class SoundnessTests(unittest.TestCase):
    """Exhaustively enumerate small programs and require that the abstract
    analysis flags every concrete violation (no false negatives)."""

    def test_all_samples_sound(self):
        for build in samples.ALL:
            with self.subTest(program=build.__name__):
                prog = build()
                missing, complete = soundness_check(prog)
                self.assertTrue(complete, "exhaustive search must finish")
                self.assertEqual(missing, [])


if __name__ == "__main__":
    unittest.main()
