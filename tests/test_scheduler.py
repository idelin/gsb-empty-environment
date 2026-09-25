"""Unittest suite for the unified audio/control scheduler (stdlib only)."""
from __future__ import annotations

import unittest

from audiosched.events import FILTER, GAIN, ROUTE, ControlEvent
from audiosched.fixtures import (BLOCK_SIZE, DECLARED_LATENCY_SECONDS,
                                 MASTER, SAMPLE_RATE, TRACKS, TOLERANCE,
                                 make_sources)
from audiosched.graph import RoutingGraph
from audiosched.reference import reference_render
from audiosched.replay import SCENARIOS, build_interleave_events
from audiosched.scheduler import MAX_GAIN, Scheduler


def make_scheduler():
    return Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, make_sources(), MASTER)


def render_n(sched, n):
    out = []
    for _ in range(n):
        out.extend(sched.render_block().samples)
    return out


def assert_matches(testcase, actual, expected):
    testcase.assertEqual(len(actual), len(expected))
    for i, (a, b) in enumerate(zip(actual, expected)):
        if abs(a - b) > TOLERANCE:
            testcase.fail(f"sample {i}: {a} != {b} (tol {TOLERANCE})")


class TestDeclarations(unittest.TestCase):
    def test_latency_and_tolerance_declared(self):
        self.assertAlmostEqual(DECLARED_LATENCY_SECONDS,
                               BLOCK_SIZE / SAMPLE_RATE)
        self.assertGreater(TOLERANCE, 0.0)


class TestRoutingGraph(unittest.TestCase):
    def test_cycle_detection(self):
        g = RoutingGraph(["a", "b", "c"])
        g.connect("a", "b")
        g.connect("b", "c")
        self.assertFalse(g.has_cycle())
        g.connect("c", "a")
        self.assertTrue(g.has_cycle())
        with self.assertRaises(ValueError):
            g.topo_order()

    def test_self_loop_is_a_cycle(self):
        g = RoutingGraph(["a"])
        g.connect("a", "a")
        self.assertTrue(g.has_cycle())


class TestTransactions(unittest.TestCase):
    def test_invalid_param_rolls_back_whole_transaction(self):
        sched = make_scheduler()
        pos = 50
        sched.submit(ControlEvent("ok-filter", 0, pos, 1, FILTER,
                                  {"track": "a", "cutoff": 900.0}))
        sched.submit(ControlEvent("bad-gain", 0, pos, 1, GAIN,
                                  {"track": "b", "gain": -0.5}))
        sched.render_block()
        outcomes = {eid: oc for eid, _, _, oc in sched.applied_log}
        self.assertEqual(outcomes["ok-filter"], "rolled_back")
        self.assertEqual(outcomes["bad-gain"], "rolled_back")
        rollbacks = [a for a in sched.audit_log if a.kind == "rollback"]
        self.assertEqual(len(rollbacks), 1)
        # Nothing from the transaction became visible.
        self.assertEqual(sched._states["a"].cutoff, SAMPLE_RATE / 2.0)
        self.assertEqual(sched._states["b"].gain, 1.0)

    def test_gain_bounds(self):
        sched = make_scheduler()
        sched.submit(ControlEvent("too-big", 0, 10, 1, GAIN,
                                  {"track": "a", "gain": MAX_GAIN + 1}))
        sched.render_block()
        outcomes = {eid: oc for eid, _, _, oc in sched.applied_log}
        self.assertEqual(outcomes["too-big"], "rolled_back")

    def test_same_sample_route_swap_has_no_transient_ring(self):
        sources = make_sources()
        sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
        p_swap, p_ring = 40, 200
        events = [
            ControlEvent("pre", 0, 10, 1, ROUTE,
                         {"op": "connect", "src": "a", "dst": "b"}),
            # a->b becomes b->a atomically; a naive connect-first order
            # would ring a<->b, but only the final DAG is ever exposed.
            ControlEvent("sw1", 0, p_swap, 1, ROUTE,
                         {"op": "connect", "src": "b", "dst": "a"}),
            ControlEvent("sw2", 0, p_swap, 1, ROUTE,
                         {"op": "disconnect", "src": "a", "dst": "b"}),
            # Final graph is a genuine ring -> whole transaction rejected.
            ControlEvent("bad1", 0, p_ring, 1, ROUTE,
                         {"op": "connect", "src": "a", "dst": "b"}),
            ControlEvent("bad2", 0, p_ring, 1, ROUTE,
                         {"op": "disconnect", "src": "b", "dst": "master"}),
        ]
        for e in events:
            sched.submit(e)
        rendered = render_n(sched, 3)
        outcomes = {eid: oc for eid, _, _, oc in sched.applied_log}
        self.assertEqual(outcomes["sw1"], "committed")
        self.assertEqual(outcomes["sw2"], "committed")
        self.assertEqual(outcomes["bad1"], "rolled_back")
        self.assertEqual(outcomes["bad2"], "rolled_back")
        expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                    events, BLOCK_SIZE * 3)
        assert_matches(self, rendered, expected)


