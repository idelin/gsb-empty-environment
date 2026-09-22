import os
import random
import tempfile
import unittest

from alerting import (
    AlertSystem,
    FaultState,
    IncrementalRule,
    NotificationKind,
    Outbox,
    OutboxStatus,
    RecordingTransport,
    RuleVersion,
    Sample,
    SuppressionWindow,
    compute_episodes,
    compute_states,
    state_at,
)


def make_system(**rule_kw):
    sys = AlertSystem()
    rule = RuleVersion(
        rule_id=rule_kw.pop("rule_id", "cpu"),
        version=rule_kw.pop("version", 1),
        enter_threshold=rule_kw.pop("enter_threshold", 10),
        exit_threshold=rule_kw.pop("exit_threshold", 9),
        enter_duration=rule_kw.pop("enter_duration", 5),
        exit_duration=rule_kw.pop("exit_duration", 0),
    )
    sys.set_rule(rule)
    return sys


class TestTriggerAndRevoke(unittest.TestCase):
    """Enter threshold 10, duration 5: 11@0 and 12@6 fire at t=5;
    backfilling 8@3 must revoke the already-sent alert."""

    def test_fires_then_revoked_by_backfill(self):
        sys = make_system()
        sys.ingest(0, 11)
        sys.ingest(6, 12)
        emitted = sys.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.OPEN])
        alert_id = emitted[0].alert_id
        self.assertEqual(emitted[0].payload["fire_start"], 5)

        # Late-arriving historical data: the condition broke at t=3,
        # so the 5s duration was never sustained.
        sys.ingest(3, 8)
        emitted = sys.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.REVOKED])
        self.assertEqual(emitted[0].alert_id, alert_id)  # original alert id
        self.assertEqual(emitted[0].revision, 2)         # revision that corrected it

        # Audit keeps both events: nothing disappears.
        kinds = [(r.alert_id, r.kind) for r in sys.audit]
        self.assertEqual(
            kinds,
            [(alert_id, NotificationKind.OPEN), (alert_id, NotificationKind.REVOKED)],
        )
        # The new pending episode (fires at t=11) is beyond the data horizon:
        # no replacement notification is emitted.
        self.assertEqual(len(sys.outbox.entries), 2)

    def test_deferred_open_when_horizon_advances(self):
        sys = make_system()
        for t, v in [(0, 11), (6, 12), (3, 8)]:
            sys.ingest(t, v)
        sys.recompute()
        self.assertEqual(sys.outbox.entries, [])
        sys.ingest(15, 12)  # horizon passes t=11
        emitted = sys.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.OPEN])
        self.assertEqual(emitted[0].payload["fire_start"], 11)


class TestSuppression(unittest.TestCase):
    def test_recovery_during_suppression_is_never_notified(self):
        sys = make_system(enter_duration=1)
        sys.set_suppression(SuppressionWindow("maint", "cpu", 0, 10, version=1))
        sys.ingest(0, 20)  # fires at t=1, suppressed
        sys.ingest(8, 0)   # recovers at t=8, still inside the window
        emitted = sys.recompute()
        self.assertEqual(emitted, [])
        self.assertEqual(sys.state_at("cpu", 5), FaultState.SUPPRESSED)
        self.assertEqual(sys.state_at("cpu", 11), FaultState.RESOLVED)

        # Suppression lifts, more data arrives: the stale fault must not
        # be re-notified.
        sys.ingest(15, 0)
        emitted = sys.recompute()
        self.assertEqual(emitted, [])
        self.assertEqual(sys.outbox.entries, [])

    def test_still_firing_after_suppression_defers_open(self):
        sys = make_system(enter_duration=1)
        sys.set_suppression(SuppressionWindow("maint", "cpu", 0, 10, version=1))
        sys.ingest(0, 20)
        sys.ingest(20, 0)
        emitted = sys.recompute()
        kinds = [e.kind for e in emitted]
        self.assertEqual(kinds, [NotificationKind.OPEN, NotificationKind.RESOLVED])
        self.assertEqual(emitted[0].payload["open_time"], 10)  # deferred, not 1
        self.assertEqual(emitted[1].payload["resolve_time"], 20)

    def test_suppression_version_rollback(self):
        sys2 = make_system(enter_duration=1)
        sys2.set_suppression(SuppressionWindow("maint", "cpu", 0, 10, version=1))
        sys2.ingest(0, 20)
        sys2.ingest(8, 0)
        self.assertEqual(sys2.recompute(), [])

        # v2 shortens the window: the fault (fires at 1, resolves at 8) is
        # no longer fully covered -> open deferred to 5, then resolved.
        sys2.set_suppression(SuppressionWindow("maint", "cpu", 0, 5, version=2))
        emitted = sys2.recompute()
        self.assertEqual(
            [e.kind for e in emitted],
            [NotificationKind.OPEN, NotificationKind.RESOLVED],
        )
        self.assertEqual(emitted[0].payload["open_time"], 5)
        alert_id = emitted[0].alert_id

        # Rolling back to v1 makes the notification wrong: it is revoked,
        # not deleted.
        sys2.rollback_suppression("maint", 1)
        emitted = sys2.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.REVOKED])
        self.assertEqual(emitted[0].alert_id, alert_id)
        self.assertEqual(emitted[0].revision, 3)
        self.assertEqual(len(sys2.audit), 3)


