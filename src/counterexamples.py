"""Standalone property checks: python3.11 src/counterexamples.py

The checks fail loudly if any forbidden counterexample is possible.
"""

from __future__ import annotations

from automata import AutomatonError, Pipeline, RejectedInputError, StopConsumption, transduce
from samples import pipeline_stages, stopper


def partitions(items: tuple[object, ...]) -> list[tuple[tuple[object, ...], ...]]:
    if not items:
        return [()]
    if len(items) == 1:
        return [(items,)]
    result: list[tuple[tuple[object, ...], ...]] = []
    for cut in range(1, len(items) + 1):
        prefix = items[:cut]
        for suffix in partitions(items[cut:]):
            result.append((prefix, *suffix))
    return result


def assert_chunk_invariance() -> None:
    stages = pipeline_stages()
    text = tuple("abbx")
    expected = Pipeline(stages, buffer_size=1).run([text])
    for split in partitions(text):
        actual = Pipeline(stages, buffer_size=1).run(list(split))
        assert actual == expected, f"chunk boundary changed result: {split!r} -> {actual!r}"


def assert_multi_output_order() -> None:
    actual = transduce(stopper(), ["a", "a", "b"])
    expected = ("i", "i", "i", "i", "i", "FOUND")
    assert actual == expected


def assert_buffer_capacity() -> None:
    stages = pipeline_stages()

    def source() -> object:
        for value in "a" * 5000 + "b" * 5000 + "x" * 2:
            yield value

    result = Pipeline(stages, buffer_size=1).run_with_stats(source(), max_outputs=3)
    assert all(peak <= 1 for peak in result.peak_buffers)
    assert result.consumed < 50


def assert_consumer_cancel() -> None:
    seen = 0

    def consume_first(item: object) -> None:
        nonlocal seen
        seen += 1
        assert seen == 1
        raise StopConsumption

    def source() -> object:
        yield from ("a" for _ in range(10_000))

    result = Pipeline((stopper(),), buffer_size=1).consume(source(), consume_first)
    assert result.stopped
    assert result.consumed <= 4


def assert_invalid_definitions_fail() -> None:
    stages = pipeline_stages()
    try:
        Pipeline(stages, buffer_size=0)
    except AutomatonError:
        pass
    else:
        raise AssertionError("zero-capacity buffers were accepted")

    try:
        Pipeline(stages).run(["z"])
    except RejectedInputError:
        pass
    else:
        raise AssertionError("input outside the explicit alphabet was accepted")


def main() -> None:
    checks = (
        assert_chunk_invariance,
        assert_multi_output_order,
        assert_buffer_capacity,
        assert_consumer_cancel,
        assert_invalid_definitions_fail,
    )
    for check in checks:
        check()
        print(f"ok - {check.__name__}")


if __name__ == "__main__":
    main()