class TestEpochAndVersion(unittest.TestCase):
    def test_old_epoch_events_never_fire(self):
        sched = make_scheduler()
        sched.submit(ControlEvent("old", 0, 200, 1, GAIN,
                                  {"track": "a", "gain": 0.1}))
        sched.render_block()
        sched.set_epoch(1)
        self.assertFalse(sched.submit(ControlEvent(
            "old-late", 0, 300, 1, GAIN, {"track": "b", "gain": 0.1})))
        sched.submit(ControlEvent("new", 1, 300, 1, GAIN,
                                  {"track": "b", "gain": 0.5}))
        render_n(sched, 2)
        fired = {eid for eid, _, _, oc in sched.applied_log
                 if oc == "committed"}
        self.assertNotIn("old", fired)
        self.assertNotIn("old-late", fired)
        self.assertIn("new", fired)
        drops = [a for a in sched.audit_log if a.kind == "epoch_drop"]
        self.assertEqual(len(drops), 2)

    def test_stale_version_ignored(self):
        sched = make_scheduler()
        self.assertTrue(sched.submit(ControlEvent(
            "e", 0, 10, 2, GAIN, {"track": "a", "gain": 0.5})))
        self.assertFalse(sched.submit(ControlEvent(
            "e", 0, 10, 1, GAIN, {"track": "a", "gain": 0.9})))  # stale
        self.assertTrue(sched.submit(ControlEvent(
            "e", 0, 10, 3, GAIN, {"track": "a", "gain": 0.25})))  # newer
        sched.render_block()
        committed = [(eid, v) for eid, v, _, oc in sched.applied_log
                     if oc == "committed"]
        self.assertEqual(committed, [("e", 3)])


class TestBoundaryEvent(unittest.TestCase):
    def test_boundary_event_fires_exactly_once(self):
        sources = make_sources()
        sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
        events = [ControlEvent("edge", 0, BLOCK_SIZE, 1, GAIN,
                               {"track": "a", "gain": 0.5})]
        for e in events:
            sched.submit(e)
        rendered = render_n(sched, 3)
        fires = [eid for eid, _, _, oc in sched.applied_log
                 if oc == "committed"]
        self.assertEqual(fires.count("edge"), 1)
        expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                    events, BLOCK_SIZE * 3)
        assert_matches(self, rendered, expected)


