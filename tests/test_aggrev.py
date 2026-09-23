import random
import tempfile
import unittest
from pathlib import Path

from aggrev import (
    AtomicStore,
    ChangeEvent,
    ConflictError,
    Correction,
    Downstream,
    Publisher,
    UnknownParentsError,
    Upstream,
    collect,
    load_events,
    recompute,
    verify,
)

FIXTURES = Path(__file__).resolve().parent.parent / "fixtures"


def make_correction(rid, parents, delta, source="A", seq=1):
    return Correction(
        revision_id=rid,
        source=source,
        parents=tuple(parents),
        deltas={"accounts": delta},
        checkpoints={source: seq},
        txn_ids=(f"{rid}-txn",),
    )


def standard_flow():
    """Two upstreams, DAG: A-r1(V0), B-r1(V0), A-r2(A-r1,B-r1), B-r2(A-r2)."""
    events_a = load_events(FIXTURES / "source_a.json")
    events_b = load_events(FIXTURES / "source_b.json")
    publisher = Publisher()
    downstream = Downstream("ds-1")
    up_a = Upstream("A", publisher)
    up_b = Upstream("B", publisher)
    up_a.refresh()
    c_a1 = up_a.publish_batch(events_a[0:3])
    c_b1 = up_b.publish_batch(events_b[0:2])  # branches off V0
    up_a.refresh()
    c_a2 = up_a.publish_batch(events_a[3:6])  # merges the branches
    up_b.refresh()
    c_b2 = up_b.publish_batch(events_b[2:4])
    corrections = {"A-r1": c_a1, "B-r1": c_b1, "A-r2": c_a2, "B-r2": c_b2}
    return publisher, downstream, corrections, events_a + events_b


class TestEndToEnd(unittest.TestCase):
    def test_in_order_delivery(self):
        publisher, downstream, corr, events = standard_flow()
        for rid in ("A-r1", "B-r1", "A-r2", "B-r2"):
            result = downstream.receive(corr[rid])
            self.assertEqual(result.status, "applied")
        self.assertEqual(downstream.value(), {"accounts": 560})
        self.assertEqual(downstream.checkpoints(), {"A": 6, "B": 4})
        report = verify(events, publisher, downstream)
        self.assertTrue(report.ok, report.problems)

    def test_version_dependencies_form_dag_not_chain(self):
        publisher, _, corr, _ = standard_flow()
        self.assertEqual(corr["A-r1"].parents, ("V0",))
        self.assertEqual(corr["B-r1"].parents, ("V0",))  # branch
        self.assertEqual(set(corr["A-r2"].parents), {"V:A-r1", "V:B-r1"})  # merge
        versions = publisher.store.state["versions"]
        referenced = {p for v in versions.values() for p in v["parents"]}
        heads = [v for v in versions if v not in referenced]
        self.assertEqual(heads, ["V:B-r2"])


class TestGapWaiting(unittest.TestCase):
    def test_missing_intermediate_pauses_not_misapplies(self):
        _, downstream, corr, _ = standard_flow()
        result = downstream.receive(corr["A-r2"])  # parents not present yet
        self.assertEqual(result.status, "waiting")
        self.assertEqual(downstream.value(), {})  # paused, nothing added
        self.assertEqual(downstream.waiting(), ["A-r2"])

    def test_gap_fill_resumes_parked_correction(self):
        _, downstream, corr, _ = standard_flow()
        downstream.receive(corr["A-r2"])
        downstream.receive(corr["B-r1"])
        result = downstream.receive(corr["A-r1"])
        self.assertEqual(result.status, "applied")
        self.assertEqual(result.resumed, ["A-r2"])
        self.assertEqual(downstream.value(), {"accounts": 500})
        self.assertEqual(downstream.waiting(), [])

    def test_unfilled_gap_stays_paused(self):
        _, downstream, corr, _ = standard_flow()
        downstream.receive(corr["A-r2"])
        downstream.receive(corr["B-r1"])  # still missing V:A-r1
        self.assertEqual(downstream.waiting(), ["A-r2"])
        self.assertEqual(downstream.value(), {"accounts": 270})
        self.assertNotIn("A-r2", downstream.applied())