class TestOverlappingRulesAndRollback(unittest.TestCase):
    def test_three_overlapping_rules(self):
        sys = AlertSystem()
        sys.set_rule(RuleVersion("warn", 1, 10, 9, enter_duration=5))
        sys.set_rule(RuleVersion("crit", 1, 15, 14, enter_duration=0))
        sys.set_rule(RuleVersion("mid", 1, 12, 11, enter_duration=3))
        sys.ingest(0, 11)   # warn pending
        sys.ingest(2, 16)   # crit fires immediately, mid pending
        sys.ingest(20, 0)   # everything resolves
        emitted = sys.recompute()

        opens = {e.alert_id: e for e in emitted if e.kind == NotificationKind.OPEN}
        self.assertEqual(len(opens), 3)
        fire_starts = {e.alert_id: e.payload["fire_start"] for e in opens.values()}
        by_rule = {a.rule_id: a.alert_id for a in sys.alerts.values()}
        self.assertEqual(fire_starts[by_rule["warn"]], 5)   # 0 + 5
        self.assertEqual(fire_starts[by_rule["crit"]], 2)   # 2 + 0
        self.assertEqual(fire_starts[by_rule["mid"]], 5)    # 2 + 3
        resolves = [e for e in emitted if e.kind == NotificationKind.RESOLVED]
        self.assertEqual(len(resolves), 3)
        self.assertTrue(all(e.payload["resolve_time"] == 20 for e in resolves))

    def test_rule_version_rollback(self):
        sys = make_system()
        sys.ingest(0, 11)
        sys.ingest(6, 12)
        emitted = sys.recompute()
        alert_v1 = emitted[0].alert_id
        self.assertEqual(emitted[0].revision, 1)

        # v2 raises the threshold: the alert never should have fired.
        sys.set_rule(RuleVersion("cpu", 2, 20, 19, enter_duration=5))
        emitted = sys.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.REVOKED])
        self.assertEqual(emitted[0].alert_id, alert_v1)
        self.assertEqual(emitted[0].revision, 2)

        # Roll back to v1: a fresh alert opens (the revoked one stays revoked).
        sys.rollback_rule("cpu", 1)
        emitted = sys.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.OPEN])
        self.assertNotEqual(emitted[0].alert_id, alert_v1)
        self.assertEqual(emitted[0].revision, 3)
        self.assertTrue(sys.alerts[alert_v1].revoked)

    def test_same_time_sample_correction(self):
        sys = make_system()
        sys.ingest(0, 11)
        sys.ingest(6, 12)
        emitted = sys.recompute()
        alert_id = emitted[0].alert_id

        # Correction: the t=0 sample was actually 5, not 11 (higher seq wins).
        sys.ingest(0, 5)
        emitted = sys.recompute()
        self.assertEqual([e.kind for e in emitted], [NotificationKind.REVOKED])
        self.assertEqual(emitted[0].alert_id, alert_id)


