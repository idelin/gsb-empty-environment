"""unittest suite for the offline audio/control scheduler.

All verification is done offline against local fixtures; no audio device is
touched.  The complex-interleave test verifies the engine sample-by-sample
against an independent per-sample reference implementation within the
declared numeric tolerance.
"""

from __future__ import annotations

import math
import unittest

from audiosched.engine import Engine, MASTER
from audiosched.events import filter_event, gain_event, route_event
from audiosched.fixtures import (BLOCK_SIZE, DECLARED_LATENCY_SAMPLES,
                                 NUMERIC_TOLERANCE, SAMPLE_RATE, constant,
                                 impulse_train, sine)
from audiosched.graph import Graph
from audiosched.scenarios import SCENARIOS, scenario_epoch_boundary, \
    scenario_interleave, scenario_late_correction, scenario_pause_resume, \
    scenario_transaction_cycle

TOTAL = BLOCK_SIZE * 8
TOL = NUMERIC_TOLERANCE


def flat(outputs):
    return [s for block in outputs for s in block.samples]


def applied_positions(audit, event_id):
    return [e["position"] for e in audit
            if e["type"] == "transaction_applied"
            and event_id in e["events"]]


class GraphTest(unittest.TestCase):
    def test_cycle_detection(self):
        g = Graph(["a", "b", "c"])
        g.add_edge("a", "b")
        g.add_edge("b", "c")
        self.assertFalse(g.has_cycle())
        g.add_edge("c", "a")
        self.assertTrue(g.has_cycle())
        g.remove_edge("c", "a")
        self.assertEqual(g.topological_order(), ["a", "b", "c"])

    def test_initial_cyclic_routing_rejected(self):
        with self.assertRaises(ValueError):
            Engine(SAMPLE_RATE, BLOCK_SIZE, {"t": constant(TOTAL, 1.0)},
                   [("t", MASTER), (MASTER, "t")])