class TestIdempotencyAndConflicts(unittest.TestCase):
    def test_duplicate_correction_not_double_applied(self):
        _, downstream, corr, _ = standard_flow()
        downstream.receive(corr["A-r1"])
        result = downstream.receive(corr["A-r1"])
        self.assertEqual(result.status, "duplicate")
        self.assertEqual(downstream.value(), {"accounts": 170})

    def test_same_id_different_content_is_conflict(self):
        _, downstream, corr, _ = standard_flow()
        downstream.receive(corr["A-r1"])
        forged = make_correction("A-r1", ["V0"], 999)
        result = downstream.receive(forged)
        self.assertEqual(result.status, "conflict")
        self.assertEqual(downstream.value(), {"accounts": 170})
        self.assertEqual(len(downstream.conflicts()), 1)

    def test_publisher_republish_same_id_is_noop(self):
        publisher, _, corr, _ = standard_flow()
        self.assertEqual(publisher.publish(corr["A-r1"]), "duplicate")
        self.assertEqual(publisher.value(), {"accounts": 560})

    def test_publisher_conflict_on_same_id_different_content(self):
        publisher, _, _, _ = standard_flow()
        with self.assertRaises(ConflictError):
            publisher.publish(make_correction("A-r1", ["V0"], 1))

    def test_publisher_rejects_unknown_parents(self):
        publisher = Publisher()
        with self.assertRaises(UnknownParentsError):
            publisher.publish(make_correction("X-r1", ["V:ghost"], 5))


class TestCycleRejection(unittest.TestCase):
    def test_cyclic_dependency_rejected(self):
        downstream = Downstream("ds-1")
        c_x = make_correction("X", ["V:Y"], 10)
        c_y = make_correction("Y", ["V:X"], 20)
        self.assertEqual(downstream.receive(c_x).status, "waiting")
        self.assertEqual(downstream.receive(c_y).status, "waiting")
        self.assertEqual(
            downstream.rejected(),
            {"X": "cyclic dependency", "Y": "cyclic dependency"},
        )
        self.assertEqual(downstream.value(), {})
        self.assertEqual(downstream.waiting(), [])

    def test_dependent_of_rejected_is_rejected(self):
        downstream = Downstream("ds-1")
        downstream.receive(make_correction("X", ["V:Y"], 10))
        downstream.receive(make_correction("Y", ["V:X"], 20))
        result = downstream.receive(make_correction("Z", ["V:X"], 30))
        self.assertEqual(result.status, "rejected")
        self.assertEqual(downstream.value(), {})

    def test_self_cycle_rejected(self):
        downstream = Downstream("ds-1")
        result = downstream.receive(make_correction("S", ["V:S"], 5))
        self.assertEqual(result.status, "waiting")
        self.assertEqual(downstream.rejected(), {"S": "cyclic dependency"})


class TestOrderingAndRetraction(unittest.TestCase):
    def test_retract_then_readd_ordering(self):
        events = load_events(FIXTURES / "source_a.json")
        totals, problems = recompute(events)
        self.assertEqual(problems, [])
        # a2: insert 50 (seq2) -> delete (seq4) -> insert 80 (seq5)
        self.assertEqual(totals, {"accounts": 230})

    def test_shuffled_events_still_sorted_by_commit_seq(self):
        events_a = load_events(FIXTURES / "source_a.json")
        shuffled = list(events_a)
        random.Random(42).shuffle(shuffled)
        publisher = Publisher()
        up = Upstream("A", publisher)
        correction = up.publish_batch(shuffled)
        self.assertEqual(correction.deltas, {"accounts": 230})
        self.assertEqual(correction.checkpoints, {"A": 6})

    def test_verifier_detects_before_image_mismatch(self):
        events = load_events(FIXTURES / "source_a.json")
        # drop the update A-t3 (a1: 100->120); the later update A-t6 then
        # carries a before-image (120) that does not match the state (100)
        broken = [e for e in events if e.txn_id != "A-t3"]
        _, problems = recompute(broken)
        self.assertTrue(any("before-image mismatch" in p for p in problems))

    def test_verifier_detects_lost_retraction(self):
        events = load_events(FIXTURES / "source_a.json")
        broken = [e for e in events if e.txn_id != "A-t4"]  # drop the delete
        _, problems = recompute(broken)
        self.assertTrue(any("insert of existing row" in p for p in problems))


class TestStaleCorrection(unittest.TestCase):
    def test_old_base_correction_cannot_overwrite_new_result(self):
        publisher, downstream, corr, events = standard_flow()
        for rid in ("A-r1", "B-r1", "A-r2", "B-r2"):
            downstream.receive(corr[rid])
        self.assertEqual(downstream.value(), {"accounts": 560})
        # late correction computed against the genesis version (stale base)
        stale = make_correction("A-r9", ["V0"], 25, seq=2)
        result = downstream.receive(stale)
        self.assertEqual(result.status, "applied")
        # delta is added, the newer result is never overwritten
        self.assertEqual(downstream.value(), {"accounts": 585})
        # checkpoint does not regress to the stale seq
        self.assertEqual(downstream.checkpoints()["A"], 6)