class TestOutboxRecovery(unittest.TestCase):
    def test_crash_between_record_and_confirm(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "outbox.json")
            outbox = Outbox(path)
            sys = AlertSystem(outbox)
            sys.set_rule(RuleVersion("cpu", 1, 10, 9, enter_duration=5))
            sys.ingest(0, 11)
            sys.ingest(6, 12)
            sys.recompute()

            transport = RecordingTransport()
            outbox.dispatch(transport)
            entry = outbox.entries[0]
            self.assertEqual(entry.status, OutboxStatus.SENT)  # recorded+sent, unconfirmed

            # Crash and restart before the confirm arrives.
            recovered = Outbox(path)
            self.assertEqual(recovered.entries[0].status, OutboxStatus.SENT)

            # Re-running reconciliation must not duplicate the notification.
            sys2 = AlertSystem(recovered)
            sys2.set_rule(RuleVersion("cpu", 1, 10, 9, enter_duration=5))
            sys2.samples = list(sys.samples)
            sys2.alerts = dict(sys.alerts)
            sys2._alert_seq = sys._alert_seq
            sys2.revision = sys.revision
            emitted = sys2.recompute()
            self.assertEqual(emitted, [])
            recovered.dispatch(transport)
            self.assertEqual(transport.delivered, [entry.dedupe_key])
            self.assertEqual(transport.duplicates, 0)

            # The confirm finally arrives after restart.
            recovered.confirm(entry.entry_id)
            self.assertEqual(recovered.unacked(), [])

            # A correction after recovery is not lost either.
            sys2.outbox = recovered
            sys2.ingest(3, 8)
            emitted = sys2.recompute()
            self.assertEqual([e.kind for e in emitted], [NotificationKind.REVOKED])
            recovered.dispatch(transport)
            self.assertEqual(len(transport.delivered), 2)

    def test_crash_before_dispatch_and_transport_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "outbox.json")
            outbox = Outbox(path)
            entry, _ = outbox.enqueue("alert-1", NotificationKind.OPEN, 1, {"x": 1})
            # Crash while PENDING (recorded, never sent).
            recovered = Outbox(path)
            self.assertEqual(recovered.entries[0].status, OutboxStatus.PENDING)

            transport = RecordingTransport(fail_keys={entry.dedupe_key})
            recovered.dispatch(transport)  # injected failure: stays PENDING
            self.assertEqual(recovered.entries[0].status, OutboxStatus.PENDING)

            transport.fail_keys.clear()
            recovered.dispatch(transport)
            self.assertEqual(recovered.entries[0].status, OutboxStatus.SENT)
            recovered.confirm(entry.entry_id)
            self.assertEqual(transport.delivered, [entry.dedupe_key])

            # Re-enqueue of the same event is a no-op.
            _, created = recovered.enqueue("alert-1", NotificationKind.OPEN, 1, {"x": 1})
            self.assertFalse(created)
            self.assertEqual(len(recovered.entries), 1)


class TestIncrementalVsRecompute(unittest.TestCase):
    def test_incremental_matches_full_recompute(self):
        rng = random.Random(42)
        rule = RuleVersion("cpu", 1, 10, 8, enter_duration=3, exit_duration=2)
        samples = []
        t = 0.0
        for _ in range(60):
            t += rng.choice([1.0, 2.0, 3.5])
            samples.append(Sample(t, rng.uniform(0, 20), seq=len(samples) + 1))

        for k in range(2, len(samples) + 1):
            prefix = samples[:k]
            tail = prefix[-1].event_time + 37.5
            inc = IncrementalRule(rule)
            for s in prefix:
                inc.add(s)
                self.assertFalse(inc.needs_recompute)
            inc.advance_to(tail)
            full = compute_episodes(
                prefix + [Sample(tail, prefix[-1].value, seq=10_000)], rule
            )
            self.assertEqual(inc.episodes, full, f"prefix of {k} samples")

    def test_backfill_flags_recompute(self):
        rule = RuleVersion("cpu", 1, 10, 9, enter_duration=5)
        inc = IncrementalRule(rule)
        inc.add(Sample(0, 11, 1))
        inc.add(Sample(6, 12, 2))
        self.assertFalse(inc.needs_recompute)
        inc.add(Sample(3, 8, 3))  # backfill
        self.assertTrue(inc.needs_recompute)

    def test_state_machine_transitions(self):
        rule = RuleVersion("cpu", 1, 10, 9, enter_duration=5, exit_duration=2)
        samples = [Sample(0, 11, 1), Sample(10, 12, 2), Sample(20, 0, 3), Sample(30, 0, 4)]
        segs = compute_states(samples, rule)
        self.assertEqual(state_at(segs, [], "cpu", 2), FaultState.PENDING)
        self.assertEqual(state_at(segs, [], "cpu", 6), FaultState.FIRING)
        self.assertEqual(state_at(segs, [], "cpu", 21), FaultState.FIRING)  # exit hysteresis
        self.assertEqual(state_at(segs, [], "cpu", 23), FaultState.RESOLVED)
        win = [SuppressionWindow("m", "cpu", 4, 8, 1)]
        self.assertEqual(state_at(segs, win, "cpu", 6), FaultState.SUPPRESSED)


if __name__ == "__main__":
    unittest.main()
