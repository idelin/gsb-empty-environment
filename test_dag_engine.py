import unittest

from dag_engine import (
    Edge,
    Engine,
    GraphUpdate,
    UpdateRejected,
    Vertex,
    VertexState,
)


def V(vid, **kw):
    return Vertex(vid=vid, **kw)


class SimulatedCrash(Exception):
    pass


def started_ids(engine):
    return [a for _, a, _ in engine.started_log]


def unlocks_of(engine, vid):
    return [t for v, t, _ in engine.unlocked_log if v == vid]


class CycleRejectionTest(unittest.TestCase):
    """A->B->C: after A ran, inserting C->B is rejected and the plan is kept."""

    def setUp(self):
        self.eng = Engine(quotas={"cpu": 2})
        self.eng.apply_update(GraphUpdate(
            add_vertices=[
                V("A", duration=1, resources={"cpu": 1}),
                V("B", duration=1, resources={"cpu": 1}),
                V("C", duration=1, resources={"cpu": 1}),
            ],
            add_edges=[Edge("e_ab", "A", "B"), Edge("e_bc", "B", "C")],
        ))
        self.eng.settle()

    def test_cycle_insert_rejected_and_plan_preserved(self):
        eng = self.eng
        plan_before = eng.levels()
        version_before = eng.version
        edges_before = set(eng.graph.edges)

        eng.advance_to(1)  # A done at t=1, B starts
        self.assertEqual(eng.state["A"], VertexState.SUCCEEDED)
        self.assertEqual(eng.state["B"], VertexState.RUNNING)

        with self.assertRaises(UpdateRejected) as ctx:
            eng.apply_update(GraphUpdate(add_edges=[Edge("e_cb", "C", "B")]))
        reasons = " ".join(ctx.exception.reasons)
        self.assertIn("cycle detected", reasons)
        self.assertIn("B -> C -> B", reasons)
        self.assertIn("already started", reasons)  # B is running

        # Original plan fully preserved.
        self.assertEqual(eng.version, version_before)
        self.assertEqual(eng.levels(), plan_before)
        self.assertEqual(eng.levels(), [["A"], ["B"], ["C"]])
        self.assertEqual(set(eng.graph.edges), edges_before)
        self.assertEqual(eng.state["B"], VertexState.RUNNING)

        eng.run_to_completion()
        self.assertEqual(
            {v: eng.state[v] for v in ("A", "B", "C")},
            {v: VertexState.SUCCEEDED for v in ("A", "B", "C")},
        )
        self.assertEqual(started_ids(eng), ["A#1", "B#1", "C#1"])
        self.assertEqual(len(unlocks_of(eng, "C")), 1)


class LateFailureAndIndependentBranchTest(unittest.TestCase):
    """B's stale attempt results are ignored; D->E branch is never disturbed."""

    def test_stale_attempts_and_branch_isolation(self):
        eng = Engine(quotas={"cpu": 8})
        eng.apply_update(GraphUpdate(
            add_vertices=[
                V("A", duration=1, resources={"cpu": 1}),
                V("B", duration=2, resources={"cpu": 1}),
                V("C", duration=1, resources={"cpu": 1}),
                V("D", duration=3, resources={"cpu": 1}),
                V("E", duration=1, resources={"cpu": 1}),
            ],
            add_edges=[
                Edge("e_ab", "A", "B"),
                Edge("e_bc", "B", "C"),
                Edge("e_de", "D", "E"),
            ],
        ))
        eng.settle()
        self.assertEqual(started_ids(eng), ["A#1", "D#1"])
        eng.set_outcome("B", 1, "fail")  # B#1 will report a late failure

        eng.advance_to(1)  # A done -> B#1 starts (reports at t=3); D running
        self.assertEqual(started_ids(eng)[-1], "B#1")

        eng.cancel("B")    # cancel running B#1, quota released
        eng.retry("B")     # cancelled attempts stay put until retried
        eng.settle()       # retry: B#2 starts at t=1
        self.assertEqual(started_ids(eng)[-1], "B#2")

        # Mid-run graph update on the A->B->C branch only.
        eng.advance_to(2)
        affected = eng.apply_update(GraphUpdate(
            add_vertices=[V("X", duration=1, resources={"cpu": 1})],
            add_edges=[Edge("e_bx", "B", "X")],
        ))
        self.assertEqual(affected, {"B", "X"})
        eng.settle()       # B#2 invalidated by the update; B#3 starts at t=2
        self.assertEqual(started_ids(eng)[-1], "B#3")

        eng.advance_to(3)  # stale B#1 fail + stale B#2 ok arrive; D done -> E
        stale_ids = [a for _, a, _, _ in eng.stale_log]
        self.assertIn("B#1", stale_ids)
        self.assertIn("B#2", stale_ids)
        self.assertEqual(eng.state["B"], VertexState.RUNNING)  # B#3 still live
        self.assertEqual(eng.state["D"], VertexState.SUCCEEDED)
        self.assertEqual(eng.state["E"], VertexState.RUNNING)

        eng.run_to_completion()

        # C unlocked exactly once, by the current attempt B#3.
        self.assertEqual(unlocks_of(eng, "C"), [4])
        self.assertEqual(started_ids(eng).count("C#1"), 1)
        self.assertEqual(eng.state["B"], VertexState.SUCCEEDED)

        # Independent D->E branch: single attempts, never re-planned.
        self.assertIn("D#1", started_ids(eng))
        self.assertIn("E#1", started_ids(eng))
        self.assertNotIn("D#2", started_ids(eng))
        replanned_vids = [v for _, v in eng.replanned_log]
        self.assertNotIn("D", replanned_vids)
        self.assertNotIn("E", replanned_vids)
        self.assertIn("B", replanned_vids)

        for vid in ("A", "B", "C", "D", "E", "X"):
            self.assertEqual(eng.state[vid], VertexState.SUCCEEDED, vid)


