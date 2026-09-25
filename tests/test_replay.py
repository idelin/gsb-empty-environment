import json
import os
import tempfile
import unittest

from hlc.network import Envelope, Network
from hlc.node import Node
from hlc.persistence import CrashPoint, InjectedCrash
from hlc.replayer import Replayer
from hlc.scenarios import MIXED_CLOCK_SCENARIO
from hlc.tag import Tag
from hlc.time_model import Duration, OffsetTable, SyntheticClock, WallReading
from hlc.trace import EventRecord, TraceCollector
from hlc.validator import CausalValidator


def chains(trace_events):
    out = {}
    for event in trace_events:
        if event.kind in ("local", "send", "receive"):
            out.setdefault(event.node, []).append(
                (event.tag.physical, event.tag.logical, event.tag.epoch)
            )
    return out


class TestMixedClockScenario(unittest.TestCase):
    def replay(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="hlc-test-")
        self.addCleanup(self.tmp.cleanup)
        return Replayer(MIXED_CLOCK_SCENARIO, self.tmp.name).run()

    def test_full_trace_validates(self):
        artifact = self.replay()
        self.assertTrue(
            artifact.validation["ok"], artifact.validation["errors"]
        )
        v = artifact.validation
        self.assertEqual(v["causal_edges"], 32)
        self.assertEqual(v["quarantined"], 1)
        self.assertEqual(v["duplicates"], 1)
        self.assertEqual(v["crashes"], 3)
        self.assertEqual(v["restarts"], 6)

    def test_exact_tag_chains(self):
        artifact = self.replay()
        expected = {
            "a": [
                (0xF4240, 1, 0), (0xF4240, 2, 0), (0xF4240, 3, 0),
                (0xF4240, 4, 0), (0xF4240, 5, 0),
                (0xF4DF8, 1, 0), (0xF4DF8, 2, 0),
                (0xF4DF9, 0, 0), (0xF4DF9, 1, 0),
                (0xF4DF9, 1, 1),
            ],
            "b": [
                (0xF4240, 1, 0), (0xF4240, 4, 0), (0xF4240, 5, 0),
                (0xF4240, 6, 0), (0xF4241, 2, 0), (0xF4241, 3, 0),
                (0xF4DF9, 2, 0),
            ],
            "c": [
                (0xF4240, 1, 0), (0xF4240, 2, 0), (0xF4240, 3, 0),
                (0xF4240, 4, 0), (0xF4240, 5, 0), (0xF4240, 6, 0),
                (0xF4240, 7, 0), (0xF4241, 0, 0), (0xF4241, 1, 0),
                (0xF4241, 2, 0), (0xF4DF8, 0, 0),
                (0xF4DF8, 1, 1),
            ],
        }
        self.assertEqual(chains(artifact.trace and [
            EventRecord(
                node=e["node"], kind=e["kind"], seq=e["seq"],
                tag=Tag.from_dict(e["tag"]) if e["tag"] else None,
                msg_id=e["msg_id"], peer=e["peer"],
            )
            for e in artifact.trace
        ]), expected)

    def test_replay_is_deterministic(self):
        with tempfile.TemporaryDirectory(prefix="hlc-r1-") as d1, \
                tempfile.TemporaryDirectory(prefix="hlc-r2-") as d2:
            a = Replayer(MIXED_CLOCK_SCENARIO, d1).run()
            b = Replayer(MIXED_CLOCK_SCENARIO, d2).run()
        self.assertEqual(a.to_json(), b.to_json())

    def test_artifact_is_serializable_json(self):
        artifact = self.replay()
        blob = artifact.to_json()
        self.assertEqual(json.loads(blob)["scenario"], "mixed_clock")


