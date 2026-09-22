import unittest

from disordered_counter import (
    CounterAnalysisError,
    CounterAnalyzer,
    CounterConfig,
    Sample,
    SourceSpec,
    recompute_intervals,
    summarize,
)
from disordered_counter.intervals import make_interval_id


def analyzer() -> CounterAnalyzer:
    return CounterAnalyzer({"dev": SourceSpec(width=8, max_gap=10)}, "cfg-42")


class IntervalAnalysisTests(unittest.TestCase):
    def test_250_to_3_is_one_known_wrap(self) -> None:
        result = analyzer().add_samples(
            [
                Sample("a", "dev", 0, 250),
                Sample("b", "dev", 5, 3),
            ]
        )

        self.assertEqual(len(result), 1)
        interval = result[0].interval
        self.assertIsNotNone(interval)
        self.assertEqual(interval.status, "definite")
        self.assertEqual(interval.delta, 9)
        self.assertEqual(interval.left_sample_id, "a")
        self.assertEqual(interval.right_sample_id, "b")
        self.assertEqual(interval.config_version, "cfg-42")

    def test_explicit_reset_same_value_is_not_an_increment(self) -> None:
        engine = analyzer()
        engine.add_sample(Sample("a", "dev", 0, 10))
        engine.add_sample(Sample("b", "dev", 1, 10, reset=True))

        edge = engine.active_intervals[0]
        self.assertEqual(edge.status, "reset")
        self.assertIsNone(edge.delta)
        summary = engine.summary()
        source = summary.sources[0]
        self.assertEqual(source.known_delta, 0)
        self.assertTrue(source.exact)
        self.assertEqual(source.reset_boundaries, 1)
        self.assertEqual(
            [run.run_id for run in source.runs],
            [
                "dev:initial",
                "dev:after-reset:b",
            ],
        )

    def test_first_sample_with_reset_starts_one_post_reset_run(self) -> None:
        engine = analyzer()
        engine.add_sample(Sample("a", "dev", 0, 7, reset=True))

        source = engine.summary().sources[0]
        self.assertEqual(source.known_delta, 0)
        self.assertEqual(source.reset_boundaries, 0)
        self.assertEqual(len(source.runs), 1)
        self.assertEqual(source.runs[0].run_id, "dev:after-reset:a")

    def test_long_gap_is_unknown_until_every_subgap_is_decidable(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("a", "dev", 0, 0),
                Sample("j", "dev", 100, 20),
            ]
        )

        source = engine.summary().sources[0]
        self.assertFalse(source.exact)
        self.assertEqual(source.known_delta, 0)
        self.assertEqual(len(source.unknown_interval_ids), 1)
        self.assertEqual(engine.active_intervals[0].left_sample_id, "a")
        self.assertEqual(engine.active_intervals[0].right_sample_id, "j")

        engine.add_sample(Sample("b", "dev", 5, 10))
        source = engine.summary().sources[0]
        self.assertFalse(source.exact)
        self.assertEqual(source.known_delta, 10)
        self.assertEqual(len(source.unknown_interval_ids), 1)
        self.assertEqual(engine.active_intervals[-1].left_sample_id, "b")
        self.assertEqual(engine.active_intervals[-1].right_sample_id, "j")

        fill = [
            Sample("c", "dev", 15, 12),
            Sample("d", "dev", 25, 13),
            Sample("e", "dev", 35, 14),
            Sample("f", "dev", 45, 15),
            Sample("g", "dev", 55, 16),
            Sample("h", "dev", 65, 18),
            Sample("i", "dev", 75, 20),
            Sample("i2", "dev", 85, 20),
            Sample("i3", "dev", 95, 20),
        ]
        engine.add_samples(fill)
        source = engine.summary().sources[0]
        self.assertTrue(source.exact)
        self.assertEqual(source.known_delta, 20)
        self.assertEqual(source.unknown_interval_ids, ())
        self.assertTrue(engine.matches_reference())

    def test_late_valid_sample_conserves_definite_interval_sum(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("a", "dev", 0, 100),
                Sample("c", "dev", 8, 120),
            ]
        )
        self.assertEqual(engine.summary().known_delta, 20)

        revisions = engine.add_sample(Sample("b", "dev", 4, 107))
        actions = [(revision.action, revision.supersedes) for revision in revisions]
        self.assertEqual(
            actions,
            [
                ("delete", (make_interval_id("dev", "a", "c"),)),
                ("upsert", ()),
                ("upsert", ()),
            ],
        )
        self.assertEqual(engine.summary().known_delta, 20)

    def test_late_reset_invalidates_only_adjacent_interval(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("a", "dev", 0, 0),
                Sample("c", "dev", 4, 9),
                Sample("d", "dev", 6, 12),
            ]
        )
        unrelated_before = engine.active_intervals[0]
        unrelated_after = engine.active_intervals[1]

        revisions = engine.add_sample(Sample("b-reset", "dev", 2, 5, reset=True))

        self.assertEqual(
            [
                revision.supersedes
                for revision in revisions
                if revision.action == "delete"
            ],
            [(make_interval_id("dev", "a", "c"),)],
        )
        self.assertTrue(
            any(
                revision.interval is not None and revision.interval.status == "reset"
                for revision in revisions
            )
        )
        self.assertIn(
            unrelated_after.interval_id,
            {interval.interval_id for interval in engine.active_intervals},
        )
        self.assertNotIn(
            unrelated_before.interval_id,
            {interval.interval_id for interval in engine.active_intervals},
        )

    def test_unknown_gap_is_reported_as_incomplete_lower_bound(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("a", "dev", 0, 0),
                Sample("b", "dev", 4, 5),
                Sample("c", "dev", 20, 30),
            ]
        )

        summary = engine.summary()
        self.assertFalse(summary.exact)
        self.assertEqual(summary.known_delta, 5)
        self.assertEqual(len(summary.sources[0].unknown_interval_ids), 1)

    def test_retraction_repairs_only_adjacent_edges(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("a", "dev", 0, 0),
                Sample("b", "dev", 2, 5),
                Sample("c", "dev", 4, 9),
            ]
        )

        revisions = engine.retract_sample("b")
        self.assertEqual(
            [
                revision.interval.interval_id if revision.interval else None
                for revision in revisions
            ],
            [
                None,
                None,
                make_interval_id("dev", "a", "c"),
            ],
        )
        self.assertEqual(
            [revision.supersedes for revision in revisions[:2]],
            [
                (make_interval_id("dev", "a", "b"),),
                (make_interval_id("dev", "b", "c"),),
            ],
        )
        self.assertEqual(engine.summary().known_delta, 9)
        self.assertTrue(engine.matches_reference())

    def test_same_time_orders_by_seq_then_rejects_equal_seq(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("second", "dev", 10, 7, seq=1),
                Sample("first", "dev", 10, 5, seq=0),
            ]
        )

        self.assertEqual(
            [
                (interval.left_sample_id, interval.right_sample_id)
                for interval in engine.active_intervals
            ],
            [("first", "second")],
        )
        self.assertEqual(engine.active_intervals[0].delta, 2)

        with self.assertRaises(CounterAnalysisError):
            engine.add_sample(Sample("tie", "dev", 10, 8, seq=0))

    def test_reset_and_wrap_are_distinct_even_when_raw_values_match(self) -> None:
        config = CounterConfig({"dev": SourceSpec(8, 10)})
        wrapped = recompute_intervals(
            config,
            [Sample("a", "dev", 0, 250), Sample("b", "dev", 1, 3)],
        )
        reset = recompute_intervals(
            config,
            [
                Sample("c", "dev", 0, 250),
                Sample("d", "dev", 1, 3, reset=True),
            ],
        )

        self.assertEqual(wrapped[0].delta, 9)
        self.assertEqual(reset[0].status, "reset")
        self.assertIsNone(reset[0].delta)

    def test_full_sort_reference_summary_matches_engine(self) -> None:
        engine = analyzer()
        engine.add_samples(
            [
                Sample("a", "dev", 0, 250),
                Sample("c", "dev", 100, 20),
                Sample("b", "dev", 5, 3),
            ]
        )
        reference_intervals = recompute_intervals(
            engine.config, engine.active_samples()
        )
        reference_summary = summarize(
            engine.config,
            engine.active_samples(),
            reference_intervals,
        )

        self.assertEqual(engine.summary(), reference_summary)


if __name__ == "__main__":
    unittest.main()