class TestLateCorrection(unittest.TestCase):
    def test_confirmed_output_yields_audit_conflict(self):
        sched = make_scheduler()
        sched.submit(ControlEvent("g", 0, 10, 1, GAIN,
                                  {"track": "a", "gain": 1.5}))
        blk0 = sched.render_block()
        sched.confirm(blk0.output_id)
        ok = sched.submit(ControlEvent("g", 0, 10, 2, GAIN,
                                       {"track": "a", "gain": 0.25}))
        self.assertFalse(ok)
        conflicts = [a for a in sched.audit_log if a.kind == "conflict"]
        self.assertEqual(len(conflicts), 1)
        self.assertIn(blk0.output_id, conflicts[0].message)
        # Played output is untouched: no recompute happened.
        self.assertEqual(sched.position, BLOCK_SIZE)
        self.assertEqual(sched._states["a"].gain, 1.5)

    def test_unconfirmed_output_is_recomputed(self):
        sources = make_sources()
        sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
        sched.submit(ControlEvent("g", 0, 10, 1, GAIN,
                                  {"track": "a", "gain": 1.5}))
        blk0 = sched.render_block()
        blk1 = sched.render_block()
        sched.confirm(blk0.output_id)
        ok = sched.submit(ControlEvent("h", 0, 150, 1, GAIN,
                                       {"track": "b", "gain": 0.5}))
        self.assertTrue(ok)
        self.assertEqual(sched.position, 2 * BLOCK_SIZE)  # frontier kept
        events = [ControlEvent("g", 0, 10, 1, GAIN,
                               {"track": "a", "gain": 1.5}),
                  ControlEvent("h", 0, 150, 1, GAIN,
                               {"track": "b", "gain": 0.5})]
        expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                    events, BLOCK_SIZE * 2)
        pending = {o.output_id: o for o in sched.pending_outputs()}
        # Confirmed block 0 was not re-emitted; block 1 was recomputed.
        self.assertNotIn(blk0.output_id, pending)
        self.assertEqual(sched.emit_log.count(blk0.output_id), 1)
        assert_matches(self, list(pending[blk1.output_id].samples),
                       expected[BLOCK_SIZE:])


class TestPauseResume(unittest.TestCase):
    def test_pause_between_apply_and_confirm_resume_dedups(self):
        sources = make_sources()
        sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
        sched.submit(ControlEvent("g", 0, 10, 1, GAIN,
                                  {"track": "a", "gain": 1.5}))
        blk0 = sched.render_block()   # events applied, output pending
        sched.pause()                 # paused before confirm
        self.assertIsNone(sched.render_block())
        self.assertEqual(sched.position, BLOCK_SIZE)  # clock held
        # Late correction recomputes the paused, unconfirmed block.
        self.assertTrue(sched.submit(ControlEvent(
            "g", 0, 10, 2, GAIN, {"track": "a", "gain": 0.25})))
        sched.resume()
        blk1 = sched.render_block()
        self.assertIsNotNone(blk1)
        events = [ControlEvent("g", 0, 10, 2, GAIN,
                               {"track": "a", "gain": 0.25})]
        expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                    events, BLOCK_SIZE * 2)
        pending = {o.output_id: o for o in sched.pending_outputs()}
        assert_matches(self, list(pending[blk0.output_id].samples),
                       expected[:BLOCK_SIZE])
        assert_matches(self, list(blk1.samples), expected[BLOCK_SIZE:])
        ids = [o.output_id for o in sched.pending_outputs()]
        self.assertEqual(len(ids), len(set(ids)))  # deduped by output id


class TestCheckpoint(unittest.TestCase):
    def test_restore_reproduces_output(self):
        sched = make_scheduler()
        sched.render_block()
        sched.checkpoint("cp")
        sched.submit(ControlEvent("g", 0, 200, 1, GAIN,
                                  {"track": "a", "gain": 0.5}))
        first = sched.render_block().samples
        sched.restore("cp")
        second = sched.render_block().samples
        self.assertEqual(first, second)
        self.assertEqual(sched.position, 2 * BLOCK_SIZE)


class TestComplexInterleave(unittest.TestCase):
    def test_matches_per_sample_reference(self):
        sources = make_sources()
        sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
        events = build_interleave_events()
        for e in events:
            sched.submit(e)
        rendered = render_n(sched, 6)
        expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                    events, BLOCK_SIZE * 6)
        assert_matches(self, rendered, expected)


class TestReplayScenarios(unittest.TestCase):
    def test_all_scenarios_pass_their_checks(self):
        for name, fn in SCENARIOS.items():
            with self.subTest(scenario=name):
                result = fn()
                for check, ok in result["checks"].items():
                    self.assertTrue(ok, f"{name}.{check} failed")


if __name__ == "__main__":
    unittest.main()
