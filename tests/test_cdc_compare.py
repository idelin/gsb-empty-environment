from __future__ import annotations

import unittest

from cdc_compare import (
    ABSENT,
    ChangeEvent,
    EntityRef,
    ComparisonSession,
    InvalidFixtureError,
    Replica,
    WatermarkRegressionError,
    compare_replicas,
    snapshot,
)


def event(
    transaction_id: str,
    primary_key: str,
    before: object,
    after: object,
    commit_seq: int,
    table: str = "orders",
) -> ChangeEvent:
    return ChangeEvent(
        transaction_id=transaction_id,
        table=table,
        primary_key=primary_key,
        before_value=before,
        after_value=after,
        commit_seq=commit_seq,
    )


class CDCComparisonTests(unittest.TestCase):
    def test_consistent_at_explicit_common_watermark(self) -> None:
        left = Replica("left", 3, (event("t1", "k", None, 1, 1),))
        right = Replica("right", 3, (event("t1", "k", None, 1, 1),))

        result = compare_replicas(left, right, 3)

        self.assertEqual(result.status, "consistent")
        self.assertEqual(result.common_watermark, 3)
        self.assertEqual(result.missing_events, ())
        self.assertEqual(result.final_value_divergences, ())

    def test_lagging_replica_reports_only_lag(self) -> None:
        left = Replica("left", 4, (event("t1", "k", None, 1, 1),))
        right = Replica("right", 3, (event("t1", "k", None, 1, 1),))

        result = compare_replicas(left, right, 4)

        self.assertEqual(result.status, "lagging")
        self.assertEqual(result.lag.side, "right")
        self.assertEqual(result.lag.replica_name, "right")
        self.assertEqual(result.lag.requested_watermark, 4)
        self.assertEqual(result.lag.replica_high_water_mark, 3)
        self.assertEqual(result.missing_events, ())
        self.assertEqual(result.final_value_divergences, ())

    def test_default_uses_minimum_progress_without_false_positive(self) -> None:
        left = Replica(
            "left",
            4,
            (
                event("t1", "k", None, 1, 1),
                event("t2", "k", 1, 2, 4),
            ),
        )
        right = Replica("right", 3, (event("t1", "k", None, 1, 1),))

        result = compare_replicas(left, right)

        self.assertEqual(result.status, "consistent")
        self.assertEqual(result.common_watermark, 3)

    def test_snapshot_hides_events_after_common_watermark(self) -> None:
        replica = Replica(
            "replica",
            5,
            (
                event("t1", "k", None, 1, 1),
                event("t2", "k", 1, 2, 4),
            ),
        )

        summaries = snapshot(replica, 3)

        self.assertEqual(summaries[EntityRef("orders", "k")].final_value, 1)

    def test_missing_event_reported_even_when_final_value_matches(self) -> None:
        left = Replica(
            "left",
            3,
            (
                event("t1", "k", None, 1, 1),
                event("t2", "k", 1, None, 2),
                event("t3", "k", None, 1, 3),
            ),
        )
        right = Replica(
            "right",
            3,
            (
                event("t1", "k", None, 1, 1),
                event("t3", "k", None, 1, 3),
            ),
        )

        result = compare_replicas(left, right, 3)

        self.assertEqual(result.status, "divergent")
        self.assertEqual(len(result.missing_events), 1)
        missing = result.missing_events[0]
        self.assertEqual(missing.event_key.transaction_id, "t2")
        self.assertEqual(missing.side, "right")
        self.assertEqual(result.order_conflicts, ())
        self.assertEqual(result.event_value_divergences, ())
        self.assertEqual(result.final_value_divergences, ())

    def test_order_conflict_when_same_event_has_different_commit_sequence(self) -> None:
        left = Replica(
            "left",
            3,
            (
                event("t1", "k", None, 1, 1),
                event("t2", "k", 1, 2, 2),
            ),
        )
        right = Replica(
            "right",
            3,
            (
                event("t1", "k", None, 1, 1),
                event("t2", "k", 1, 2, 3),
            ),
        )

        result = compare_replicas(left, right, 3)

        self.assertEqual(len(result.order_conflicts), 1)
        conflict = result.order_conflicts[0]
        self.assertEqual(conflict.event_key.transaction_id, "t2")
        self.assertEqual(conflict.left_commit_seq, 2)
        self.assertEqual(conflict.right_commit_seq, 3)
        self.assertEqual(result.missing_events, ())

    def test_event_values_and_final_value_are_compared_separately(self) -> None:
        left = Replica(
            "left",
            2,
            (
                event("t1", "k", None, "a", 1),
                event("t2", "k", "a", "b", 2),
            ),
        )
        right = Replica(
            "right",
            2,
            (
                event("t1", "k", None, "a", 1),
                event("t2", "k", "a", "c", 2),
            ),
        )

        result = compare_replicas(left, right, 2)

        self.assertEqual(len(result.event_value_divergences), 1)
        self.assertEqual(len(result.final_value_divergences), 1)
        self.assertEqual(result.final_value_divergences[0].left_value, "b")
        self.assertEqual(result.final_value_divergences[0].right_value, "c")

    def test_entity_absent_on_one_side_is_final_value_divergence(self) -> None:
        left = Replica("left", 1, (event("t1", "k", None, "a", 1),))
        right = Replica("right", 1, ())

        result = compare_replicas(left, right, 1)

        self.assertEqual(len(result.final_value_divergences), 1)
        self.assertEqual(result.final_value_divergences[0].right_value, ABSENT)

    def test_session_rejects_watermark_regression(self) -> None:
        left = Replica("left", 5, (event("t1", "k", None, 1, 1),))
        right = Replica("right", 5, (event("t1", "k", None, 1, 1),))
        session = ComparisonSession()

        session.compare(left, right, 5)

        with self.assertRaises(WatermarkRegressionError):
            session.compare(left, right, 4)

    def test_lag_does_not_advance_session_watermark(self) -> None:
        left = Replica("left", 5, (event("t1", "k", None, 1, 1),))
        right = Replica("right", 5, (event("t1", "k", None, 1, 1),))
        session = ComparisonSession()

        lag_result = session.compare(left, right, 6)
        self.assertEqual(lag_result.status, "lagging")
        self.assertEqual(session.compare(left, right, 5).status, "consistent")

    def test_event_above_replica_watermark_is_invalid_fixture(self) -> None:
        with self.assertRaises(InvalidFixtureError):
            Replica("left", 1, (event("t1", "k", None, 1, 2),))

    def test_duplicate_event_identity_is_invalid_fixture(self) -> None:
        with self.assertRaises(InvalidFixtureError):
            Replica(
                "left",
                2,
                (
                    event("t1", "k", None, 1, 1),
                    event("t1", "k", 1, 2, 2),
                ),
            )


if __name__ == "__main__":
    unittest.main()