class CancelQuotaAndCheckpointTest(unittest.TestCase):
    """Cancel releases quota; checkpoint resume skips confirmed side effects."""

    def test_cancel_releases_quota(self):
        eng = Engine(quotas={"cpu": 2})
        eng.apply_update(GraphUpdate(add_vertices=[
            V("P", duration=2, resources={"cpu": 2}),
            V("Q", duration=1, resources={"cpu": 1}),
        ]))
        eng.settle()
        self.assertEqual(started_ids(eng), ["P#1"])
        self.assertIn("cpu", eng.blocking_report()["Q"])  # blocking evidence

        eng.cancel("P")
        self.assertEqual(eng.used.get("cpu", 0.0), 0.0)  # quota released
        eng.settle()
        self.assertEqual(started_ids(eng), ["P#1", "Q#1"])
        self.assertEqual(eng.state["P"], VertexState.CANCELLED)
        self.assertIn("cancelled", eng.blocking_report()["P"])

        eng.retry("P")
        eng.settle()       # P still waits: Q holds 1 of 2 cpu
        self.assertIn("cpu", eng.blocking_report()["P"])

        eng.advance_to(1)  # Q done at t=1 -> P retries with freed quota
        self.assertEqual(started_ids(eng), ["P#1", "Q#1", "P#2"])
        eng.advance_to(3)
        self.assertEqual(eng.state["P"], VertexState.SUCCEEDED)
        self.assertEqual(eng.used.get("cpu", 0.0), 0.0)

    def test_checkpoint_resume_skips_confirmed_effects(self):
        eng = Engine(quotas={"cpu": 1})
        eng.apply_update(GraphUpdate(add_vertices=[
            V("X", duration=1, resources={"cpu": 1}, effects=("charge",)),
        ]))
        eng.settle()
        self.assertEqual(started_ids(eng), ["X#1"])

        captured = {}

        def crash_hook(vid):
            captured["cp"] = eng.checkpoint()
            raise SimulatedCrash()

        eng.hooks.before_state_commit = crash_hook
        with self.assertRaises(SimulatedCrash):
            eng.advance_to(1)  # effect confirmed, crash before state commit
        self.assertEqual(eng.effects_executed, ["charge"])

        eng.restore(captured["cp"])
        eng.hooks.before_state_commit = None
        self.assertEqual(eng.state["X"], VertexState.PENDING)  # attempt lost
        self.assertEqual(eng.used, {})

        eng.settle()
        self.assertEqual(started_ids(eng), ["X#1", "X#2"])
        eng.advance_to(2)
        self.assertEqual(eng.state["X"], VertexState.SUCCEEDED)
        # Confirmed side effect not re-executed after resume.
        self.assertEqual(eng.effects_executed, ["charge"])
        self.assertEqual(eng.effects_skipped, ["charge"])