class SegmentationTest(unittest.TestCase):
    def test_mid_block_event_cut_is_sample_exact(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.submit(gain_event("g", 0, 30, 1, "t", 0.5))
        out = flat(eng.render_blocks(2))
        for i in range(30):
            self.assertAlmostEqual(out[i], 1.0, delta=TOL)
        for i in range(30, 128):
            self.assertAlmostEqual(out[i], 0.5, delta=TOL)

    def test_block_boundary_event_fires_exactly_once(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.submit(gain_event("b", 0, BLOCK_SIZE, 1, "t", 0.25))
        eng.render_blocks(4)
        self.assertEqual(applied_positions(eng.audit, "b"), [BLOCK_SIZE])
        out = flat(eng.emitted)
        self.assertAlmostEqual(out[BLOCK_SIZE - 1], 1.0, delta=TOL)
        self.assertAlmostEqual(out[BLOCK_SIZE], 0.25, delta=TOL)


class TransactionTest(unittest.TestCase):
    def test_illegal_parameter_rolls_back_whole_transaction(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.submit(gain_event("ok", 0, 10, 1, "t", 0.5))
        eng.submit(filter_event("bad", 0, 10, 1, "t",
                                SAMPLE_RATE * 2.0))  # illegal cutoff
        eng.render_blocks(1)
        rejected = [e for e in eng.audit
                    if e["type"] == "transaction_rejected"]
        self.assertEqual(len(rejected), 1)
        self.assertEqual(set(rejected[0]["events"]), {"ok", "bad"})
        # Both events rolled back: gain and filter untouched.
        self.assertEqual(eng.gain["t"], 1.0)
        self.assertIsNone(eng.filters["t"].cutoff)
        self.assertTrue(all(abs(s - 1.0) <= TOL
                            for s in eng.emitted[0].samples))

    def test_negative_gain_is_illegal(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.submit(gain_event("neg", 0, 5, 1, "t", -0.1))
        eng.render_blocks(1)
        self.assertEqual(applied_positions(eng.audit, "neg"), [])

    def test_same_sample_route_swap_has_no_transient_cycle(self):
        result = scenario_transaction_cycle()
        audit = result["audit"]
        # The swap transaction commits: final graph is acyclic and the block
        # never passes through an illegal intermediate routing.
        self.assertEqual(applied_positions(audit, "swap1"), [96])
        self.assertEqual(applied_positions(audit, "swap2"), [96])
        self.assertEqual(applied_positions(audit, "swap3"), [96])
        # The later cyclic connect is rejected and rolled back.
        self.assertEqual(applied_positions(audit, "bad1"), [])
        rejected = [e for e in audit if e["type"] == "transaction_rejected"]
        self.assertEqual(len(rejected), 1)
        self.assertIn("cycle", rejected[0]["reason"])

    def test_cyclic_transaction_leaves_routing_unchanged(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"a": constant(TOTAL, 1.0), "b": constant(TOTAL, 2.0)},
                     [("a", "b"), ("b", MASTER)])
        eng.submit(route_event("loop", 0, 10, 1, "connect", "b", "a"))
        eng.render_blocks(1)
        self.assertFalse(eng.graph.has_edge("b", "a"))
        self.assertTrue(eng.graph.has_edge("a", "b"))
        self.assertFalse(eng.routing_has_cycle())


class EpochTest(unittest.TestCase):
    def test_old_epoch_events_never_fire(self):
        result = scenario_epoch_boundary()
        audit = result["audit"]
        quarantined = {e["event_id"] for e in audit
                       if e["type"] == "quarantined"}
        self.assertIn("old1", quarantined)    # dropped at epoch switch
        self.assertIn("stale", quarantined)   # foreign epoch on submit
        self.assertEqual(applied_positions(audit, "old1"), [])
        self.assertEqual(applied_positions(audit, "stale"), [])
        # New-epoch boundary event fires exactly once, at sample 64.
        self.assertEqual(applied_positions(audit, "new1"), [64])

    def test_epoch_change_resets_output_stream(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.render_blocks(2)
        eng.set_epoch(7)
        self.assertEqual(eng.clock, 0)
        self.assertEqual(eng.emitted, [])
        self.assertEqual(eng.confirmed, set())


class LateCorrectionTest(unittest.TestCase):
    def test_recomputes_unconfirmed_output(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.submit(gain_event("c", 0, 100, 1, "t", 0.5))
        eng.render_blocks(4)
        self.assertEqual(eng.submit(gain_event("c", 0, 70, 2, "t", 0.25)),
                         "rescheduled")
        eng.render_blocks(3)
        ids = [b.output_id for b in eng.emitted]
        self.assertEqual(len(ids), len(set(ids)))  # no duplicates
        block1 = next(b for b in eng.emitted if b.output_id == (0, 64))
        for i in range(6):          # samples 64..69 keep old gain
            self.assertAlmostEqual(block1.samples[i], 1.0, delta=TOL)
        for i in range(6, 64):      # sample 70 onward uses corrected gain
            self.assertAlmostEqual(block1.samples[i], 0.25, delta=TOL)

    def test_confirmed_output_yields_audit_conflict(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.render_blocks(2)
        self.assertTrue(eng.confirm((0, 0)))
        before = list(eng.emitted[0].samples)
        self.assertEqual(eng.submit(gain_event("late", 0, 10, 1, "t", 0.1)),
                         "conflict")
        conflicts = [e for e in eng.audit if e["type"] == "conflict"]
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(conflicts[0]["output_id"], (0, 0))
        self.assertEqual(eng.gain["t"], 1.0)            # untouched
        self.assertEqual(eng.emitted[0].samples, before)  # untouched

    def test_stale_version_ignored(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.submit(gain_event("v", 0, 10, 2, "t", 0.5))
        self.assertEqual(eng.submit(gain_event("v", 0, 10, 1, "t", 0.9)),
                         "stale")
        eng.render_blocks(1)
        self.assertAlmostEqual(eng.gain["t"], 0.5, delta=TOL)

    def test_scenario_late_correction_conflict_path(self):
        result = scenario_late_correction()
        kinds = [e["type"] for e in result["audit"]]
        self.assertIn("late_correction", kinds)
        self.assertIn("conflict", kinds)


class CheckpointTest(unittest.TestCase):
    def test_checkpoint_restore_reproduces_output(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": sine(TOTAL, 1000.0)}, [("t", MASTER)])
        eng.submit(gain_event("g", 0, 200, 1, "t", 0.5))
        eng.render_blocks(2)
        cid = eng.checkpoint("mid")
        first = flat(eng.render_blocks(2))
        eng.restore(cid)
        self.assertEqual(eng.clock, 2 * BLOCK_SIZE)
        second = flat(eng.render_blocks(2))
        self.assertEqual(len(first), len(second))
        for a, b in zip(first, second):
            self.assertAlmostEqual(a, b, delta=TOL)


class PauseResumeTest(unittest.TestCase):
    def test_pause_blocks_rendering_and_resume_dedups_by_output_id(self):
        result = scenario_pause_resume()
        self.assertIsNone(result["extra"]["paused_render"])
        ids = [b.output_id for b in result["outputs"]]
        self.assertEqual(len(ids), len(set(ids)))
        self.assertEqual(ids.count((0, 0)), 1)  # confirmed block not replayed
        self.assertEqual(ids, [(0, 0), (0, 64), (0, 128), (0, 192)])

    def test_paused_engine_renders_nothing(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)])
        eng.pause()
        self.assertIsNone(eng.render_block())
        self.assertEqual(eng.clock, 0)
        eng.resume()
        self.assertIsNotNone(eng.render_block())


class DeclarationsTest(unittest.TestCase):
    def test_latency_and_tolerance_declared(self):
        eng = Engine(SAMPLE_RATE, BLOCK_SIZE,
                     {"t": constant(TOTAL, 1.0)}, [("t", MASTER)],
                     latency_samples=DECLARED_LATENCY_SAMPLES,
                     tolerance=NUMERIC_TOLERANCE)
        self.assertEqual(eng.latency_samples, BLOCK_SIZE)
        self.assertEqual(eng.tolerance, 1e-9)
        self.assertEqual(DECLARED_LATENCY_SAMPLES, BLOCK_SIZE)


class PerSampleReferenceTest(unittest.TestCase):
    """Direct per-sample verification of the complex interleave scenario
    against an independent reference implementation (no engine code)."""

    @staticmethod
    def reference(total):
        drums = impulse_train(total, 16, 0.9)
        bass = constant(total, 0.5)
        lead = sine(total, 440.0, SAMPLE_RATE, 0.25)

        gain = {"drums": 1.0, "bass": 1.0, "lead": 1.0}
        cutoff = {"drums": None, "bass": None, "lead": None}
        zstate = {"drums": 0.0, "bass": 0.0, "lead": 0.0}
        bass_routed = True

        # Committed schedule of scenario_interleave, written out by hand.
        schedule = {
            30: [("gain", "bass", 0.2), ("filter", "lead", 2000.0)],
            64: [("gain", "drums", 0.5)],
            100: [("unroute_bass",), ("gain", "lead", 2.0)],
            150: [("filter", "drums", 800.0)],
        }

        def alpha(fc):
            return 1.0 - math.exp(-2.0 * math.pi * fc / SAMPLE_RATE)

        out = []
        for i in range(total):
            for action in schedule.get(i, []):
                if action[0] == "gain":
                    gain[action[1]] = action[2]
                elif action[0] == "filter":
                    cutoff[action[1]] = action[2]
                elif action[0] == "unroute_bass":
                    bass_routed = False
            sample = 0.0
            for name, src in (("drums", drums), ("bass", bass),
                              ("lead", lead)):
                x = src[i]
                fc = cutoff[name]
                y = x if fc is None else (
                    zstate.__setitem__(
                        name, zstate[name] + alpha(fc)
                        * (x - zstate[name])) or zstate[name])
                if name == "bass" and not bass_routed:
                    continue
                sample += y * gain[name]
            out.append(sample)
        return out

    def test_interleave_matches_reference_sample_by_sample(self):
        result = scenario_interleave()
        engine_out = flat(result["outputs"])
        reference = self.reference(len(engine_out))
        self.assertEqual(len(engine_out), len(reference))
        worst = 0.0
        for i, (got, want) in enumerate(zip(engine_out, reference)):
            worst = max(worst, abs(got - want))
            self.assertAlmostEqual(
                got, want, delta=TOL,
                msg=f"sample {i}: got {got!r}, want {want!r}")
        self.assertLessEqual(worst, TOL)


class ReplayTest(unittest.TestCase):
    PINNED_DIGESTS = {
        "interleave": "102154ec811775d21dbe0ac9e22c13b5500e58e65a982b68320218ab9be47015",
        "transaction_cycle": "4fa9fcd6656ac85b7ab58cf4f8042faf62a2fb7d9d8d01593dcf3701a4b6536f",
        "epoch_boundary": "3ac3cb332841e600d3b13bc07fee541fbe0923270be12a92a920fe5b620ef4db",
        "late_correction": "5c395069f9093a25b175279220f066563a2d6ee671599ed58db8df9e45ffd554",
        "pause_resume": "d4a9002f4814531826e26718fa6440def9857327f2baed79628cccd7d7d9e346",
    }

    def test_scenarios_are_deterministic(self):
        for name, fn in SCENARIOS.items():
            self.assertEqual(fn()["digest"], fn()["digest"], name)

    def test_scenario_digests_are_pinned(self):
        for name, digest in self.PINNED_DIGESTS.items():
            self.assertEqual(SCENARIOS[name]()["digest"], digest, name)


if __name__ == "__main__":
    unittest.main()