class TestCrashWindowsDirect(unittest.TestCase):
    def boot_node(self, path, node_id="x", fresh=True, start=WallReading(1_000)):
        network = Network()
        trace = TraceCollector()
        node = Node(
            node_id=node_id,
            datadir=path,
            network=network,
            trace=trace,
            start=start,
            table=OffsetTable.create(),
            max_logical=64,
            allowed_skew=Duration.seconds(60),
            fresh=fresh,
        )
        return node, network, trace

    def test_before_sync_leaves_nothing_and_restart_advances_epoch(self):
        with tempfile.TemporaryDirectory() as path:
            node, network, trace = self.boot_node(path)
            with self.assertRaises(InjectedCrash):
                node.send("m", "y", crash=CrashPoint.BEFORE_SYNC)
            self.assertFalse(node.alive)
            node2, network2, trace2 = self.boot_node(
                path, start=WallReading(900), fresh=False
            )
            tag = node2.local_event()
            self.assertEqual(tag.epoch, 1)
            # BEFORE_SYNC committed nothing, so there is no durable floor;
            # the incarnation simply follows the recovered wall reading
            self.assertEqual(tag.physical, 900)
            self.assertEqual(network2.pending("y"), 0)
            report = CausalValidator().validate(trace2.events)
            self.assertTrue(report.ok, report.errors)

    def test_after_sync_republishes_same_tag_and_peer_dedupes(self):
        with tempfile.TemporaryDirectory() as path:
            node, network, sender_trace = self.boot_node(path, node_id="x")
            with self.assertRaises(InjectedCrash):
                node.send("m", "y", crash=CrashPoint.AFTER_SYNC)
            node2, network2, trace2 = self.boot_node(
                path, node_id="x", start=WallReading(900), fresh=False
            )
            sender_events = list(sender_trace.events) + list(trace2.events)
            self.assertEqual(network2.pending("y"), 1)
            envelope = network2.next_for("y")
            self.assertEqual(envelope.tag.epoch, 0)
            self.assertEqual(envelope.tag.physical, 1_000)

            peer, peer_net, peer_trace = self.boot_node(path, node_id="y")
            peer_net.publish(envelope)
            first = peer.deliver()
            self.assertEqual(first.kind, "receive")
            self.assertGreater(first.tag, envelope.tag)
            # forced redelivery of the identical envelope
            peer_net.publish(envelope.clone_for_retry())
            second = peer.deliver()
            self.assertEqual(second.kind, "duplicate")
            # validator gets the full independent cross-node trace
            all_events = sender_events + peer_trace.events
            report = CausalValidator().validate(all_events)
            self.assertTrue(report.ok, report.errors)

    def test_after_publish_restart_does_not_reuse_tag(self):
        with tempfile.TemporaryDirectory() as path:
            node, network, _ = self.boot_node(path, node_id="x")
            with self.assertRaises(InjectedCrash):
                node.send("m", "y", crash=CrashPoint.AFTER_PUBLISH)
            self.assertEqual(network.pending("y"), 1)
            node2, network2, trace2 = self.boot_node(
                path, node_id="x", start=WallReading(900), fresh=False
            )
            tag = node2.local_event()
            self.assertEqual(tag.epoch, 1)
            self.assertGreater(tag, Tag(1_000, 1, 0, "x"))
            report = CausalValidator().validate(trace2.events)
            self.assertTrue(report.ok, report.errors)

    def test_far_future_is_quarantined_and_clock_unaffected(self):
        with tempfile.TemporaryDirectory() as path:
            node, network, trace = self.boot_node(path, node_id="x")
            before = node.local_event()
            evil = Tag(before.physical + 120_000_000, 0, 0, "attacker")  # 120s > 60s bound
            network.inject(Envelope("evil", "attacker", "x", evil))
            quarantine = node.deliver()
            self.assertEqual(quarantine.kind, "quarantine")
            after = node.local_event()
            self.assertEqual(after.physical, before.physical)
            self.assertEqual(after.logical, before.logical + 1)
            self.assertLess(after, evil)
            report = CausalValidator().validate(trace.events)
            self.assertTrue(report.ok, report.errors)
            self.assertEqual(report.quarantined, 1)


class TestValidatorIndependence(unittest.TestCase):
    def test_validator_detects_reused_tag_and_bad_edge(self):
        tag = Tag(100, 1, 0, "a")
        events = [
            EventRecord("a", "local", 0, tag),
            EventRecord("a", "local", 1, tag),  # reused
            EventRecord("a", "send", 2, tag, msg_id="m", peer="b"),
            EventRecord("b", "receive", 3, tag, msg_id="m", peer="a"),
        ]
        report = CausalValidator().validate(events)
        self.assertFalse(report.ok)
        self.assertTrue(any("reused" in e for e in report.errors))
        self.assertTrue(any("message edge" in e for e in report.errors))


if __name__ == "__main__":
    unittest.main()