class SimultaneousUpdateAndCompletionTest(unittest.TestCase):
    """Graph change and completion at the same timestamp, both orderings."""

    def _engine(self):
        eng = Engine(quotas={"cpu": 4})
        eng.apply_update(GraphUpdate(
            add_vertices=[
                V("F", duration=2, resources={"cpu": 1}),
                V("G", duration=1, resources={"cpu": 1}),
                V("M", duration=4, resources={"cpu": 1}),
                V("N", duration=1, resources={"cpu": 1}),
            ],
            add_edges=[Edge("e_fg", "F", "G"), Edge("e_mn", "M", "N")],
        ))
        return eng

    def _update(self):
        return GraphUpdate(
            add_vertices=[V("K", duration=1, resources={"cpu": 1})],
            add_edges=[Edge("e_fk", "F", "K")],
        )

    def test_update_sequenced_before_completion(self):
        eng = self._engine()
        eng.schedule_update(2, self._update())  # seq before completion events
        eng.settle()                            # F#1 (ends t=2), M#1 start
        eng.advance_to(2)
        # Update invalidated running F#1; its same-instant completion is stale.
        self.assertIn("F#1", [a for _, a, _, _ in eng.stale_log])
        self.assertEqual(eng.state["F"], VertexState.RUNNING)  # F#2 re-planned
        self.assertEqual(started_ids(eng)[-1], "F#2")
        # Stale F#1 result unlocked nothing in the new graph.
        self.assertEqual(unlocks_of(eng, "G"), [])
        self.assertEqual(unlocks_of(eng, "K"), [])

        eng.run_to_completion()
        self.assertEqual(unlocks_of(eng, "G"), [4])
        self.assertEqual(unlocks_of(eng, "K"), [4])
        self.assertEqual(started_ids(eng).count("M#1"), 1)
        self.assertEqual(started_ids(eng).count("N#1"), 1)
        replanned_vids = [v for _, v in eng.replanned_log]
        self.assertIn("F", replanned_vids)
        self.assertNotIn("M", replanned_vids)
        self.assertNotIn("N", replanned_vids)
        for vid in ("F", "G", "K", "M", "N"):
            self.assertEqual(eng.state[vid], VertexState.SUCCEEDED, vid)

    def test_completion_sequenced_before_update(self):
        eng = self._engine()
        eng.settle()                            # F#1 (ends t=2), M#1 start
        eng.schedule_update(2, self._update())  # seq after completion event
        eng.advance_to(2)
        # F#1 completed under v1 (valid); G started under the old graph.
        self.assertEqual(eng.state["G"], VertexState.RUNNING)
        # The update invalidates F's old result for the NEW graph: F re-planned.
        self.assertEqual(eng.state["F"], VertexState.RUNNING)
        self.assertEqual(started_ids(eng)[-1], "F#2")
        self.assertEqual(unlocks_of(eng, "K"), [])  # old attempt must not unlock K

        eng.run_to_completion()
        self.assertEqual(unlocks_of(eng, "K"), [4])  # unlocked only by F#2
        self.assertEqual(started_ids(eng).count("G#1"), 1)
        self.assertEqual(eng.state["G"], VertexState.SUCCEEDED)
        for vid in ("F", "G", "K", "M", "N"):
            self.assertEqual(eng.state[vid], VertexState.SUCCEEDED, vid)


class StageBarrierAndGraphShapeTest(unittest.TestCase):
    """Stage barriers, isolated vertices, multi-edges, quota validation."""

    def test_stage_barrier_and_graph_features(self):
        eng = Engine(quotas={"cpu": 10})
        eng.apply_update(GraphUpdate(
            add_vertices=[
                V("S1", stage=0, duration=1, resources={"cpu": 1}),
                V("S2", stage=0, duration=2, resources={"cpu": 1}),
                V("T1", stage=1, duration=1, resources={"cpu": 1}),
                V("ISO", stage=0, duration=1, resources={"cpu": 1}),
            ],
            add_edges=[
                Edge("e1", "S1", "T1"),
                Edge("e2", "S1", "T1"),  # multi-edge, distinct edge IDs
            ],
        ))
        eng.settle()
        self.assertEqual(started_ids(eng), ["ISO#1", "S1#1", "S2#1"])
        self.assertIn("S1", eng.blocking_report()["T1"])

        eng.advance_to(1)  # S1, ISO done; T1 held by stage barrier (S2 running)
        report = eng.blocking_report()
        self.assertIn("stage barrier", report["T1"])
        self.assertIn("S2", report["T1"])
        self.assertEqual(unlocks_of(eng, "T1"), [])

        eng.advance_to(2)  # S2 done -> barrier clears
        self.assertEqual(unlocks_of(eng, "T1"), [2])  # once despite multi-edge
        eng.run_to_completion()
        self.assertEqual(eng.state["T1"], VertexState.SUCCEEDED)

        # Execution levels and graph shape.
        self.assertEqual(eng.levels(), [["ISO", "S1", "S2"], ["T1"]])
        self.assertIn("ISO", eng.graph.vertices)  # isolated vertex kept
        multi = [e for e in eng.graph.edges.values()
                 if e.src == "S1" and e.dst == "T1"]
        self.assertEqual(len(multi), 2)  # multi-edges kept

    def test_quota_violation_rejected_atomically(self):
        eng = Engine(quotas={"cpu": 2})
        eng.apply_update(GraphUpdate(add_vertices=[V("A", resources={"cpu": 1})]))
        version_before = eng.version
        with self.assertRaises(UpdateRejected) as ctx:
            eng.apply_update(GraphUpdate(add_vertices=[
                V("Z", resources={"cpu": 99}),
            ]))
        self.assertIn("exceeding quota", " ".join(ctx.exception.reasons))
        self.assertEqual(eng.version, version_before)
        self.assertNotIn("Z", eng.graph.vertices)


if __name__ == "__main__":
    unittest.main()
