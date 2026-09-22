from __future__ import annotations

from collections.abc import Iterable
import pathlib
import sys
import unittest


ROOT = pathlib.Path(__file__).parent
sys.path.insert(0, str(ROOT / "src"))

from automata import (  # noqa: E402
    EPSILON,
    AutomatonError,
    FiniteTransducer,
    Pipeline,
    RejectedInputError,
    StopConsumption,
    compose,
    transduce,
)
from samples import (  # noqa: E402
    expander,
    grouper,
    pairer,
    pipeline_stages,
    stop_signal_source,
    stopper,
)


def partitions(values: tuple[object, ...]) -> Iterable[tuple[str, ...]]:
    if not values:
        yield ()
        return
    if len(values) == 1:
        yield ("".join(values),)
        return
    for cut in range(1, len(values) + 1):
        prefix = "".join(values[:cut])
        for suffix in partitions(values[cut:]):
            yield (prefix, *suffix)


class TransducerTests(unittest.TestCase):
    def test_multi_outputs_preserve_order(self) -> None:
        self.assertEqual(
            transduce(expander(), ["abb"]),
            ("0", "1", "1", "0", "1", "0"),
        )

    def test_epsilon_outputs_are_filtered(self) -> None:
        stage = expander()
        self.assertEqual(transduce(stage, ["xxx"]), ())
        edge = stage._transitions["S"]["x"].outputs
        self.assertEqual(edge, (EPSILON,))

    def test_finish_outputs_flush_only_on_acceptance(self) -> None:
        accepted = transduce(grouper(), [("A1", "B1", "M")])
        self.assertEqual(accepted, ("01", "|", "END"))

        with self.assertRaises(RejectedInputError):
            transduce(grouper(), [("A1", "B1")])

    def test_missing_transition_is_an_explicit_definition_error(self) -> None:
        with self.assertRaises(AutomatonError):
            FiniteTransducer(
                states={"S"},
                input_alphabet="ab",
                output_alphabet=(),
                transitions={"S": {"a": ("S", ())}},
                initial="S",
                accepting={"S"},
            )

    def test_unknown_symbol_is_rejected_at_runtime(self) -> None:
        with self.assertRaises(RejectedInputError):
            transduce(pairer(), ["2"])


class PipelineTests(unittest.TestCase):
    STAGES = staticmethod(pipeline_stages)

    def test_chunk_boundary_invariance(self) -> None:
        stages = self.STAGES()
        expected = compose(stages, "abbx")
        for chunking in partitions(tuple("abbx")):
            with self.subTest(chunking=chunking):
                self.assertEqual(
                    Pipeline(stages, buffer_size=1).run(chunking),
                    expected,
                )

    def test_pipeline_matches_pure_reference(self) -> None:
        stages = self.STAGES()
        result = Pipeline(stages, buffer_size=2).run_with_stats(["a", "bb", "x"])
        self.assertEqual(result.outputs, compose(stages, "abbx"))
        self.assertEqual(result.consumed, 4)
        self.assertFalse(result.stopped)

    def test_intermediate_buffers_are_bounded(self) -> None:
        stages = self.STAGES()

        def source() -> Iterable[str]:
            yield from "a" * 10_000

        result = Pipeline(stages, buffer_size=2).run_with_stats(source(), max_outputs=5)
        self.assertTrue(result.stopped)
        self.assertTrue(all(peak <= 2 for peak in result.peak_buffers))
        self.assertLess(result.consumed, 100)

    def test_halting_stage_cancels_upstream(self) -> None:
        stages = (stop_signal_source(), stopper())

        def source() -> Iterable[str]:
            for index in range(10_000):
                yield "b" if index == 10 else "a"

        result = Pipeline(stages, buffer_size=1).run_with_stats(source())
        self.assertTrue(result.stopped)
        self.assertEqual(result.outputs[-1], "FOUND")
        self.assertLess(result.consumed, 50)

    def test_consumer_stop_cancels_source(self) -> None:
        consumed = 0

        def source() -> Iterable[str]:
            nonlocal consumed
            while True:
                consumed += 1
                yield "a"

        def stop_after_first(item: object) -> None:
            raise StopConsumption

        result = Pipeline((stopper(),), buffer_size=1).consume(source(), stop_after_first)
        self.assertTrue(result.stopped)
        self.assertEqual(result.outputs, ())
        self.assertLessEqual(consumed, 4)

    def test_downstream_rejection_cancels_and_reports_error(self) -> None:
        stages = self.STAGES()
        with self.assertRaises(RejectedInputError):
            Pipeline(stages, buffer_size=1).run(["z"])

    def test_alphabets_are_checked_between_stages(self) -> None:
        with self.assertRaises(AutomatonError):
            Pipeline((grouper(), stopper()))

    def test_empty_chunks_do_not_emit_or_finish_early(self) -> None:
        stages = self.STAGES()
        expected = Pipeline(stages).run(["abbx"])
        self.assertEqual(Pipeline(stages).run(["", "ab", "", "bx", ""]), expected)


if __name__ == "__main__":
    unittest.main(verbosity=2)