class TestAtomicity(unittest.TestCase):
    def test_failed_commit_leaves_state_untouched(self):
        store = AtomicStore(initial={
            "value": {},
            "versions": {"V0": {"revision_id": None, "parents": []}},
            "applied": {},
            "acks": {},
            "checkpoints": {},
            "conflicts": [],
            "rejected": {},
        })
        downstream = Downstream("ds-1", store=store)

        def boom(_state):
            raise IOError("simulated crash during commit")

        store._persist = boom
        with self.assertRaises(IOError):
            downstream.receive(make_correction("A-r1", ["V0"], 100))
        # nothing partially applied: no value, no ack, no checkpoint
        self.assertEqual(store.state["value"], {})
        self.assertEqual(store.state["applied"], {})
        self.assertEqual(store.state["acks"], {})
        self.assertEqual(store.state["checkpoints"], {})

    def test_persistent_store_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = str(Path(tmp) / "state.json")
            store = AtomicStore(path=path, initial={
                "value": {},
                "versions": {"V0": {"revision_id": None, "parents": []}},
                "applied": {},
                "acks": {},
                "checkpoints": {},
                "conflicts": [],
                "rejected": {},
            })
            downstream = Downstream("ds-1", store=store)
            downstream.receive(make_correction("A-r1", ["V0"], 100))
            reloaded = AtomicStore(path=path)
            self.assertEqual(reloaded.state["value"], {"accounts": 100})
            self.assertEqual(reloaded.state["checkpoints"], {"A": 1})
            self.assertIn("A-r1", reloaded.state["acks"])


class TestGarbageCollection(unittest.TestCase):
    def test_gc_collects_only_acked_ancestors(self):
        publisher, downstream, corr, events = standard_flow()
        for rid in ("A-r1", "B-r1", "A-r2", "B-r2"):
            downstream.receive(corr[rid])
        # no ack yet -> nothing to collect
        self.assertEqual(collect(publisher), [])
        publisher.ack("ds-1", downstream.acked_versions(), downstream.checkpoints())
        collected = collect(publisher)
        self.assertEqual(sorted(collected), ["A-r1", "A-r2", "B-r1"])
        self.assertEqual(publisher.base(), {"accounts": 500})
        self.assertEqual(publisher.value(), {"accounts": 560})  # unchanged
        report = verify(events, publisher, downstream)
        self.assertTrue(report.ok, report.problems)

    def test_gc_requires_every_downstreams_ack(self):
        publisher, ds1, corr, _ = standard_flow()
        ds2 = Downstream("ds-2")
        for rid in ("A-r1", "B-r1", "A-r2", "B-r2"):
            ds1.receive(corr[rid])
        ds2.receive(corr["A-r1"])  # ds-2 far behind
        publisher.ack("ds-1", ds1.acked_versions(), ds1.checkpoints())
        publisher.ack("ds-2", ds2.acked_versions(), ds2.checkpoints())
        self.assertEqual(collect(publisher), [])  # nothing confirmed by both
        for rid in ("B-r1", "A-r2", "B-r2"):
            ds2.receive(corr[rid])
        publisher.ack("ds-2", ds2.acked_versions(), ds2.checkpoints())
        self.assertEqual(sorted(collect(publisher)), ["A-r1", "A-r2", "B-r1"])

    def test_collected_revision_redelivery_still_idempotent(self):
        publisher, downstream, corr, _ = standard_flow()
        for rid in ("A-r1", "B-r1", "A-r2", "B-r2"):
            downstream.receive(corr[rid])
        publisher.ack("ds-1", downstream.acked_versions(), downstream.checkpoints())
        collect(publisher)
        self.assertEqual(publisher.publish(corr["A-r1"]), "duplicate")
        self.assertEqual(publisher.value(), {"accounts": 560})


class TestVerifierIndependence(unittest.TestCase):
    def test_verifier_detects_tampered_downstream_value(self):
        publisher, downstream, corr, events = standard_flow()
        for rid in ("A-r1", "B-r1", "A-r2", "B-r2"):
            downstream.receive(corr[rid])
        downstream.store.state["value"]["accounts"] = 999  # tamper
        report = verify(events, publisher, downstream)
        self.assertFalse(report.ok)
        self.assertTrue(any("downstream value" in p for p in report.problems))

    def test_verifier_reports_incomplete_application(self):
        publisher, downstream, corr, events = standard_flow()
        downstream.receive(corr["A-r1"])
        downstream.receive(corr["B-r1"])
        report = verify(events, publisher, downstream)  # A-r2, B-r2 missing
        self.assertFalse(report.ok)
        self.assertTrue(any("downstream value" in p for p in report.problems))
        self.assertTrue(any("checkpoint" in p for p in report.problems))


if __name__ == "__main__":
    unittest.main()
